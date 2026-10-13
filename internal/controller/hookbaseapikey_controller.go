package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	hookbasesdk "github.com/HookbaseApp/hookbase-go"
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

type HookbaseAPIKeyReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *hookbase.ClientManager
	RequeueAfter  time.Duration
}

func (r *HookbaseAPIKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("HookbaseAPIKey").Observe(time.Since(start).Seconds())
	}()

	var apiKey hookbasev1alpha1.HookbaseAPIKey
	if err := r.Get(ctx, req.NamespacedName, &apiKey); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// API key creation uses the bootstrap key, not its own key
	sdkClient, err := r.ClientManager.GetClient(ctx, apiKey.Namespace, apiKey.Spec.BootstrapAPIKeySecretRef)
	if err != nil {
		hookbasev1alpha1.SetCondition(&apiKey.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "BootstrapKeyError", err.Error(), apiKey.Generation)
		_ = r.Status().Update(ctx, &apiKey)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if !apiKey.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&apiKey, hookbasev1alpha1.Finalizer) {
			if apiKey.Status.APIKeyID != "" {
				if err := sdkClient.APIKeys.Delete(ctx, apiKey.Status.APIKeyID); err != nil {
					var notFound *hookbasesdk.NotFoundError
					if !errors.As(err, &notFound) {
						return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
					}
				}
			}
			// Invalidate cached clients that used this key
			r.ClientManager.InvalidateCache(apiKey.Namespace, apiKey.Spec.SecretRef.Name)
			controllerutil.RemoveFinalizer(&apiKey, hookbasev1alpha1.Finalizer)
			return ctrl.Result{}, r.Update(ctx, &apiKey)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&apiKey, hookbasev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&apiKey, hookbasev1alpha1.Finalizer)
		if err := r.Update(ctx, &apiKey); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if apiKey.Status.APIKeyID == "" {
		// CREATE - API keys can only be created, not updated for scopes
		params := &hookbasesdk.CreateAPIKeyParams{
			Name:          apiKey.Spec.Name,
			Scopes:        apiKey.Spec.Scopes,
			ExpiresInDays: apiKey.Spec.ExpiresInDays,
		}

		result, err := sdkClient.APIKeys.Create(ctx, params)
		if err != nil {
			log.Error(err, "failed to create API key")
			hookbasev1alpha1.SetCondition(&apiKey.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), apiKey.Generation)
			_ = r.Status().Update(ctx, &apiKey)
			metrics.ReconcileTotal.WithLabelValues("HookbaseAPIKey", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		// Store the raw key in the referenced Secret — this is the only time it's available
		if err := r.storeAPIKeySecret(ctx, &apiKey, result.Key); err != nil {
			log.Error(err, "failed to store API key secret")
			// Delete the created key since we can't store it
			_ = sdkClient.APIKeys.Delete(ctx, result.ID)
			hookbasev1alpha1.SetCondition(&apiKey.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "SecretCreateFailed", err.Error(), apiKey.Generation)
			_ = r.Status().Update(ctx, &apiKey)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		apiKey.Status.APIKeyID = result.ID
		apiKey.Status.KeyPrefix = result.KeyPrefix
		if result.ExpiresAt != nil {
			apiKey.Status.ExpiresAt = *result.ExpiresAt
		}
		log.Info("created API key", "apiKeyId", result.ID, "keyPrefix", result.KeyPrefix)
	} else {
		// For scope changes, we need to delete + recreate since the API doesn't support scope updates
		// Check if scopes changed by comparing with current
		existing, err := sdkClient.APIKeys.Get(ctx, apiKey.Status.APIKeyID)
		if err != nil {
			var notFound *hookbasesdk.NotFoundError
			if errors.As(err, &notFound) {
				apiKey.Status.APIKeyID = ""
				_ = r.Status().Update(ctx, &apiKey)
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		// Update name if changed
		if existing.Name != apiKey.Spec.Name {
			_, err := sdkClient.APIKeys.Update(ctx, apiKey.Status.APIKeyID, &hookbasesdk.UpdateAPIKeyParams{
				Name: &apiKey.Spec.Name,
			})
			if err != nil {
				log.Error(err, "failed to update API key name")
			}
		}
	}

	now := metav1.Now()
	apiKey.Status.LastSyncTime = &now
	apiKey.Status.ObservedGeneration = apiKey.Generation
	hookbasev1alpha1.SetCondition(&apiKey.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "API key is synced", apiKey.Generation)
	_ = r.Status().Update(ctx, &apiKey)
	metrics.ReconcileTotal.WithLabelValues("HookbaseAPIKey", "success").Inc()
	return ctrl.Result{RequeueAfter: r.RequeueAfter}, nil
}

func (r *HookbaseAPIKeyReconciler) storeAPIKeySecret(ctx context.Context, apiKey *hookbasev1alpha1.HookbaseAPIKey, rawKey string) error {
	secretKey := apiKey.Spec.SecretRef.Key
	if secretKey == "" {
		secretKey = "apiKey"
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      apiKey.Spec.SecretRef.Name,
			Namespace: apiKey.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "hookbase-operator",
				"hookbase.io/api-key":          apiKey.Name,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			secretKey: []byte(rawKey),
		},
	}

	if err := controllerutil.SetControllerReference(apiKey, secret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Namespace: apiKey.Namespace, Name: apiKey.Spec.SecretRef.Name}, existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, secret)
	}
	if err != nil {
		return err
	}
	existing.Data = secret.Data
	return r.Update(ctx, existing)
}

func (r *HookbaseAPIKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.HookbaseAPIKey{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
