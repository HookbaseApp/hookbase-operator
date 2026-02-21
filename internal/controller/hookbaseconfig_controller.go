package controller

import (
	"context"
	"time"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/hookbase"
	"github.com/hookbase/hookbase-operator/internal/metrics"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// HookbaseConfigReconciler reconciles the cluster-scoped HookbaseConfig.
type HookbaseConfigReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	ClientManager      *hookbase.ClientManager
	OperatorNamespace  string
}

func (r *HookbaseConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("HookbaseConfig").Observe(time.Since(start).Seconds())
	}()

	var config hookbasev1alpha1.HookbaseConfig
	if err := r.Get(ctx, req.NamespacedName, &config); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Resolve bootstrap API key from Secret
	ref := config.Spec.DefaultBootstrapAPIKeySecretRef
	secretKey := ref.Key
	if secretKey == "" {
		secretKey = "apiKey"
	}

	var secret corev1.Secret
	// For cluster-scoped config, the Secret must be in the operator's namespace
	ns := r.OperatorNamespace
	if ns == "" {
		ns = "hookbase-system"
	}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, &secret); err != nil {
		log.Error(err, "failed to get bootstrap API key secret", "secret", ref.Name)
		hookbasev1alpha1.SetCondition(&config.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "BootstrapKeyNotFound", err.Error(), config.Generation)
		_ = r.Status().Update(ctx, &config)
		metrics.ReconcileTotal.WithLabelValues("HookbaseConfig", "error").Inc()
		return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
	}

	apiKey, ok := secret.Data[secretKey]
	if !ok {
		log.Error(nil, "bootstrap secret missing key", "key", secretKey)
		hookbasev1alpha1.SetCondition(&config.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "BootstrapKeyMissing", "Secret does not contain the expected key", config.Generation)
		_ = r.Status().Update(ctx, &config)
		return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
	}

	// Update the client manager with the resolved bootstrap key
	r.ClientManager.SetBootstrapKey(string(apiKey))
	log.Info("updated bootstrap API key from HookbaseConfig")

	config.Status.ObservedGeneration = config.Generation
	hookbasev1alpha1.SetCondition(&config.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Configured", "HookbaseConfig is valid and bootstrap key is set", config.Generation)
	_ = r.Status().Update(ctx, &config)
	metrics.ReconcileTotal.WithLabelValues("HookbaseConfig", "success").Inc()
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *HookbaseConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.HookbaseConfig{}).
		Complete(r)
}
