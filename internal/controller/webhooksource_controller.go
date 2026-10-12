package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

	// 5. Resolve user-referenced secrets. These Secrets are read, never written.
	signingSecret, signingSecretVersion, err := r.readSecretRef(ctx, source.Namespace, source.Spec.SigningSecretRef, ownedSecretKeySigningSecret)
	if err != nil {
		return r.failRef(ctx, &source, "SigningSecretRefError", err)
	}
	verifyToken, verifyTokenVersion, err := r.readSecretRef(ctx, source.Namespace, source.Spec.VerifyTokenRef, ownedSecretKeyVerifyToken)
	if err != nil {
		return r.failRef(ctx, &source, "VerifyTokenRefError", err)
	}

	// 6. Create or Update
	var apiSource *hookbasesdk.Source
	if source.Status.SourceID == "" {
		// CREATE
		createParams := buildCreateSourceParams(&source)
		if source.Spec.SigningSecretRef != nil {
			createParams.SigningSecret = &signingSecret
		}
		if source.Spec.VerifyTokenRef != nil {
			createParams.VerifyToken = &verifyToken
		}

		created, err := sdkClient.Sources.Create(ctx, createParams)
		if err != nil {
			log.Error(err, "failed to create source in Hookbase API")
			hookbasev1alpha1.SetCondition(&source.Status.Conditions, hookbasev1alpha1.ConditionReady,
				metav1.ConditionFalse, "CreateFailed", err.Error(), source.Generation)
			_ = r.Status().Update(ctx, &source)
			metrics.ReconcileTotal.WithLabelValues("WebhookSource", "error").Inc()
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		apiSource = created

		source.Status.SourceID = created.ID
		source.Status.SigningSecretRefVersion = signingSecretVersion
		source.Status.VerifyTokenRefVersion = verifyTokenVersion

		// The API returns a generated secret / verify token once, on create. Keep them in the
		// operator-owned Secret, but only when the user did not supply their own: a referenced
		// Secret is never written to. A null secret (built-in key providers such as wise and
		// aws-sns, or asana/notion whose secret arrives after registration) stores nothing.
		if source.Spec.SigningSecretRef == nil && created.SigningSecret != nil && *created.SigningSecret != "" {
			if err := r.storeOwnedSecretKey(ctx, &source, ownedSecretKeySigningSecret, *created.SigningSecret); err != nil {
				log.Error(err, "failed to store signing secret")
			}
		}
		if source.Spec.VerifyTokenRef == nil && created.VerifyToken != nil && *created.VerifyToken != "" {
			if err := r.storeOwnedSecretKey(ctx, &source, ownedSecretKeyVerifyToken, *created.VerifyToken); err != nil {
				log.Error(err, "failed to store verify token")
			}
		}

		log.Info("created source in Hookbase API", "sourceId", created.ID)
	} else {
		// UPDATE
		updateParams := buildUpdateSourceParams(&source)

		// Referenced secrets are re-sent only when the Secret changed since the last sync, or when
		// the provider changed (the API needs the new provider's secret in the same update).
		providerChanged := source.Status.Provider != source.Spec.Provider
		if source.Spec.SigningSecretRef != nil && (providerChanged || signingSecretVersion != source.Status.SigningSecretRefVersion) {
			updateParams.SigningSecret = &signingSecret
		}
		if source.Spec.VerifyTokenRef != nil && (providerChanged || verifyTokenVersion != source.Status.VerifyTokenRefVersion) {
			updateParams.VerifyToken = &verifyToken
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
		source.Status.SigningSecretRefVersion = signingSecretVersion
		source.Status.VerifyTokenRefVersion = verifyTokenVersion
		log.V(1).Info("updated source in Hookbase API", "sourceId", source.Status.SourceID)

		// PATCH answers {success: true}; read the source back for the status fields.
		got, err := sdkClient.Sources.Get(ctx, source.Status.SourceID)
		if err != nil {
			log.Error(err, "failed to read source back after update", "sourceId", source.Status.SourceID)
		} else {
			apiSource = got
		}
	}

	if apiSource != nil {
		applySourceStatus(&source.Status, apiSource)
		r.backfillOwnedSecret(ctx, sdkClient, &source, apiSource)
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

const (
	// ownedSecretKeySigningSecret is the key the API-generated signing secret is stored under in
	// the operator-owned Secret, and the default key read from spec.signingSecretRef.
	ownedSecretKeySigningSecret = "signingSecret"
	// ownedSecretKeyVerifyToken is the key the API-generated Meta verify token is stored under in
	// the operator-owned Secret, and the default key read from spec.verifyTokenRef.
	ownedSecretKeyVerifyToken = "verifyToken"
	// providerWhatsApp is the only provider with a Meta verify token.
	providerWhatsApp = "whatsapp"
)

// ownedSecretName is the operator-owned Secret holding API-generated credentials for a source.
func ownedSecretName(source *hookbasev1alpha1.WebhookSource) string {
	return fmt.Sprintf("%s-signing-secret", source.Name)
}

// rejectInvalidSignatures resolves spec.rejectInvalidSignatures, falling back to the deprecated
// spec.verifySignature.
func rejectInvalidSignatures(spec *hookbasev1alpha1.WebhookSourceSpec) *bool {
	if spec.RejectInvalidSignatures != nil {
		return spec.RejectInvalidSignatures
	}
	return spec.VerifySignature
}

// sourceSlug returns spec.slug, or one derived from spec.name (falling back to the object
// name) in the form the API accepts: ^[a-z0-9-]+$, at most 50 characters.
func sourceSlug(source *hookbasev1alpha1.WebhookSource) string {
	if source.Spec.Slug != "" {
		return source.Spec.Slug
	}
	if slug := slugify(source.Spec.Name); slug != "" {
		return slug
	}
	return slugify(source.Name)
}

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := b.String()
	if len(slug) > 50 {
		slug = slug[:50]
	}
	return strings.Trim(slug, "-")
}

func methodStrings(methods []hookbasev1alpha1.IngestMethod) []string {
	if methods == nil {
		return nil
	}
	out := make([]string, len(methods))
	for i, m := range methods {
		out[i] = string(m)
	}
	return out
}

// buildCreateSourceParams maps the spec onto the API's create body. Secrets from references
// are added by the caller.
func buildCreateSourceParams(source *hookbasev1alpha1.WebhookSource) *hookbasesdk.CreateSourceParams {
	spec := &source.Spec
	params := &hookbasesdk.CreateSourceParams{
		Name:                    spec.Name,
		Slug:                    sourceSlug(source),
		RejectInvalidSignatures: rejectInvalidSignatures(spec),
		RateLimitPerMinute:      spec.RateLimitPerMinute,
		DedupWindowHours:        spec.DedupWindowHours,
		TransientMode:           spec.TransientMode,
		IPAllowlist:             spec.IPAllowlist,
		IPDenylist:              spec.IPDenylist,
		AllowedMethods:          methodStrings(spec.AllowedMethods),
	}
	if spec.Provider != "" {
		provider := hookbasesdk.SourceProvider(spec.Provider)
		params.Provider = &provider
	}
	if spec.Description != "" {
		params.Description = &spec.Description
	}
	if spec.DedupStrategy != "" {
		ds := hookbasesdk.DedupStrategy(spec.DedupStrategy)
		params.DedupStrategy = &ds
	}
	if spec.IPFilterMode != "" {
		mode := hookbasesdk.IPFilterMode(spec.IPFilterMode)
		params.IPFilterMode = &mode
	}
	return params
}

// buildUpdateSourceParams maps the spec onto the API's update body, provider included. Secrets
// from references are added by the caller.
func buildUpdateSourceParams(source *hookbasev1alpha1.WebhookSource) *hookbasesdk.UpdateSourceParams {
	spec := &source.Spec
	params := &hookbasesdk.UpdateSourceParams{
		Name:                    &spec.Name,
		RejectInvalidSignatures: rejectInvalidSignatures(spec),
		RateLimitPerMinute:      spec.RateLimitPerMinute,
		DedupWindowHours:        spec.DedupWindowHours,
		TransientMode:           spec.TransientMode,
		IPAllowlist:             spec.IPAllowlist,
		IPDenylist:              spec.IPDenylist,
		AllowedMethods:          methodStrings(spec.AllowedMethods),
	}
	if spec.Provider != "" {
		provider := hookbasesdk.SourceProvider(spec.Provider)
		params.Provider = &provider
	}
	if spec.Description != "" {
		params.Description = &spec.Description
	}
	if spec.DedupStrategy != "" {
		ds := hookbasesdk.DedupStrategy(spec.DedupStrategy)
		params.DedupStrategy = &ds
	}
	if spec.IPFilterMode != "" {
		mode := hookbasesdk.IPFilterMode(spec.IPFilterMode)
		params.IPFilterMode = &mode
	}
	return params
}

// applySourceStatus copies the API's view of the source into status.
func applySourceStatus(status *hookbasev1alpha1.WebhookSourceStatus, src *hookbasesdk.Source) {
	if src.IngestURL != nil {
		status.IngestURL = *src.IngestURL
	}
	status.Provider = string(src.Provider)
	status.HasSigningSecret = src.HasSigningSecret.Bool()
	status.VerifiesWithoutSecret = src.VerifiesWithoutSecret.Bool()
	status.AwaitingSigningSecret = src.AwaitingSigningSecret.Bool()
}

// readSecretRef reads a user-referenced Secret key. It returns the value and the Secret's
// resourceVersion, or empty strings when ref is nil.
func (r *WebhookSourceReconciler) readSecretRef(ctx context.Context, namespace string, ref *hookbasev1alpha1.SecretKeyRef, defaultKey string) (string, string, error) {
	if ref == nil {
		return "", "", nil
	}
	key := ref.Key
	if key == "" {
		key = defaultKey
	}
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		return "", "", fmt.Errorf("failed to get Secret %s/%s: %w", namespace, ref.Name, err)
	}
	value, ok := secret.Data[key]
	if !ok || len(value) == 0 {
		return "", "", fmt.Errorf("Secret %s/%s has no value under key %q", namespace, ref.Name, key)
	}
	return string(value), secret.ResourceVersion, nil
}

func (r *WebhookSourceReconciler) failRef(ctx context.Context, source *hookbasev1alpha1.WebhookSource, reason string, err error) (ctrl.Result, error) {
	log.FromContext(ctx).Error(err, "failed to resolve secret reference")
	hookbasev1alpha1.SetCondition(&source.Status.Conditions, hookbasev1alpha1.ConditionReady,
		metav1.ConditionFalse, reason, err.Error(), source.Generation)
	_ = r.Status().Update(ctx, source)
	metrics.ReconcileTotal.WithLabelValues("WebhookSource", "error").Inc()
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// backfillOwnedSecret fills the operator-owned Secret with credentials it is missing (for
// instance a source created before this operator stored them, a Secret deleted by hand, or an
// asana secret that arrived after registration). Only keys with no user reference are touched,
// and failures are logged rather than failing the reconcile.
func (r *WebhookSourceReconciler) backfillOwnedSecret(ctx context.Context, sdkClient *hookbasesdk.Client, source *hookbasev1alpha1.WebhookSource, src *hookbasesdk.Source) {
	log := log.FromContext(ctx)
	wantSecret := source.Spec.SigningSecretRef == nil && src.HasSigningSecret.Bool()
	wantToken := source.Spec.VerifyTokenRef == nil && string(src.Provider) == providerWhatsApp
	if !wantSecret && !wantToken {
		return
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Namespace: source.Namespace, Name: ownedSecretName(source)}, existing)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "failed to read operator-owned Secret")
		return
	}
	if err == nil && !metav1.IsControlledBy(existing, source) {
		return // not ours; storeOwnedSecretKey would refuse anyway
	}

	if wantSecret && len(existing.Data[ownedSecretKeySigningSecret]) == 0 {
		if value, err := sdkClient.Sources.RevealSecret(ctx, src.ID); err != nil {
			log.Error(err, "failed to reveal signing secret for backfill", "sourceId", src.ID)
		} else if value != "" {
			if err := r.storeOwnedSecretKey(ctx, source, ownedSecretKeySigningSecret, value); err != nil {
				log.Error(err, "failed to store signing secret")
			}
		}
	}
	if wantToken && len(existing.Data[ownedSecretKeyVerifyToken]) == 0 {
		if value, err := sdkClient.Sources.VerifyToken(ctx, src.ID); err != nil {
			log.Error(err, "failed to read verify token for backfill", "sourceId", src.ID)
		} else if value != "" {
			if err := r.storeOwnedSecretKey(ctx, source, ownedSecretKeyVerifyToken, value); err != nil {
				log.Error(err, "failed to store verify token")
			}
		}
	}
}

// storeOwnedSecretKey sets one key in the operator-owned Secret "<name>-signing-secret",
// creating it (owned by the source, so it is garbage-collected with it) when missing. Other keys
// are preserved. A Secret of that name that this source does not control is never modified.
func (r *WebhookSourceReconciler) storeOwnedSecretKey(ctx context.Context, source *hookbasev1alpha1.WebhookSource, key, value string) error {
	name := ownedSecretName(source)

	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Namespace: source.Namespace, Name: name}, existing)
	if apierrors.IsNotFound(err) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: source.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "hookbase-operator",
					"hookbase.io/source":           source.Name,
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{key: []byte(value)},
		}
		if err := controllerutil.SetControllerReference(source, secret, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, secret)
	}
	if err != nil {
		return err
	}
	if !metav1.IsControlledBy(existing, source) {
		return fmt.Errorf("Secret %s/%s exists and is not owned by WebhookSource %s; refusing to overwrite it", source.Namespace, name, source.Name)
	}
	if string(existing.Data[key]) == value {
		return nil
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	existing.Data[key] = []byte(value)
	return r.Update(ctx, existing)
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebhookSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hookbasev1alpha1.WebhookSource{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
