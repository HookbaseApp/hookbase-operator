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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type HookbaseCronJobReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *hookbase.ClientManager
	RequeueAfter  time.Duration
}

func (r *HookbaseCronJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("HookbaseCronJob").Observe(time.Since(start).Seconds())
	}()

	var cron hookbasev1alpha1.HookbaseCronJob
	if err := r.Get(ctx, req.NamespacedName, &cron); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	sdkClient, err := r.ClientManager.GetClient(ctx, cron.Namespace, cron.Spec.APIKeyRef)
	if err != nil {
		hookbasev1alpha1.SetCondition(&cron.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "APIKeyError", err.Error(), cron.Generation)
		_ = r.Status().Update(ctx, &cron)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if !cron.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&cron, hookbasev1alpha1.Finalizer) {
			if cron.Status.CronJobID != "" {
				if err := sdkClient.Cron.Delete(ctx, cron.Status.CronJobID); err != nil {
					var notFound *hookbasesdk.NotFoundError
					if !errors.As(err, &notFound) {
						return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
					}
				}
			}
			controllerutil.RemoveFinalizer(&cron, hookbasev1alpha1.Finalizer)
			return ctrl.Result{}, r.Update(ctx, &cron)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&cron, hookbasev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&cron, hookbasev1alpha1.Finalizer)
		if err := r.Update(ctx, &cron); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Resolve payload from ConfigMap if needed
	payload := cron.Spec.Payload
	if payload == "" && cron.Spec.PayloadFrom != nil {
		var cm corev1.ConfigMap
		if err := r.Get(ctx, client.ObjectKey{Namespace: cron.Namespace, Name: cron.Spec.PayloadFrom.Name}, &cm); err != nil {
			hookbasev1alpha1.SetCondition(&cron.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "ConfigMapError", err.Error(), cron.Generation)
			_ = r.Status().Update(ctx, &cron)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		val, ok := cm.Data[cron.Spec.PayloadFrom.Key]
		if !ok {
			hookbasev1alpha1.SetCondition(&cron.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "ConfigMapError",
				fmt.Sprintf("key %q not found in ConfigMap %q", cron.Spec.PayloadFrom.Key, cron.Spec.PayloadFrom.Name),
				cron.Generation)
			_ = r.Status().Update(ctx, &cron)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		payload = val
	}

	method := cron.Spec.Method
	if method == "" {
		method = "POST"
	}

	if cron.Status.CronJobID == "" {
		params := &hookbasesdk.CreateCronParams{
			Name:        cron.Spec.Name,
			Schedule:    cron.Spec.CronExpression,
			URL:         cron.Spec.URL,
			Method:      &method,
			Headers:     cron.Spec.Headers,
			IsActive:    cron.Spec.IsActive,
			UseStaticIP: cron.Spec.UseStaticIP,
		}
		if cron.Spec.Description != "" {
			params.Description = &cron.Spec.Description
		}
		if payload != "" {
			params.Payload = &payload
		}
		if cron.Spec.Timezone != "" {
			params.Timezone = &cron.Spec.Timezone
		}

		apiCron, err := sdkClient.Cron.Create(ctx, params)
		if err != nil {
			log.Error(err, "failed to create cron job")
			hookbasev1alpha1.SetCondition(&cron.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), cron.Generation)
			_ = r.Status().Update(ctx, &cron)
			metrics.ReconcileTotal.WithLabelValues("HookbaseCronJob", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		cron.Status.CronJobID = apiCron.ID
		if apiCron.NextRunAt != nil {
			cron.Status.NextRunAt = *apiCron.NextRunAt
		}
		log.Info("created cron job", "cronJobId", apiCron.ID)
	} else {
		params := &hookbasesdk.UpdateCronParams{
			Name:        &cron.Spec.Name,
			Schedule:    &cron.Spec.CronExpression,
			URL:         &cron.Spec.URL,
			Method:      &method,
			Headers:     cron.Spec.Headers,
			IsActive:    cron.Spec.IsActive,
			UseStaticIP: cron.Spec.UseStaticIP,
		}
		if cron.Spec.Description != "" {
			params.Description = &cron.Spec.Description
		}
		if payload != "" {
			params.Payload = &payload
		}
		if cron.Spec.Timezone != "" {
			params.Timezone = &cron.Spec.Timezone
		}

		apiCron, err := sdkClient.Cron.Update(ctx, cron.Status.CronJobID, params)
		if err != nil {
			var notFound *hookbasesdk.NotFoundError
			if errors.As(err, &notFound) {
				cron.Status.CronJobID = ""
				_ = r.Status().Update(ctx, &cron)
				return ctrl.Result{Requeue: true}, nil
			}
			hookbasev1alpha1.SetCondition(&cron.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "UpdateFailed", err.Error(), cron.Generation)
			_ = r.Status().Update(ctx, &cron)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		if apiCron.LastRunAt != nil {
			cron.Status.LastRunAt = *apiCron.LastRunAt
		}
		if apiCron.NextRunAt != nil {
			cron.Status.NextRunAt = *apiCron.NextRunAt
		}
	}

	now := metav1.Now()
	cron.Status.LastSyncTime = &now
	cron.Status.ObservedGeneration = cron.Generation
	hookbasev1alpha1.SetCondition(&cron.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "CronJob is synced", cron.Generation)
	_ = r.Status().Update(ctx, &cron)
	metrics.ReconcileTotal.WithLabelValues("HookbaseCronJob", "success").Inc()
	return ctrl.Result{RequeueAfter: r.RequeueAfter}, nil
}

func (r *HookbaseCronJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.HookbaseCronJob{}).
		Complete(r)
}
