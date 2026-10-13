package controller

import (
	"context"
	"encoding/json"
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

type WebhookSchemaReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *hookbase.ClientManager
	RequeueAfter  time.Duration
}

func (r *WebhookSchemaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("WebhookSchema").Observe(time.Since(start).Seconds())
	}()

	var schema hookbasev1alpha1.WebhookSchema
	if err := r.Get(ctx, req.NamespacedName, &schema); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	sdkClient, err := r.ClientManager.GetClient(ctx, schema.Namespace, schema.Spec.APIKeyRef)
	if err != nil {
		hookbasev1alpha1.SetCondition(&schema.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "APIKeyError", err.Error(), schema.Generation)
		_ = r.Status().Update(ctx, &schema)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if !schema.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&schema, hookbasev1alpha1.Finalizer) {
			if schema.Status.SchemaID != "" {
				if err := sdkClient.Schemas.Delete(ctx, schema.Status.SchemaID); err != nil {
					var notFound *hookbasesdk.NotFoundError
					if !errors.As(err, &notFound) {
						return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
					}
				}
			}
			controllerutil.RemoveFinalizer(&schema, hookbasev1alpha1.Finalizer)
			return ctrl.Result{}, r.Update(ctx, &schema)
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&schema, hookbasev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&schema, hookbasev1alpha1.Finalizer)
		if err := r.Update(ctx, &schema); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Resolve JSON schema
	jsonSchemaMap, err := r.resolveJSONSchema(ctx, &schema)
	if err != nil {
		hookbasev1alpha1.SetCondition(&schema.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "SchemaResolveError", err.Error(), schema.Generation)
		_ = r.Status().Update(ctx, &schema)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if schema.Status.SchemaID == "" {
		params := &hookbasesdk.CreateSchemaParams{
			Name:       schema.Spec.Name,
			JSONSchema: jsonSchemaMap,
		}
		if schema.Spec.Slug != "" {
			params.Slug = &schema.Spec.Slug
		}
		if schema.Spec.Description != "" {
			params.Description = &schema.Spec.Description
		}

		apiSchema, err := sdkClient.Schemas.Create(ctx, params)
		if err != nil {
			log.Error(err, "failed to create schema")
			hookbasev1alpha1.SetCondition(&schema.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), schema.Generation)
			_ = r.Status().Update(ctx, &schema)
			metrics.ReconcileTotal.WithLabelValues("WebhookSchema", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		schema.Status.SchemaID = apiSchema.ID
		log.Info("created schema", "schemaId", apiSchema.ID)
	} else {
		params := &hookbasesdk.UpdateSchemaParams{
			Name:       &schema.Spec.Name,
			JSONSchema: jsonSchemaMap,
		}
		if schema.Spec.Description != "" {
			params.Description = &schema.Spec.Description
		}

		if err := sdkClient.Schemas.Update(ctx, schema.Status.SchemaID, params); err != nil {
			var notFound *hookbasesdk.NotFoundError
			if errors.As(err, &notFound) {
				schema.Status.SchemaID = ""
				_ = r.Status().Update(ctx, &schema)
				return ctrl.Result{Requeue: true}, nil
			}
			hookbasev1alpha1.SetCondition(&schema.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "UpdateFailed", err.Error(), schema.Generation)
			_ = r.Status().Update(ctx, &schema)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	now := metav1.Now()
	schema.Status.LastSyncTime = &now
	schema.Status.ObservedGeneration = schema.Generation
	hookbasev1alpha1.SetCondition(&schema.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "Schema is synced", schema.Generation)
	_ = r.Status().Update(ctx, &schema)
	metrics.ReconcileTotal.WithLabelValues("WebhookSchema", "success").Inc()
	return ctrl.Result{RequeueAfter: r.RequeueAfter}, nil
}

func (r *WebhookSchemaReconciler) resolveJSONSchema(ctx context.Context, schema *hookbasev1alpha1.WebhookSchema) (map[string]interface{}, error) {
	if schema.Spec.JSONSchema != nil {
		var result map[string]interface{}
		if err := json.Unmarshal(schema.Spec.JSONSchema.Raw, &result); err != nil {
			return nil, fmt.Errorf("failed to parse inline JSON schema: %w", err)
		}
		return result, nil
	}

	if schema.Spec.JSONSchemaFrom != nil {
		var cm corev1.ConfigMap
		if err := r.Get(ctx, client.ObjectKey{Namespace: schema.Namespace, Name: schema.Spec.JSONSchemaFrom.Name}, &cm); err != nil {
			return nil, fmt.Errorf("ConfigMap %q not found: %w", schema.Spec.JSONSchemaFrom.Name, err)
		}
		data, ok := cm.Data[schema.Spec.JSONSchemaFrom.Key]
		if !ok {
			return nil, fmt.Errorf("ConfigMap %q does not contain key %q", schema.Spec.JSONSchemaFrom.Name, schema.Spec.JSONSchemaFrom.Key)
		}
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			return nil, fmt.Errorf("failed to parse JSON schema from ConfigMap: %w", err)
		}
		return result, nil
	}

	return nil, fmt.Errorf("either jsonSchema or jsonSchemaFrom must be set")
}

func (r *WebhookSchemaReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.WebhookSchema{}).
		Complete(r)
}
