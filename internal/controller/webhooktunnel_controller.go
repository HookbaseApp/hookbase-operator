package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strings"

	hookbasesdk "github.com/hookbase/hookbase-go"
	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/hookbase"
	"github.com/hookbase/hookbase-operator/internal/metrics"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	agentContainerName = "hookbase-agent"
	defaultAgentImage  = "ghcr.io/hookbaseapp/hookbase-agent:latest"
)

type WebhookTunnelReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	ClientManager     *hookbase.ClientManager
	DefaultAgentImage string
	HookbaseBaseURL   string
	RequeueAfter      time.Duration
}

func (r *WebhookTunnelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("WebhookTunnel").Observe(time.Since(start).Seconds())
	}()

	var tunnel hookbasev1alpha1.WebhookTunnel
	if err := r.Get(ctx, req.NamespacedName, &tunnel); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	sdkClient, err := r.ClientManager.GetClient(ctx, tunnel.Namespace, tunnel.Spec.APIKeyRef)
	if err != nil {
		hookbasev1alpha1.SetCondition(&tunnel.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "APIKeyError", err.Error(), tunnel.Generation)
		_ = r.Status().Update(ctx, &tunnel)
		metrics.ReconcileTotal.WithLabelValues("WebhookTunnel", "error").Inc()
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Handle deletion
	if !tunnel.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&tunnel, hookbasev1alpha1.Finalizer) {
			// Remove sidecar if injected
			if tunnel.Status.SidecarInjected && tunnel.Spec.SidecarInjection != nil {
				if err := r.removeSidecar(ctx, tunnel.Namespace, tunnel.Spec.SidecarInjection.DeploymentRef); err != nil {
					log.Error(err, "failed to remove sidecar")
				}
			}

			if tunnel.Status.TunnelID != "" {
				if err := sdkClient.Tunnels.Delete(ctx, tunnel.Status.TunnelID); err != nil {
					var notFound *hookbasesdk.NotFoundError
					if !errors.As(err, &notFound) {
						log.Error(err, "failed to delete tunnel")
						return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
					}
				}
			}
			controllerutil.RemoveFinalizer(&tunnel, hookbasev1alpha1.Finalizer)
			if err := r.Update(ctx, &tunnel); err != nil {
				return ctrl.Result{}, err
			}
		}
		metrics.ReconcileTotal.WithLabelValues("WebhookTunnel", "deleted").Inc()
		metrics.TunnelConnected.WithLabelValues(tunnel.Name, tunnel.Namespace).Set(0)
		return ctrl.Result{}, nil
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(&tunnel, hookbasev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&tunnel, hookbasev1alpha1.Finalizer)
		if err := r.Update(ctx, &tunnel); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if tunnel.Status.TunnelID == "" {
		// CREATE
		params := &hookbasesdk.CreateTunnelParams{
			Name:      tunnel.Spec.Name,
			LocalPort: tunnel.Spec.TargetPort,
		}
		if tunnel.Spec.Subdomain != "" {
			params.Subdomain = &tunnel.Spec.Subdomain
		}

		apiTunnel, err := sdkClient.Tunnels.Create(ctx, params)
		if err != nil {
			log.Error(err, "failed to create tunnel")
			hookbasev1alpha1.SetCondition(&tunnel.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), tunnel.Generation)
			_ = r.Status().Update(ctx, &tunnel)
			metrics.ReconcileTotal.WithLabelValues("WebhookTunnel", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		tunnel.Status.TunnelID = apiTunnel.ID
		if apiTunnel.PublicURL != nil {
			tunnel.Status.PublicURL = *apiTunnel.PublicURL
		}

		// Store auth token in a K8s Secret (only available on create)
		if apiTunnel.AuthToken != nil && *apiTunnel.AuthToken != "" {
			authSecretName := r.getAuthSecretName(&tunnel)
			authSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      authSecretName,
					Namespace: tunnel.Namespace,
					Labels: map[string]string{
						"hookbase.io/tunnel": tunnel.Name,
					},
				},
				Data: map[string][]byte{
					"authToken": []byte(*apiTunnel.AuthToken),
				},
			}
			// Set owner reference so Secret is cleaned up with the tunnel
			if err := ctrl.SetControllerReference(&tunnel, authSecret, r.Scheme); err != nil {
				log.Error(err, "failed to set owner reference on auth secret")
			}
			if err := r.Create(ctx, authSecret); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					log.Error(err, "failed to create auth token secret")
					hookbasev1alpha1.SetCondition(&tunnel.Status.Conditions, hookbasev1alpha1.ConditionReady,
						metav1.ConditionFalse, "SecretCreateFailed", err.Error(), tunnel.Generation)
					_ = r.Status().Update(ctx, &tunnel)
					return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
				}
			}
			log.Info("created auth token secret", "secret", authSecretName)
		}

		log.Info("created tunnel", "tunnelId", apiTunnel.ID)
	}

	// Inject or update sidecar
	if tunnel.Spec.SidecarInjection != nil {
		authSecretName := r.getAuthSecretName(&tunnel)

		if err := r.injectSidecar(ctx, &tunnel, authSecretName); err != nil {
			log.Error(err, "failed to inject sidecar")
			hookbasev1alpha1.SetCondition(&tunnel.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "SidecarInjectionFailed", err.Error(), tunnel.Generation)
			_ = r.Status().Update(ctx, &tunnel)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		tunnel.Status.SidecarInjected = true
	}

	now := metav1.Now()
	tunnel.Status.LastSyncTime = &now
	tunnel.Status.ObservedGeneration = tunnel.Generation
	tunnel.Status.ConnectionStatus = "configured"
	hookbasev1alpha1.SetCondition(&tunnel.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "Tunnel is synced", tunnel.Generation)
	hookbasev1alpha1.SetCondition(&tunnel.Status.Conditions, hookbasev1alpha1.ConditionSynced,
		metav1.ConditionTrue, "Synced", fmt.Sprintf("Last synced at %s", now.Format(time.RFC3339)), tunnel.Generation)

	_ = r.Status().Update(ctx, &tunnel)
	metrics.ReconcileTotal.WithLabelValues("WebhookTunnel", "success").Inc()
	return ctrl.Result{RequeueAfter: r.RequeueAfter}, nil
}

