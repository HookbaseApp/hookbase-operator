package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/metrics"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const hookbaseIngressClass = "hookbase"

// IngressReconciler watches Ingress resources with ingressClassName: hookbase
// and auto-creates WebhookSource, WebhookTunnel, and WebhookRoute CRDs.
type IngressReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("Ingress").Observe(time.Since(start).Seconds())
	}()

	var ingress networkingv1.Ingress
	if err := r.Get(ctx, req.NamespacedName, &ingress); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Only handle ingresses with our class
	if ingress.Spec.IngressClassName == nil || *ingress.Spec.IngressClassName != hookbaseIngressClass {
		return ctrl.Result{}, nil
	}

	// Handle deletion — owned resources are cleaned up by garbage collection
	if !ingress.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Read annotations for configuration
	annotations := ingress.Annotations
	if annotations == nil {
		annotations = map[string]string{}
	}
	sourceProvider := annotations["hookbase.io/source-provider"]
	if sourceProvider == "" {
		sourceProvider = "generic"
	}
	apiKeyRef := annotations["hookbase.io/api-key-ref"]
	transformRef := annotations["hookbase.io/route-transform-ref"]

	// Process each rule
	for _, rule := range ingress.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}

		for _, path := range rule.HTTP.Paths {
			backend := path.Backend
			if backend.Service == nil {
				continue
			}

			port := 80
			if backend.Service.Port.Number != 0 {
				port = int(backend.Service.Port.Number)
			}

			// Generate resource names from ingress name + rule
			baseName := fmt.Sprintf("%s-%s", ingress.Name, sanitizeName(rule.Host))

			// 1. Ensure WebhookSource
			sourceName := baseName + "-source"
			if err := r.ensureWebhookSource(ctx, &ingress, sourceName, sourceProvider, apiKeyRef, annotations); err != nil {
				log.Error(err, "failed to ensure WebhookSource", "name", sourceName)
				continue
			}

			// 2. Ensure WebhookTunnel with sidecar
			tunnelName := baseName + "-tunnel"
			if err := r.ensureWebhookTunnel(ctx, &ingress, tunnelName, backend.Service.Name, port, apiKeyRef); err != nil {
				log.Error(err, "failed to ensure WebhookTunnel", "name", tunnelName)
				continue
			}

			// 3. Ensure companion WebhookDestination for the tunnel
			destName := tunnelName + "-dest"
			if err := r.ensureWebhookDestination(ctx, &ingress, destName, tunnelName, backend.Service.Name, port, apiKeyRef); err != nil {
				log.Error(err, "failed to ensure WebhookDestination", "name", destName)
				continue
			}

			// 4. Ensure WebhookRoute connecting source → tunnel destination
			routeName := baseName + "-route"
			if err := r.ensureWebhookRoute(ctx, &ingress, routeName, sourceName, tunnelName, transformRef, apiKeyRef); err != nil {
				log.Error(err, "failed to ensure WebhookRoute", "name", routeName)
				continue
			}

			log.Info("reconciled ingress rule", "host", rule.Host, "service", backend.Service.Name)
		}
	}

	// Update ingress status with tunnel public URLs
	r.updateIngressStatus(ctx, &ingress)

	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *IngressReconciler) ensureWebhookSource(ctx context.Context, ingress *networkingv1.Ingress, name, provider, apiKeyRef string, annotations map[string]string) error {
	source := &hookbasev1alpha1.WebhookSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ingress.Namespace,
			Labels: map[string]string{
				"hookbase.io/managed-by": "ingress-controller",
				"hookbase.io/ingress":    ingress.Name,
			},
		},
	}

	existing := &hookbasev1alpha1.WebhookSource{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: name}, existing)
	if err == nil {
		return nil // Already exists
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	source.Spec = hookbasev1alpha1.WebhookSourceSpec{
		Name:     name,
		Provider: provider,
	}

	// Apply signing secret ref from annotation
	if sigRef := annotations["hookbase.io/source-signing-secret-ref"]; sigRef != "" {
		parts := strings.SplitN(sigRef, ":", 2)
		ref := &hookbasev1alpha1.SecretKeyRef{Name: parts[0]}
		if len(parts) > 1 {
			ref.Key = parts[1]
		}
		source.Spec.SigningSecretRef = ref
	}

	if reject := sourceRejectInvalidSignatures(annotations); reject != nil {
		source.Spec.RejectInvalidSignatures = reject
	}

	if apiKeyRef != "" {
		source.Spec.APIKeyRef = &hookbasev1alpha1.SecretKeyRef{Name: apiKeyRef}
	}

	if err := controllerutil.SetControllerReference(ingress, source, r.Scheme); err != nil {
		return err
	}

	return r.Create(ctx, source)
}

// sourceRejectInvalidSignatures reads spec.rejectInvalidSignatures from the Ingress annotations.
// hookbase.io/source-reject-invalid-signatures is the preferred name; the older
// hookbase.io/source-verify-signature is still honoured (it always meant the same API setting)
// when the preferred one is absent. Values that do not parse as a bool are ignored.
func sourceRejectInvalidSignatures(annotations map[string]string) *bool {
	for _, key := range []string{"hookbase.io/source-reject-invalid-signatures", "hookbase.io/source-verify-signature"} {
		if raw, ok := annotations[key]; ok {
			if v, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
				return &v
			}
		}
	}
	return nil
}

