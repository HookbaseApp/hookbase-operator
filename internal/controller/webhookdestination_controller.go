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

type WebhookDestinationReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *hookbase.ClientManager
	RequeueAfter  time.Duration
}

func (r *WebhookDestinationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("WebhookDestination").Observe(time.Since(start).Seconds())
	}()

	var dest hookbasev1alpha1.WebhookDestination
	if err := r.Get(ctx, req.NamespacedName, &dest); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	sdkClient, err := r.ClientManager.GetClient(ctx, dest.Namespace, dest.Spec.APIKeyRef)
	if err != nil {
		hookbasev1alpha1.SetCondition(&dest.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "APIKeyError", err.Error(), dest.Generation)
		_ = r.Status().Update(ctx, &dest)
		metrics.ReconcileTotal.WithLabelValues("WebhookDestination", "error").Inc()
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Handle deletion
	if !dest.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&dest, hookbasev1alpha1.Finalizer) {
			if dest.Status.DestinationID != "" {
				if err := sdkClient.Destinations.Delete(ctx, dest.Status.DestinationID); err != nil {
					var notFound *hookbasesdk.NotFoundError
					if !errors.As(err, &notFound) {
						log.Error(err, "failed to delete destination from Hookbase API")
						return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
					}
				}
			}
			controllerutil.RemoveFinalizer(&dest, hookbasev1alpha1.Finalizer)
			if err := r.Update(ctx, &dest); err != nil {
				return ctrl.Result{}, err
			}
		}
		metrics.ReconcileTotal.WithLabelValues("WebhookDestination", "deleted").Inc()
		return ctrl.Result{}, nil
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(&dest, hookbasev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&dest, hookbasev1alpha1.Finalizer)
		if err := r.Update(ctx, &dest); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Resolve auth config from Secret if needed
	authConfig := map[string]interface{}{}
	if dest.Spec.AuthSecretRef != nil && dest.Spec.AuthType != "" && dest.Spec.AuthType != "none" {
		authConfig, err = r.resolveAuthConfig(ctx, dest.Namespace, dest.Spec.AuthSecretRef, dest.Spec.AuthType)
		if err != nil {
			hookbasev1alpha1.SetCondition(&dest.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "AuthSecretError", err.Error(), dest.Generation)
			_ = r.Status().Update(ctx, &dest)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	method := hookbasesdk.HTTPMethod(dest.Spec.Method)
	if method == "" {
		method = hookbasesdk.HTTPPost
	}
	authType := hookbasesdk.AuthType(dest.Spec.AuthType)

	// Resolve warehouse config from Secret if needed
	var warehouseConfig map[string]interface{}
	destType := dest.Spec.Type
	if destType == "" {
		destType = "http"
	}
	isWarehouse := destType != "http"
	if isWarehouse && dest.Spec.ConfigSecretRef != nil {
		warehouseConfig, err = r.resolveWarehouseConfig(ctx, dest.Namespace, dest.Spec.ConfigSecretRef)
		if err != nil {
			hookbasev1alpha1.SetCondition(&dest.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "ConfigSecretError", err.Error(), dest.Generation)
			_ = r.Status().Update(ctx, &dest)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// Convert field mappings
	var sdkFieldMappings []hookbasesdk.FieldMapping
	if len(dest.Spec.FieldMapping) > 0 {
		sdkFieldMappings = toSDKFieldMappings(dest.Spec.FieldMapping)
	}

	if dest.Status.DestinationID == "" {
		// CREATE
		params := &hookbasesdk.CreateDestinationParams{
			Name:               dest.Spec.Name,
			URL:                dest.Spec.URL,
			Method:             &method,
			Headers:            dest.Spec.Headers,
			AuthType:           &authType,
			AuthConfig:         authConfig,
			Timeout:            dest.Spec.TimeoutMs,
			RetryCount:         dest.Spec.RetryCount,
			RateLimit:          dest.Spec.RateLimitPerMinute,
			UseStaticIP:        dest.Spec.UseStaticIP,
			BatchSize:          dest.Spec.BatchSize,
			BatchWindowSeconds: dest.Spec.BatchWindowSeconds,
		}
		if dest.Spec.Slug != "" {
			params.Slug = &dest.Spec.Slug
		}
		if dest.Spec.Description != "" {
			params.Description = &dest.Spec.Description
		}
		if isWarehouse {
			dt := hookbasesdk.DestinationType(destType)
			params.Type = &dt
			params.Config = warehouseConfig
		}
		if len(sdkFieldMappings) > 0 {
			params.FieldMapping = sdkFieldMappings
		}

		apiDest, err := sdkClient.Destinations.Create(ctx, params)
		if err != nil {
			log.Error(err, "failed to create destination")
			hookbasev1alpha1.SetCondition(&dest.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), dest.Generation)
			_ = r.Status().Update(ctx, &dest)
			metrics.ReconcileTotal.WithLabelValues("WebhookDestination", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		dest.Status.DestinationID = apiDest.ID
		log.Info("created destination", "destinationId", apiDest.ID)
	} else {
		// UPDATE
		params := &hookbasesdk.UpdateDestinationParams{
			Name:               &dest.Spec.Name,
			URL:                &dest.Spec.URL,
			Method:             &method,
			Headers:            dest.Spec.Headers,
			AuthType:           &authType,
			AuthConfig:         authConfig,
			Timeout:            dest.Spec.TimeoutMs,
			RetryCount:         dest.Spec.RetryCount,
			RateLimit:          dest.Spec.RateLimitPerMinute,
			UseStaticIP:        dest.Spec.UseStaticIP,
			BatchSize:          dest.Spec.BatchSize,
			BatchWindowSeconds: dest.Spec.BatchWindowSeconds,
		}
		if dest.Spec.Description != "" {
			params.Description = &dest.Spec.Description
		}
		if isWarehouse {
			params.Config = warehouseConfig
		}
		if len(sdkFieldMappings) > 0 {
			params.FieldMapping = sdkFieldMappings
		}

		if err := sdkClient.Destinations.Update(ctx, dest.Status.DestinationID, params); err != nil {
			var notFound *hookbasesdk.NotFoundError
			if errors.As(err, &notFound) {
				dest.Status.DestinationID = ""
				_ = r.Status().Update(ctx, &dest)
				return ctrl.Result{Requeue: true}, nil
			}
			hookbasev1alpha1.SetCondition(&dest.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "UpdateFailed", err.Error(), dest.Generation)
			_ = r.Status().Update(ctx, &dest)
			metrics.ReconcileTotal.WithLabelValues("WebhookDestination", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	now := metav1.Now()
	dest.Status.LastSyncTime = &now
	dest.Status.ObservedGeneration = dest.Generation
	hookbasev1alpha1.SetCondition(&dest.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "Destination is synced", dest.Generation)
	hookbasev1alpha1.SetCondition(&dest.Status.Conditions, hookbasev1alpha1.ConditionSynced,
		metav1.ConditionTrue, "Synced", fmt.Sprintf("Last synced at %s", now.Format(time.RFC3339)), dest.Generation)

	_ = r.Status().Update(ctx, &dest)
	metrics.ReconcileTotal.WithLabelValues("WebhookDestination", "success").Inc()
	return ctrl.Result{RequeueAfter: r.RequeueAfter}, nil
}

func (r *WebhookDestinationReconciler) resolveAuthConfig(ctx context.Context, namespace string, ref *hookbasev1alpha1.SecretKeyRef, authType string) (map[string]interface{}, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get auth secret %s: %w", ref.Name, err)
	}

	config := map[string]interface{}{}
	switch authType {
	case "bearer":
		if token, ok := secret.Data["token"]; ok {
			config["token"] = string(token)
		}
	case "basic":
		if user, ok := secret.Data["username"]; ok {
			config["username"] = string(user)
		}
		if pass, ok := secret.Data["password"]; ok {
			config["password"] = string(pass)
		}
	case "api_key":
		if name, ok := secret.Data["headerName"]; ok {
			config["headerName"] = string(name)
		}
		if val, ok := secret.Data["headerValue"]; ok {
			config["headerValue"] = string(val)
		}
	case "custom_header":
		if name, ok := secret.Data["headerName"]; ok {
			config["headerName"] = string(name)
		}
		if val, ok := secret.Data["headerValue"]; ok {
			config["headerValue"] = string(val)
		}
	}
	return config, nil
}

func (r *WebhookDestinationReconciler) resolveWarehouseConfig(ctx context.Context, namespace string, ref *hookbasev1alpha1.SecretKeyRef) (map[string]interface{}, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get config secret %s: %w", ref.Name, err)
	}

	config := make(map[string]interface{}, len(secret.Data))
	for k, v := range secret.Data {
		config[k] = string(v)
	}
	return config, nil
}

func toSDKFieldMappings(specs []hookbasev1alpha1.FieldMappingSpec) []hookbasesdk.FieldMapping {
	mappings := make([]hookbasesdk.FieldMapping, len(specs))
	for i, s := range specs {
		mappings[i] = hookbasesdk.FieldMapping{
			Source:  s.Source,
			Target:  s.Target,
			Type:    s.Type,
			Default: s.Default,
		}
	}
	return mappings
}

func (r *WebhookDestinationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.WebhookDestination{}).
		Complete(r)
}
