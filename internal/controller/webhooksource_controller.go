package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	hookbasesdk "github.com/hookbase/hookbase-go"
	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/hookbase"
	"github.com/hookbase/hookbase-operator/internal/metrics"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// WebhookSourceReconciler reconciles WebhookSource objects.
type WebhookSourceReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *hookbase.ClientManager
	RequeueAfter  time.Duration
}

// +kubebuilder:rbac:groups=hookbase.io,resources=webhooksources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=hookbase.io,resources=webhooksources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hookbase.io,resources=webhooksources/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch

func (r *WebhookSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("WebhookSource").Observe(time.Since(start).Seconds())
	}()

	// 1. Fetch CRD
	var source hookbasev1alpha1.WebhookSource
	if err := r.Get(ctx, req.NamespacedName, &source); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 2. Resolve API key and get SDK client
	sdkClient, err := r.ClientManager.GetClient(ctx, source.Namespace, source.Spec.APIKeyRef)
	if err != nil {
		log.Error(err, "failed to resolve API key")
		hookbasev1alpha1.SetCondition(&source.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "APIKeyError", err.Error(), source.Generation)
		_ = r.Status().Update(ctx, &source)
		metrics.ReconcileTotal.WithLabelValues("WebhookSource", "error").Inc()
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// 3. Handle deletion
	if !source.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&source, hookbasev1alpha1.Finalizer) {
			if source.Status.SourceID != "" {
				if err := sdkClient.Sources.Delete(ctx, source.Status.SourceID); err != nil {
					var notFound *hookbasesdk.NotFoundError
					if !errors.As(err, &notFound) {
						log.Error(err, "failed to delete source from Hookbase API")
						return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
					}
				}
				log.Info("deleted source from Hookbase API", "sourceId", source.Status.SourceID)
			}
			controllerutil.RemoveFinalizer(&source, hookbasev1alpha1.Finalizer)
			if err := r.Update(ctx, &source); err != nil {
				return ctrl.Result{}, err
			}
		}
		metrics.ReconcileTotal.WithLabelValues("WebhookSource", "deleted").Inc()
		return ctrl.Result{}, nil
	}

	// 4. Add finalizer
	if !controllerutil.ContainsFinalizer(&source, hookbasev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&source, hookbasev1alpha1.Finalizer)
		if err := r.Update(ctx, &source); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// 5. Skip if no changes (drift detection will still run on requeue)
	// Build params from spec
	provider := hookbasesdk.SourceProvider(source.Spec.Provider)
	createParams := &hookbasesdk.CreateSourceParams{
		Name:     source.Spec.Name,
		Provider: &provider,
	}
	if source.Spec.Slug != "" {
		createParams.Slug = &source.Spec.Slug
	}
	if source.Spec.Description != "" {
		createParams.Description = &source.Spec.Description
	}
	if source.Spec.VerifySignature != nil {
		createParams.VerifySignature = source.Spec.VerifySignature
	}
	if source.Spec.TransientMode != nil {
		createParams.TransientMode = source.Spec.TransientMode
	}
	if source.Spec.RateLimitPerMinute != nil {
		createParams.RateLimit = source.Spec.RateLimitPerMinute
	}
	if source.Spec.DedupStrategy != "" {
		ds := hookbasesdk.DedupStrategy(source.Spec.DedupStrategy)
		createParams.DedupStrategy = &ds
	}
	if source.Spec.DedupWindowHours != nil {
		createParams.DedupWindow = source.Spec.DedupWindowHours
	}
	if source.Spec.IPFilterMode != "" {
		mode := hookbasesdk.IPFilterMode(source.Spec.IPFilterMode)
		createParams.IPFilterMode = &mode
	}
	createParams.IPAllowlist = source.Spec.IPAllowlist
	createParams.IPDenylist = source.Spec.IPDenylist

	// 6. Create or Update
	if source.Status.SourceID == "" {
		// CREATE
		apiSource, err := sdkClient.Sources.Create(ctx, createParams)
		if err != nil {
			log.Error(err, "failed to create source in Hookbase API")
			hookbasev1alpha1.SetCondition(&source.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), source.Generation)
			_ = r.Status().Update(ctx, &source)
			metrics.ReconcileTotal.WithLabelValues("WebhookSource", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		source.Status.SourceID = apiSource.ID
		if apiSource.IngestURL != nil {
			source.Status.IngestURL = *apiSource.IngestURL
		}

		// Store signing secret in K8s Secret if it was auto-generated
		if apiSource.SigningSecret != nil && *apiSource.SigningSecret != "" {
			if err := r.storeSigningSecret(ctx, &source, *apiSource.SigningSecret); err != nil {
				log.Error(err, "failed to store signing secret")
			}
		}

		log.Info("created source in Hookbase API", "sourceId", apiSource.ID)
	} else {
		// UPDATE
		updateParams := &hookbasesdk.UpdateSourceParams{
			Name:            &source.Spec.Name,
			VerifySignature: source.Spec.VerifySignature,
			TransientMode:   source.Spec.TransientMode,
			RateLimit:       source.Spec.RateLimitPerMinute,
			IPAllowlist:     source.Spec.IPAllowlist,
			IPDenylist:      source.Spec.IPDenylist,
		}
		if source.Spec.Description != "" {
			updateParams.Description = &source.Spec.Description
		}
		if source.Spec.DedupStrategy != "" {
			ds := hookbasesdk.DedupStrategy(source.Spec.DedupStrategy)
			updateParams.DedupStrategy = &ds
		}
		if source.Spec.DedupWindowHours != nil {
			updateParams.DedupWindow = source.Spec.DedupWindowHours
		}
		if source.Spec.IPFilterMode != "" {
			mode := hookbasesdk.IPFilterMode(source.Spec.IPFilterMode)
			updateParams.IPFilterMode = &mode
		}

		if err := sdkClient.Sources.Update(ctx, source.Status.SourceID, updateParams); err != nil {
			var notFound *hookbasesdk.NotFoundError
			if errors.As(err, &notFound) {
				// Resource was deleted externally, recreate
				source.Status.SourceID = ""
				_ = r.Status().Update(ctx, &source)
				return ctrl.Result{Requeue: true}, nil
			}
			log.Error(err, "failed to update source in Hookbase API")
			hookbasev1alpha1.SetCondition(&source.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "UpdateFailed", err.Error(), source.Generation)
			_ = r.Status().Update(ctx, &source)
			metrics.ReconcileTotal.WithLabelValues("WebhookSource", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		log.V(1).Info("updated source in Hookbase API", "sourceId", source.Status.SourceID)
	}

	// 7. Set conditions
	now := metav1.Now()
	source.Status.LastSyncTime = &now
	source.Status.ObservedGeneration = source.Generation
	hookbasev1alpha1.SetCondition(&source.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "Source is synced with Hookbase API", source.Generation)
	hookbasev1alpha1.SetCondition(&source.Status.Conditions, hookbasev1alpha1.ConditionSynced,
		metav1.ConditionTrue, "Synced", fmt.Sprintf("Last synced at %s", now.Format(time.RFC3339)), source.Generation)

	if err := r.Status().Update(ctx, &source); err != nil {
		return ctrl.Result{}, err
	}

	metrics.ReconcileTotal.WithLabelValues("WebhookSource", "success").Inc()
	return ctrl.Result{RequeueAfter: r.RequeueAfter}, nil
}

// storeSigningSecret creates or updates a K8s Secret with the signing secret.
func (r *WebhookSourceReconciler) storeSigningSecret(ctx context.Context, source *hookbasev1alpha1.WebhookSource, signingSecret string) error {
	secretName := fmt.Sprintf("%s-signing-secret", source.Name)
	secretKey := "signingSecret"

	if source.Spec.SigningSecretRef != nil {
		secretName = source.Spec.SigningSecretRef.Name
		if source.Spec.SigningSecretRef.Key != "" {
			secretKey = source.Spec.SigningSecretRef.Key
		}
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: source.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "hookbase-operator",
				"hookbase.io/source":           source.Name,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			secretKey: []byte(signingSecret),
		},
	}

	// Set owner reference so Secret is cleaned up with the source
	if err := controllerutil.SetControllerReference(source, secret, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Namespace: source.Namespace, Name: secretName}, existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, secret)
	}
	if err != nil {
		return err
	}
	existing.Data = secret.Data
	return r.Update(ctx, existing)
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebhookSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.WebhookSource{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
