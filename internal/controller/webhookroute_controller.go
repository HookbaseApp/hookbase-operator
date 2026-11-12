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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type WebhookRouteReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	ClientManager *hookbase.ClientManager
	RequeueAfter  time.Duration
}

func (r *WebhookRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	start := time.Now()
	defer func() {
		metrics.ReconcileDuration.WithLabelValues("WebhookRoute").Observe(time.Since(start).Seconds())
	}()

	var route hookbasev1alpha1.WebhookRoute
	if err := r.Get(ctx, req.NamespacedName, &route); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	sdkClient, err := r.ClientManager.GetClient(ctx, route.Namespace, route.Spec.APIKeyRef)
	if err != nil {
		hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "APIKeyError", err.Error(), route.Generation)
		_ = r.Status().Update(ctx, &route)
		metrics.ReconcileTotal.WithLabelValues("WebhookRoute", "error").Inc()
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Handle deletion
	if !route.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&route, hookbasev1alpha1.Finalizer) {
			if route.Status.RouteID != "" {
				if err := sdkClient.Routes.Delete(ctx, route.Status.RouteID); err != nil {
					var notFound *hookbasesdk.NotFoundError
					if !errors.As(err, &notFound) {
						log.Error(err, "failed to delete route")
						return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
					}
				}
			}
			controllerutil.RemoveFinalizer(&route, hookbasev1alpha1.Finalizer)
			if err := r.Update(ctx, &route); err != nil {
				return ctrl.Result{}, err
			}
		}
		metrics.ReconcileTotal.WithLabelValues("WebhookRoute", "deleted").Inc()
		return ctrl.Result{}, nil
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(&route, hookbasev1alpha1.Finalizer) {
		controllerutil.AddFinalizer(&route, hookbasev1alpha1.Finalizer)
		if err := r.Update(ctx, &route); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Resolve all cross-resource references to API IDs
	sourceID, err := r.resolveSourceRef(ctx, route.Namespace, route.Spec.SourceRef)
	if err != nil {
		log.Info("source ref not ready, requeuing", "sourceRef", route.Spec.SourceRef, "error", err.Error())
		hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "SourceNotReady", err.Error(), route.Generation)
		_ = r.Status().Update(ctx, &route)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	destID, err := r.resolveDestinationRef(ctx, route.Namespace, route.Spec.DestinationRef)
	if err != nil {
		log.Info("destination ref not ready, requeuing", "destRef", route.Spec.DestinationRef, "error", err.Error())
		hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionReady,
			metav1.ConditionFalse, "DestinationNotReady", err.Error(), route.Generation)
		_ = r.Status().Update(ctx, &route)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Resolve optional refs
	var filterID, transformID, schemaID *string
	if route.Spec.FilterRef != "" {
		id, err := r.resolveFilterRef(ctx, route.Namespace, route.Spec.FilterRef)
		if err != nil {
			hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "FilterNotReady", err.Error(), route.Generation)
			_ = r.Status().Update(ctx, &route)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		filterID = &id
	}
	if route.Spec.TransformRef != "" {
		id, err := r.resolveTransformRef(ctx, route.Namespace, route.Spec.TransformRef)
		if err != nil {
			hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "TransformNotReady", err.Error(), route.Generation)
			_ = r.Status().Update(ctx, &route)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		transformID = &id
	}
	if route.Spec.SchemaRef != "" {
		id, err := r.resolveSchemaRef(ctx, route.Namespace, route.Spec.SchemaRef)
		if err != nil {
			hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "SchemaNotReady", err.Error(), route.Generation)
			_ = r.Status().Update(ctx, &route)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		schemaID = &id
	}

	// Convert filter conditions from CRD to SDK type
	var filterConditions []hookbasesdk.FilterCondition
	for _, c := range route.Spec.FilterConditions {
		filterConditions = append(filterConditions, hookbasesdk.FilterCondition{
			Field:    c.Field,
			Operator: c.Operator,
			Value:    c.Value,
		})
	}

	// Resolve failover destination IDs
	var failoverIDs []string
	for _, ref := range route.Spec.FailoverDestinationRefs {
		id, err := r.resolveDestinationRef(ctx, route.Namespace, ref)
		if err != nil {
			log.Info("failover destination ref not ready", "ref", ref)
			continue
		}
		failoverIDs = append(failoverIDs, id)
	}

	if route.Status.RouteID == "" {
		// CREATE
		params := &hookbasesdk.CreateRouteParams{
			Name:                   route.Spec.Name,
			SourceID:               sourceID,
			DestinationID:          destID,
			FilterID:               filterID,
			FilterConditions:       filterConditions,
			TransformID:            transformID,
			SchemaID:               schemaID,
			Priority:               route.Spec.Priority,
			IsActive:               route.Spec.IsActive,
			FailoverDestinationIDs: failoverIDs,
			HoldOnCircuitOpen:      route.Spec.HoldOnCircuitOpen,
		}
		if route.Spec.FilterLogic != "" {
			params.FilterLogic = &route.Spec.FilterLogic
		}
		if route.Spec.Notifications != nil {
			params.NotifyOnFailure = route.Spec.Notifications.OnFailure
			params.NotifyOnSuccess = route.Spec.Notifications.OnSuccess
			params.NotifyOnRecovery = route.Spec.Notifications.OnRecovery
			if route.Spec.Notifications.Emails != "" {
				params.NotifyEmails = &route.Spec.Notifications.Emails
			}
		}

		apiRoute, err := sdkClient.Routes.Create(ctx, params)
		if err != nil {
			log.Error(err, "failed to create route")
			hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), route.Generation)
			_ = r.Status().Update(ctx, &route)
			metrics.ReconcileTotal.WithLabelValues("WebhookRoute", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		route.Status.RouteID = apiRoute.ID
		log.Info("created route", "routeId", apiRoute.ID)
	} else {
		// UPDATE
		params := &hookbasesdk.UpdateRouteParams{
			Name:                   &route.Spec.Name,
			SourceID:               &sourceID,
			DestinationID:          &destID,
			FilterID:               filterID,
			FilterConditions:       filterConditions,
			TransformID:            transformID,
			SchemaID:               schemaID,
			Priority:               route.Spec.Priority,
			IsActive:               route.Spec.IsActive,
			FailoverDestinationIDs: failoverIDs,
			HoldOnCircuitOpen:      route.Spec.HoldOnCircuitOpen,
		}
		// The SDK omits an empty failover list, which leaves the server's alone, so removing every
		// failoverDestinationRef needs an explicit null. Only when the spec names none: refs that
		// are named but not synced yet must not wipe the server's list.
		if len(route.Spec.FailoverDestinationRefs) == 0 {
			params.Clear = append(params.Clear, hookbasesdk.RouteFailoverDestinationIDs)
		}
		if route.Spec.FilterLogic != "" {
			params.FilterLogic = &route.Spec.FilterLogic
		}
		if route.Spec.Notifications != nil {
			params.NotifyOnFailure = route.Spec.Notifications.OnFailure
			params.NotifyOnSuccess = route.Spec.Notifications.OnSuccess
			params.NotifyOnRecovery = route.Spec.Notifications.OnRecovery
			if route.Spec.Notifications.Emails != "" {
				params.NotifyEmails = &route.Spec.Notifications.Emails
			}
		}

		if err := sdkClient.Routes.Update(ctx, route.Status.RouteID, params); err != nil {
			var notFound *hookbasesdk.NotFoundError
			if errors.As(err, &notFound) {
				route.Status.RouteID = ""
				_ = r.Status().Update(ctx, &route)
				return ctrl.Result{Requeue: true}, nil
			}
			hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "UpdateFailed", err.Error(), route.Generation)
			_ = r.Status().Update(ctx, &route)
			metrics.ReconcileTotal.WithLabelValues("WebhookRoute", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// Update resolved IDs in status
	route.Status.ResolvedSourceID = sourceID
	route.Status.ResolvedDestinationID = destID
	if filterID != nil {
		route.Status.ResolvedFilterID = *filterID
	}
	if transformID != nil {
		route.Status.ResolvedTransformID = *transformID
	}
	if schemaID != nil {
		route.Status.ResolvedSchemaID = *schemaID
	}

	now := metav1.Now()
	route.Status.LastSyncTime = &now
	route.Status.ObservedGeneration = route.Generation
	hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionTrue, "Synced", "Route is synced", route.Generation)
	hookbasev1alpha1.SetCondition(&route.Status.Conditions, hookbasev1alpha1.ConditionSynced,
		metav1.ConditionTrue, "Synced", fmt.Sprintf("Last synced at %s", now.Format(time.RFC3339)), route.Generation)

	_ = r.Status().Update(ctx, &route)
	metrics.ReconcileTotal.WithLabelValues("WebhookRoute", "success").Inc()
	return ctrl.Result{RequeueAfter: r.RequeueAfter}, nil
}

func (r *WebhookRouteReconciler) resolveSourceRef(ctx context.Context, namespace, name string) (string, error) {
	var source hookbasev1alpha1.WebhookSource
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &source); err != nil {
		return "", fmt.Errorf("WebhookSource %q not found: %w", name, err)
	}
	if source.Status.SourceID == "" {
		return "", fmt.Errorf("WebhookSource %q is not yet synced (no sourceId)", name)
	}
	return source.Status.SourceID, nil
}