func (r *IngressReconciler) ensureWebhookTunnel(ctx context.Context, ingress *networkingv1.Ingress, name, serviceName string, port int, apiKeyRef string) error {
	existing := &hookbasev1alpha1.WebhookTunnel{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: name}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	tunnel := &hookbasev1alpha1.WebhookTunnel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ingress.Namespace,
			Labels: map[string]string{
				"hookbase.io/managed-by": "ingress-controller",
				"hookbase.io/ingress":    ingress.Name,
			},
		},
		Spec: hookbasev1alpha1.WebhookTunnelSpec{
			Name:          name,
			TargetPort:    port,
			TargetService: serviceName,
		},
	}

	if apiKeyRef != "" {
		tunnel.Spec.APIKeyRef = &hookbasev1alpha1.SecretKeyRef{Name: apiKeyRef}
	}

	if err := controllerutil.SetControllerReference(ingress, tunnel, r.Scheme); err != nil {
		return err
	}

	return r.Create(ctx, tunnel)
}

func (r *IngressReconciler) ensureWebhookDestination(ctx context.Context, ingress *networkingv1.Ingress, name, tunnelName, serviceName string, port int, apiKeyRef string) error {
	existing := &hookbasev1alpha1.WebhookDestination{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: name}, existing)
	if err == nil {
		// Update the URL if the tunnel now has a public URL
		var tunnel hookbasev1alpha1.WebhookTunnel
		if tErr := r.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: tunnelName}, &tunnel); tErr == nil && tunnel.Status.PublicURL != "" {
			if existing.Spec.URL != tunnel.Status.PublicURL {
				existing.Spec.URL = tunnel.Status.PublicURL
				return r.Update(ctx, existing)
			}
		}
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	// Resolve tunnel public URL if available
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, ingress.Namespace, port)
	var tunnel hookbasev1alpha1.WebhookTunnel
	if tErr := r.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: tunnelName}, &tunnel); tErr == nil && tunnel.Status.PublicURL != "" {
		url = tunnel.Status.PublicURL
	}

	dest := &hookbasev1alpha1.WebhookDestination{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ingress.Namespace,
			Labels: map[string]string{
				"hookbase.io/managed-by": "ingress-controller",
				"hookbase.io/ingress":    ingress.Name,
			},
		},
		Spec: hookbasev1alpha1.WebhookDestinationSpec{
			Name:   name,
			URL:    url,
			Method: "POST",
		},
	}

	if apiKeyRef != "" {
		dest.Spec.APIKeyRef = &hookbasev1alpha1.SecretKeyRef{Name: apiKeyRef}
	}

	if err := controllerutil.SetControllerReference(ingress, dest, r.Scheme); err != nil {
		return err
	}

	return r.Create(ctx, dest)
}

func (r *IngressReconciler) ensureWebhookRoute(ctx context.Context, ingress *networkingv1.Ingress, name, sourceRef, tunnelRef, transformRef, apiKeyRef string) error {
	existing := &hookbasev1alpha1.WebhookRoute{}
	err := r.Get(ctx, types.NamespacedName{Namespace: ingress.Namespace, Name: name}, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	// Note: The route references a "destination" but for tunnel-based ingress,
	// we use the tunnel's auto-created destination. This is a simplification —
	// in practice, the tunnel reconciler would create a companion destination.
	route := &hookbasev1alpha1.WebhookRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ingress.Namespace,
			Labels: map[string]string{
				"hookbase.io/managed-by": "ingress-controller",
				"hookbase.io/ingress":    ingress.Name,
			},
		},
		Spec: hookbasev1alpha1.WebhookRouteSpec{
			Name:           name,
			SourceRef:      sourceRef,
			DestinationRef: tunnelRef + "-dest", // Convention: tunnel creates companion destination
		},
	}

	if transformRef != "" {
		route.Spec.TransformRef = transformRef
	}
	if apiKeyRef != "" {
		route.Spec.APIKeyRef = &hookbasev1alpha1.SecretKeyRef{Name: apiKeyRef}
	}

	if err := controllerutil.SetControllerReference(ingress, route, r.Scheme); err != nil {
		return err
	}

	return r.Create(ctx, route)
}

func (r *IngressReconciler) updateIngressStatus(ctx context.Context, ingress *networkingv1.Ingress) {
	// Collect tunnel public URLs for ingress status
	var tunnelList hookbasev1alpha1.WebhookTunnelList
	if err := r.List(ctx, &tunnelList,
		client.InNamespace(ingress.Namespace),
		client.MatchingLabels{"hookbase.io/ingress": ingress.Name},
	); err != nil {
		return
	}

	var lbIngress []networkingv1.IngressLoadBalancerIngress
	for _, t := range tunnelList.Items {
		if t.Status.PublicURL != "" {
			lbIngress = append(lbIngress, networkingv1.IngressLoadBalancerIngress{
				Hostname: t.Status.PublicURL,
			})
		}
	}

	if len(lbIngress) > 0 {
		ingress.Status.LoadBalancer.Ingress = lbIngress
		_ = r.Status().Update(ctx, ingress)
	}
}

func sanitizeName(s string) string {
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, "/", "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.TrimRight(s, "-")
}

func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Owns(&hookbasev1alpha1.WebhookSource{}).
		Owns(&hookbasev1alpha1.WebhookDestination{}).
		Owns(&hookbasev1alpha1.WebhookTunnel{}).
		Owns(&hookbasev1alpha1.WebhookRoute{}).
		Complete(r)
}
