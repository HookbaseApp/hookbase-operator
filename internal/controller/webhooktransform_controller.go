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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type WebhookTransformReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *hookbase.ClientManager
	RequeueAfter  time.Duration
}

func (r *WebhookTransformReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("WebhookTransform").Observe(time.Since(start).Seconds())
	}()

	var transform hookbasev1alpha1.WebhookTransform
	if err := r.Get(ctx, req.NamespacedName, &transform); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	sdkClient, err := r.ClientManager.GetClient(ctx, transform.Namespace, transform.Spec.APIKeyRef)
	if err != nil {
		hookbasev1alpha1.SetCondition(&transform.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "APIKeyError", err.Error(), transform.Generation)
		_ = r.Status().Update(ctx, &transform)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Handle deletion
	if !transform.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&transform, hookbasev1alpha1.Finalizer) {
			if transform.Status.TransformID != "" {
				if err := sdkClient.Transforms.Delete(ctx, transform.Status.TransformID); err != nil {
					var notFound *hookbasesdk.NotFoundError
					if !errors.As(err, &notFound) {
						return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
					}
				}
			}
			controllerutil.RemoveFinalizer(&transform, hookbasev1alpha1.Finalizer)
			return ctrl.Result{}, r.Update(ctx, &transform)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&transform, hookbasev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&transform, hookbasev1alpha1.Finalizer)
		if err := r.Update(ctx, &transform); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Resolve code from ConfigMap if needed
	code := transform.Spec.Code
	if code == "" && transform.Spec.CodeFrom != nil {
		resolved, err := r.resolveConfigMapKey(ctx, transform.Namespace, transform.Spec.CodeFrom)
		if err != nil {
			hookbasev1alpha1.SetCondition(&transform.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "ConfigMapError", err.Error(), transform.Generation)
			_ = r.Status().Update(ctx, &transform)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		code = resolved
	}

	transformType := hookbasesdk.TransformType(transform.Spec.TransformType)
	inputFmt := hookbasesdk.ContentFormat(transform.Spec.InputFormat)
	outputFmt := hookbasesdk.ContentFormat(transform.Spec.OutputFormat)

	if transform.Status.TransformID == "" {
		params := &hookbasesdk.CreateTransformParams{
			Name:          transform.Spec.Name,
			TransformType: transformType,
			Code:          code,
			InputFormat:   &inputFmt,
			OutputFormat:  &outputFmt,
		}
		if transform.Spec.Slug != "" {
			params.Slug = &transform.Spec.Slug
		}
		if transform.Spec.Description != "" {
			params.Description = &transform.Spec.Description
		}

		apiTransform, err := sdkClient.Transforms.Create(ctx, params)
		if err != nil {
			log.Error(err, "failed to create transform")
			hookbasev1alpha1.SetCondition(&transform.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), transform.Generation)
			_ = r.Status().Update(ctx, &transform)
			metrics.ReconcileTotal.WithLabelValues("WebhookTransform", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		transform.Status.TransformID = apiTransform.ID
		log.Info("created transform", "transformId", apiTransform.ID)
	} else {
		params := &hookbasesdk.UpdateTransformParams{
			Name:          &transform.Spec.Name,
			TransformType: &transformType,
			Code:          &code,
			InputFormat:   &inputFmt,
			OutputFormat:  &outputFmt,
		}
		if transform.Spec.Description != "" {
			params.Description = &transform.Spec.Description
		}

		if err := sdkClient.Transforms.Update(ctx, transform.Status.TransformID, params); err != nil {
			var notFound *hookbasesdk.NotFoundError
			if errors.As(err, &notFound) {
				transform.Status.TransformID = ""
				_ = r.Status().Update(ctx, &transform)
				return ctrl.Result{Requeue: true}, nil
			}
			hookbasev1alpha1.SetCondition(&transform.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "UpdateFailed", err.Error(), transform.Generation)
			_ = r.Status().Update(ctx, &transform)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	now := metav1.Now()
	transform.Status.LastSyncTime = &now
	transform.Status.ObservedGeneration = transform.Generation
	hookbasev1alpha1.SetCondition(&transform.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "Transform is synced", transform.Generation)
	_ = r.Status().Update(ctx, &transform)
	metrics.ReconcileTotal.WithLabelValues("WebhookTransform", "success").Inc()
	return ctrl.Result{RequeueAfter: r.RequeueAfter}, nil
}

func (r *WebhookTransformReconciler) resolveConfigMapKey(ctx context.Context, namespace string, ref *hookbasev1alpha1.ConfigMapKeyRef) (string, error) {
	var cm corev1.ConfigMap
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &cm); err != nil {
		return "", fmt.Errorf("ConfigMap %q not found: %w", ref.Name, err)
	}
	val, ok := cm.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("ConfigMap %q does not contain key %q", ref.Name, ref.Key)
	}
	return val, nil
}

func (r *WebhookTransformReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.WebhookTransform{}).
		Complete(r)
}