func (r *WebhookRouteReconciler) resolveDestinationRef(ctx context.Context, namespace, name string) (string, error) {
	var dest hookbasev1alpha1.WebhookDestination
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &dest); err != nil {
		return "", fmt.Errorf("WebhookDestination %q not found: %w", name, err)
	}
	if dest.Status.DestinationID == "" {
		return "", fmt.Errorf("WebhookDestination %q is not yet synced (no destinationId)", name)
	}
	return dest.Status.DestinationID, nil
}

func (r *WebhookRouteReconciler) resolveFilterRef(ctx context.Context, namespace, name string) (string, error) {
	var filter hookbasev1alpha1.WebhookFilter
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &filter); err != nil {
		return "", fmt.Errorf("WebhookFilter %q not found: %w", name, err)
	}
	if filter.Status.FilterID == "" {
		return "", fmt.Errorf("WebhookFilter %q is not yet synced", name)
	}
	return filter.Status.FilterID, nil
}

func (r *WebhookRouteReconciler) resolveTransformRef(ctx context.Context, namespace, name string) (string, error) {
	var transform hookbasev1alpha1.WebhookTransform
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &transform); err != nil {
		return "", fmt.Errorf("WebhookTransform %q not found: %w", name, err)
	}
	if transform.Status.TransformID == "" {
		return "", fmt.Errorf("WebhookTransform %q is not yet synced", name)
	}
	return transform.Status.TransformID, nil
}

func (r *WebhookRouteReconciler) resolveSchemaRef(ctx context.Context, namespace, name string) (string, error) {
	var schema hookbasev1alpha1.WebhookSchema
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &schema); err != nil {
		return "", fmt.Errorf("WebhookSchema %q not found: %w", name, err)
	}
	if schema.Status.SchemaID == "" {
		return "", fmt.Errorf("WebhookSchema %q is not yet synced", name)
	}
	return schema.Status.SchemaID, nil
}

func (r *WebhookRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.WebhookRoute{}).
		Complete(r)
}
