package controller

import (
	"context"
	"errors"
	"time"

	hookbasesdk "github.com/HookbaseApp/hookbase-go"
	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/hookbase"
	"github.com/hookbase/hookbase-operator/internal/metrics"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type WebhookFilterReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *hookbase.ClientManager
	RequeueAfter  time.Duration
}

func (r *WebhookFilterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("WebhookFilter").Observe(time.Since(start).Seconds())
	}()

	var filter hookbasev1alpha1.WebhookFilter
	if err := r.Get(ctx, req.NamespacedName, &filter); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	sdkClient, err := r.ClientManager.GetClient(ctx, filter.Namespace, filter.Spec.APIKeyRef)
	if err != nil {
		hookbasev1alpha1.SetCondition(&filter.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "APIKeyError", err.Error(), filter.Generation)
		_ = r.Status().Update(ctx, &filter)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if !filter.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&filter, hookbasev1alpha1.Finalizer) {
			if filter.Status.FilterID != "" {
				if err := sdkClient.Filters.Delete(ctx, filter.Status.FilterID); err != nil {
					var notFound *hookbasesdk.NotFoundError
					if !errors.As(err, &notFound) {
						return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
					}
				}
			}
			controllerutil.RemoveFinalizer(&filter, hookbasev1alpha1.Finalizer)
			return ctrl.Result{}, r.Update(ctx, &filter)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&filter, hookbasev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&filter, hookbasev1alpha1.Finalizer)
		if err := r.Update(ctx, &filter); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Convert conditions
	var conditions []hookbasesdk.FilterCondition
	for _, c := range filter.Spec.Conditions {
		conditions = append(conditions, hookbasesdk.FilterCondition{
			Field:    c.Field,
			Operator: c.Operator,
			Value:    c.Value,
		})
	}

	if filter.Status.FilterID == "" {
		params := &hookbasesdk.CreateFilterParams{
			Name:       filter.Spec.Name,
			Conditions: conditions,
		}
		if filter.Spec.Slug != "" {
			params.Slug = &filter.Spec.Slug
		}
		if filter.Spec.Description != "" {
			params.Description = &filter.Spec.Description
		}
		if filter.Spec.Logic != "" {
			params.Logic = &filter.Spec.Logic
		}

		apiFilter, err := sdkClient.Filters.Create(ctx, params)
		if err != nil {
			log.Error(err, "failed to create filter")
			hookbasev1alpha1.SetCondition(&filter.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), filter.Generation)
			_ = r.Status().Update(ctx, &filter)
			metrics.ReconcileTotal.WithLabelValues("WebhookFilter", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		filter.Status.FilterID = apiFilter.ID
		log.Info("created filter", "filterId", apiFilter.ID)
	} else {
		params := &hookbasesdk.UpdateFilterParams{
			Name:       &filter.Spec.Name,
			Conditions: conditions,
		}
		if filter.Spec.Description != "" {
			params.Description = &filter.Spec.Description
		}
		if filter.Spec.Logic != "" {
			params.Logic = &filter.Spec.Logic
		}

		if err := sdkClient.Filters.Update(ctx, filter.Status.FilterID, params); err != nil {
			var notFound *hookbasesdk.NotFoundError
			if errors.As(err, &notFound) {
				filter.Status.FilterID = ""
				_ = r.Status().Update(ctx, &filter)
				return ctrl.Result{Requeue: true}, nil
			}
			hookbasev1alpha1.SetCondition(&filter.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "UpdateFailed", err.Error(), filter.Generation)
			_ = r.Status().Update(ctx, &filter)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	now := metav1.Now()
	filter.Status.LastSyncTime = &now
	filter.Status.ObservedGeneration = filter.Generation
	hookbasev1alpha1.SetCondition(&filter.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "Filter is synced", filter.Generation)
	_ = r.Status().Update(ctx, &filter)
	metrics.ReconcileTotal.WithLabelValues("WebhookFilter", "success").Inc()
	return ctrl.Result{RequeueAfter: r.RequeueAfter}, nil
}

func (r *WebhookFilterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.WebhookFilter{}).
		Complete(r)
}