func (r *WebhookTunnelReconciler) getAuthSecretName(tunnel *hookbasev1alpha1.WebhookTunnel) string {
	if tunnel.Spec.AuthTokenSecretRef != nil {
		return tunnel.Spec.AuthTokenSecretRef.Name
	}
	return fmt.Sprintf("%s-tunnel-auth", tunnel.Name)
}

func (r *WebhookTunnelReconciler) injectSidecar(ctx context.Context, tunnel *hookbasev1alpha1.WebhookTunnel, authSecretName string) error {
	log := log.FromContext(ctx)
	sidecar := tunnel.Spec.SidecarInjection

	// Fetch target deployment
	var deployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: tunnel.Namespace, Name: sidecar.DeploymentRef}, &deployment); err != nil {
		return fmt.Errorf("target deployment %q not found: %w", sidecar.DeploymentRef, err)
	}

	// Determine agent image
	agentImage := r.DefaultAgentImage
	if agentImage == "" {
		agentImage = defaultAgentImage
	}
	if sidecar.AgentImage != "" {
		agentImage = sidecar.AgentImage
	}

	// Determine target host
	targetHost := "localhost"
	if tunnel.Spec.TargetService != "" {
		targetHost = fmt.Sprintf("%s.%s.svc.cluster.local", tunnel.Spec.TargetService, tunnel.Namespace)
	}

	// Derive WebSocket URL from the operator's base URL
	wsURL := "wss://api.hookbase.app/tunnel/ws"
	if r.HookbaseBaseURL != "" {
		base := r.HookbaseBaseURL
		base = strings.Replace(base, "https://", "wss://", 1)
		base = strings.Replace(base, "http://", "ws://", 1)
		wsURL = strings.TrimRight(base, "/") + "/tunnel/ws"
	}

	// Build agent container
	agentContainer := corev1.Container{
		Name:            agentContainerName,
		Image:           agentImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env: []corev1.EnvVar{
			{Name: "HOOKBASE_WS_URL", Value: wsURL},
			{Name: "HOOKBASE_TUNNEL_ID", Value: tunnel.Status.TunnelID},
			{Name: "HOOKBASE_TARGET_HOST", Value: targetHost},
			{Name: "HOOKBASE_TARGET_PORT", Value: fmt.Sprintf("%d", tunnel.Spec.TargetPort)},
			{
				Name: "HOOKBASE_AUTH_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: authSecretName},
						Key:                  "authToken",
					},
				},
			},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("16Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
		},
	}

	// Override resources if specified
	if sidecar.Resources != nil {
		agentContainer.Resources = *sidecar.Resources
	}

	// Check if container already exists and update/add
	containers := deployment.Spec.Template.Spec.Containers
	found := false
	for i, c := range containers {
		if c.Name == agentContainerName {
			containers[i] = agentContainer
			found = true
			break
		}
	}
	if !found {
		containers = append(containers, agentContainer)
	}
	deployment.Spec.Template.Spec.Containers = containers

	// Add annotation for tracking
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations["hookbase.io/tunnel"] = tunnel.Name
	deployment.Spec.Template.Annotations["hookbase.io/tunnel-id"] = tunnel.Status.TunnelID

	if err := r.Update(ctx, &deployment); err != nil {
		return fmt.Errorf("failed to update deployment %q: %w", sidecar.DeploymentRef, err)
	}

	log.Info("injected sidecar into deployment", "deployment", sidecar.DeploymentRef)
	return nil
}

func (r *WebhookTunnelReconciler) removeSidecar(ctx context.Context, namespace, deploymentName string) error {
	var deployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: deploymentName}, &deployment); err != nil {
		if apierrors.IsNotFound(err) {
			return nil // Deployment already gone
		}
		return err
	}

	// Remove agent container
	containers := make([]corev1.Container, 0, len(deployment.Spec.Template.Spec.Containers))
	for _, c := range deployment.Spec.Template.Spec.Containers {
		if c.Name != agentContainerName {
			containers = append(containers, c)
		}
	}
	deployment.Spec.Template.Spec.Containers = containers

	// Remove annotations
	delete(deployment.Spec.Template.Annotations, "hookbase.io/tunnel")
	delete(deployment.Spec.Template.Annotations, "hookbase.io/tunnel-id")

	return r.Update(ctx, &deployment)
}

func (r *WebhookTunnelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.WebhookTunnel{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
