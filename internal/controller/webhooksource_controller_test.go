package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/hookbase"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// sourceAPI is a minimal stand-in for the Hookbase sources endpoints. It records every request
// body and answers create/get with the configured source JSON.
type sourceAPI struct {
	mu       sync.Mutex
	source   map[string]interface{} // returned by POST (plus id) and GET
	reveal   string                 // GET /reveal-secret
	token    string                 // GET /verify-token
	requests []recorded
}

type recorded struct {
	Method string
	Path   string
	Body   map[string]interface{}
}

func (a *sourceAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	var body map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	a.requests = append(a.requests, recorded{Method: r.Method, Path: r.URL.Path, Body: body})
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == "POST" && r.URL.Path == "/api/sources":
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"source": a.source})
	case r.Method == "PATCH":
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/reveal-secret"):
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"signingSecret": a.reveal})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/verify-token"):
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"verifyToken": a.token})
	case r.Method == "GET":
		// Get never carries the one-time secrets.
		got := map[string]interface{}{}
		for k, v := range a.source {
			if k != "signingSecret" && k != "verifyToken" {
				got[k] = v
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"source": got})
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "not found"})
	}
}

func (a *sourceAPI) find(method, suffix string) []recorded {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []recorded
	for _, r := range a.requests {
		if r.Method == method && strings.HasSuffix(r.Path, suffix) {
			out = append(out, r)
		}
	}
	return out
}

type sourceHarness struct {
	t   *testing.T
	api *sourceAPI
	k8s client.Client
	rec *WebhookSourceReconciler
	src *hookbasev1alpha1.WebhookSource
}

func newSourceHarness(t *testing.T, apiSource map[string]interface{}, spec hookbasev1alpha1.WebhookSourceSpec, objs ...client.Object) *sourceHarness {
	t.Helper()
	api := &sourceAPI{source: apiSource}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := hookbasev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	src := &hookbasev1alpha1.WebhookSource{
		ObjectMeta: metav1.ObjectMeta{Name: "hooks", Namespace: "default", UID: types.UID("source-uid")},
		Spec:       spec,
	}
	k8s := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&hookbasev1alpha1.WebhookSource{}).
		WithObjects(append(objs, src)...).
		Build()

	rec := &WebhookSourceReconciler{
		Client:        k8s,
		Scheme:        scheme,
		ClientManager: hookbase.NewClientManager(k8s, server.URL, "whr_bootstrap_test_key"),
	}
	return &sourceHarness{t: t, api: api, k8s: k8s, rec: rec, src: src}
}

// reconcile runs the reconciler and returns the refreshed source.
func (h *sourceHarness) reconcile() *hookbasev1alpha1.WebhookSource {
	h.t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "hooks"}}
	if _, err := h.rec.Reconcile(context.Background(), req); err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}
	var got hookbasev1alpha1.WebhookSource
	if err := h.k8s.Get(context.Background(), req.NamespacedName, &got); err != nil {
		h.t.Fatal(err)
	}
	return &got
}

// sync runs reconciles until the source has been created (the first one only adds the finalizer).
func (h *sourceHarness) sync() *hookbasev1alpha1.WebhookSource {
	h.t.Helper()
	got := h.reconcile()
	if got.Status.SourceID == "" {
		got = h.reconcile()
	}
	if got.Status.SourceID == "" {
		h.t.Fatalf("source was not created; conditions: %+v", got.Status.Conditions)
	}
	return got
}

func (h *sourceHarness) secret(name string) *corev1.Secret {
	h.t.Helper()
	var s corev1.Secret
	err := h.k8s.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, &s)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return &s
}

func userSecret(name string, data map[string]string) *corev1.Secret {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}, Data: map[string][]byte{}}
	for k, v := range data {
		s.Data[k] = []byte(v)
	}
	return s
}

func apiSource(provider string, extra map[string]interface{}) map[string]interface{} {
	s := map[string]interface{}{
		"id":        "src_1",
		"name":      "Hooks",
		"slug":      "hooks",
		"provider":  provider,
		"ingestUrl": "https://api.example.test/ingest/org/hooks",
	}
	for k, v := range extra {
		s[k] = v
	}
	return s
}

func boolPtr(b bool) *bool { return &b }
func intPtr(i int) *int    { return &i }

func TestSourceCreateMapsFieldsToAPINames(t *testing.T) {
	h := newSourceHarness(t, apiSource("github", map[string]interface{}{"signingSecret": "gen"}),
		hookbasev1alpha1.WebhookSourceSpec{
			Name:               "My GitHub Hooks!",
			Provider:           "github",
			VerifySignature:    boolPtr(true), // deprecated alias
			RateLimitPerMinute: intPtr(500),
			DedupStrategy:      "provider_id",
			DedupWindowHours:   intPtr(24),
			IPFilterMode:       "both",
			AllowedMethods:     []hookbasev1alpha1.IngestMethod{"POST"},
		})
	h.sync()

	posts := h.api.find("POST", "/api/sources")
	if len(posts) != 1 {
		t.Fatalf("expected one create, got %d", len(posts))
	}
	body := posts[0].Body
	want := map[string]interface{}{
		"name":                    "My GitHub Hooks!",
		"slug":                    "my-github-hooks",
		"provider":                "github",
		"rejectInvalidSignatures": true,
		"rateLimitPerMinute":      float64(500),
		"dedupStrategy":           "provider_id",
		"dedupWindowHours":        float64(24),
		"ipFilterMode":            "both",
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("create body %s = %v, want %v", k, body[k], v)
		}
	}
	for _, stale := range []string{"verifySignature", "rateLimit", "rateLimitWindow", "dedupWindow", "signingSecret", "verifyToken"} {
		if _, ok := body[stale]; ok {
			t.Errorf("create body must not carry %q: %v", stale, body)
		}
	}
}

func TestSourceRejectInvalidSignaturesWinsOverDeprecatedAlias(t *testing.T) {
	src := &hookbasev1alpha1.WebhookSource{Spec: hookbasev1alpha1.WebhookSourceSpec{
		Name: "x", RejectInvalidSignatures: boolPtr(false), VerifySignature: boolPtr(true),
	}}
	if got := buildUpdateSourceParams(src).RejectInvalidSignatures; got == nil || *got {
		t.Fatalf("rejectInvalidSignatures should win over verifySignature, got %v", got)
	}
}

func TestSourceSigningSecretRefIsSentAndNeverOverwritten(t *testing.T) {
	user := userSecret("gh-secret", map[string]string{"secret": "user-chosen"})
	// The API echoes a secret on create; it must not land in the user's Secret.
	h := newSourceHarness(t, apiSource("github", map[string]interface{}{"signingSecret": "user-chosen", "hasSigningSecret": true}),
		hookbasev1alpha1.WebhookSourceSpec{
			Name:             "Hooks",
			Provider:         "github",
			SigningSecretRef: &hookbasev1alpha1.SecretKeyRef{Name: "gh-secret", Key: "secret"},
		}, user)
	got := h.sync()

	posts := h.api.find("POST", "/api/sources")
	if posts[0].Body["signingSecret"] != "user-chosen" {
		t.Fatalf("create should send the referenced secret, body: %v", posts[0].Body)
	}
	after := h.secret("gh-secret")
	if string(after.Data["secret"]) != "user-chosen" || len(after.Data) != 1 || len(after.OwnerReferences) != 0 {
		t.Fatalf("user Secret was modified: %+v", after)
	}
	if h.secret("hooks-signing-secret") != nil {
		t.Fatal("no operator-owned Secret expected when signingSecretRef is set")
	}
	if len(h.api.find("GET", "/reveal-secret")) != 0 {
		t.Fatal("must not reveal the secret when the user supplied it")
	}

	// Unchanged Secret: the update carries the provider but not the secret.
	h.reconcile()
	patches := h.api.find("PATCH", "/api/sources/src_1")
	if len(patches) != 1 {
		t.Fatalf("expected one update, got %d", len(patches))
	}
	if patches[0].Body["provider"] != "github" {
		t.Errorf("update should send provider, body: %v", patches[0].Body)
	}
	if _, ok := patches[0].Body["signingSecret"]; ok {
		t.Errorf("unchanged Secret should not be re-sent, body: %v", patches[0].Body)
	}
	if got.Status.SigningSecretRefVersion == "" {
		t.Error("expected signingSecretRefVersion to be recorded")
	}

	// Changed Secret: the new value is sent.
	after.Data["secret"] = []byte("rotated")
	if err := h.k8s.Update(context.Background(), after); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	patches = h.api.find("PATCH", "/api/sources/src_1")
	if last := patches[len(patches)-1].Body; last["signingSecret"] != "rotated" {
		t.Fatalf("changed Secret should be sent, body: %v", last)
	}
	if string(h.secret("gh-secret").Data["secret"]) != "rotated" {
		t.Fatal("user Secret was modified")
	}
}

func TestSourceProviderChangeResendsReferencedSecret(t *testing.T) {
	user := userSecret("sv-secret", map[string]string{"signingSecret": "whsec_abc"})
	h := newSourceHarness(t, apiSource("github", nil), hookbasev1alpha1.WebhookSourceSpec{
		Name:             "Hooks",
		Provider:         "github",
		SigningSecretRef: &hookbasev1alpha1.SecretKeyRef{Name: "sv-secret"},
	}, user)
	got := h.sync()

	got.Spec.Provider = "svix"
	if err := h.k8s.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	patches := h.api.find("PATCH", "/api/sources/src_1")
	last := patches[len(patches)-1].Body
	if last["provider"] != "svix" || last["signingSecret"] != "whsec_abc" {
		t.Fatalf("provider change should send provider and secret together, body: %v", last)
	}
}

func TestSourceGeneratedSecretStoredInOwnedSecret(t *testing.T) {
	h := newSourceHarness(t, apiSource("custom", map[string]interface{}{"signingSecret": "gen-123", "hasSigningSecret": true}),
		hookbasev1alpha1.WebhookSourceSpec{Name: "Hooks", Provider: "custom"})
	got := h.sync()

	s := h.secret("hooks-signing-secret")
	if s == nil {
		t.Fatal("expected operator-owned Secret hooks-signing-secret")
	}
	if string(s.Data["signingSecret"]) != "gen-123" {
		t.Fatalf("signingSecret key = %q", s.Data["signingSecret"])
	}
	if !metav1.IsControlledBy(s, got) {
		t.Fatal("owned Secret should be controlled by the WebhookSource")
	}
	if !got.Status.HasSigningSecret {
		t.Error("status.hasSigningSecret should be true")
	}
	if len(h.api.find("GET", "/reveal-secret")) != 0 {
		t.Error("secret from the create response should not be revealed again")
	}
}

func TestSourceNullSecretProvidersStoreNothing(t *testing.T) {
	for _, tc := range []struct {
		provider              string
		verifiesWithoutSecret bool
		awaiting              bool
	}{
		{"wise", true, false},
		{"wise-sandbox", true, false},
		{"aws-sns", true, false},
		{"asana", false, true},
		{"notion", false, true},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			h := newSourceHarness(t, apiSource(tc.provider, map[string]interface{}{
				"signingSecret":         nil,
				"hasSigningSecret":      false,
				"verifiesWithoutSecret": tc.verifiesWithoutSecret,
				"awaitingSigningSecret": tc.awaiting,
			}), hookbasev1alpha1.WebhookSourceSpec{Name: "Hooks", Provider: tc.provider})
			got := h.sync()

			if h.secret("hooks-signing-secret") != nil {
				t.Fatal("no Secret should be written for a null signing secret")
			}
			if got.Status.VerifiesWithoutSecret != tc.verifiesWithoutSecret || got.Status.AwaitingSigningSecret != tc.awaiting {
				t.Fatalf("status = %+v", got.Status)
			}
			ready := false
			for _, c := range got.Status.Conditions {
				if c.Type == string(hookbasev1alpha1.ConditionReady) && c.Status == metav1.ConditionTrue {
					ready = true
				}
			}
			if !ready {
				t.Fatalf("expected Ready, conditions: %+v", got.Status.Conditions)
			}
			if len(h.api.find("GET", "/reveal-secret")) != 0 {
				t.Fatal("nothing to reveal for a source without a secret")
			}
		})
	}
}

func TestSourceWhatsAppGeneratedVerifyTokenStored(t *testing.T) {
	user := userSecret("meta-app", map[string]string{"signingSecret": "app-secret"})
	h := newSourceHarness(t, apiSource("whatsapp", map[string]interface{}{
		"signingSecret": "app-secret", "hasSigningSecret": true, "verifyToken": "vt-generated",
	}), hookbasev1alpha1.WebhookSourceSpec{
		Name:             "Hooks",
		Provider:         "whatsapp",
		SigningSecretRef: &hookbasev1alpha1.SecretKeyRef{Name: "meta-app"},
	}, user)
	h.sync()

	if _, ok := h.api.find("POST", "/api/sources")[0].Body["verifyToken"]; ok {
		t.Fatal("verifyToken should be omitted so the API generates one")
	}
	s := h.secret("hooks-signing-secret")
	if s == nil || string(s.Data["verifyToken"]) != "vt-generated" {
		t.Fatalf("expected verifyToken in owned Secret, got %+v", s)
	}
	if _, ok := s.Data["signingSecret"]; ok {
		t.Fatal("signing secret is user-referenced and must not be copied into the owned Secret")
	}
	if len(user.Data) != 1 || string(h.secret("meta-app").Data["signingSecret"]) != "app-secret" || len(h.secret("meta-app").Data) != 1 {
		t.Fatal("user Secret was modified")
	}
}

func TestSourceWhatsAppVerifyTokenRefSent(t *testing.T) {
	vt := userSecret("meta-vt", map[string]string{"verifyToken": "my-token"})
	h := newSourceHarness(t, apiSource("whatsapp", map[string]interface{}{"verifyToken": "my-token"}),
		hookbasev1alpha1.WebhookSourceSpec{
			Name:           "Hooks",
			Provider:       "whatsapp",
			VerifyTokenRef: &hookbasev1alpha1.SecretKeyRef{Name: "meta-vt"},
		}, vt)
	h.sync()

	if got := h.api.find("POST", "/api/sources")[0].Body["verifyToken"]; got != "my-token" {
		t.Fatalf("create verifyToken = %v", got)
	}
	if s := h.secret("hooks-signing-secret"); s != nil {
		if _, ok := s.Data["verifyToken"]; ok {
			t.Fatal("verify token from a ref must not be copied into the owned Secret")
		}
	}
	if len(h.secret("meta-vt").OwnerReferences) != 0 || string(h.secret("meta-vt").Data["verifyToken"]) != "my-token" {
		t.Fatal("user verify-token Secret was modified")
	}
}

func TestSourceBackfillsMissingVerifyToken(t *testing.T) {
	// A whatsapp source that already exists (created before tokens were stored).
	h := newSourceHarness(t, apiSource("whatsapp", nil), hookbasev1alpha1.WebhookSourceSpec{Name: "Hooks", Provider: "whatsapp"})
	h.api.token = "vt-existing"
	h.sync()
	h.reconcile()

	s := h.secret("hooks-signing-secret")
	if s == nil || string(s.Data["verifyToken"]) != "vt-existing" {
		t.Fatalf("expected backfilled verifyToken, got %+v", s)
	}
}

func TestSourceForeignSecretWithOwnedNameIsNotOverwritten(t *testing.T) {
	foreign := userSecret("hooks-signing-secret", map[string]string{"signingSecret": "someone-elses"})
	h := newSourceHarness(t, apiSource("custom", map[string]interface{}{"signingSecret": "gen-xyz", "hasSigningSecret": true}),
		hookbasev1alpha1.WebhookSourceSpec{Name: "Hooks", Provider: "custom"}, foreign)
	h.sync()

	if got := string(h.secret("hooks-signing-secret").Data["signingSecret"]); got != "someone-elses" {
		t.Fatalf("a Secret the source does not own was overwritten: %q", got)
	}
}

func TestSourceMissingSecretRefFailsWithoutCreating(t *testing.T) {
	h := newSourceHarness(t, apiSource("github", nil), hookbasev1alpha1.WebhookSourceSpec{
		Name:             "Hooks",
		Provider:         "github",
		SigningSecretRef: &hookbasev1alpha1.SecretKeyRef{Name: "absent"},
	})
	h.reconcile()
	got := h.reconcile()
	if got.Status.SourceID != "" || len(h.api.find("POST", "/api/sources")) != 0 {
		t.Fatal("source must not be created without its referenced secret")
	}
	for _, c := range got.Status.Conditions {
		if c.Type == string(hookbasev1alpha1.ConditionReady) {
			if c.Reason != "SigningSecretRefError" {
				t.Fatalf("Ready reason = %q", c.Reason)
			}
			return
		}
	}
	t.Fatal("expected a Ready condition")
}

func TestSourceSlugDerivation(t *testing.T) {
	for _, tc := range []struct{ slug, name, objName, want string }{
		{"explicit", "Anything", "obj", "explicit"},
		{"", "Stripe Payments (prod)", "obj", "stripe-payments-prod"},
		{"", "---", "my-obj", "my-obj"},
		{"", strings.Repeat("a", 60), "obj", strings.Repeat("a", 50)},
	} {
		src := &hookbasev1alpha1.WebhookSource{
			ObjectMeta: metav1.ObjectMeta{Name: tc.objName},
			Spec:       hookbasev1alpha1.WebhookSourceSpec{Slug: tc.slug, Name: tc.name},
		}
		if got := sourceSlug(src); got != tc.want {
			t.Errorf("sourceSlug(%q, %q) = %q, want %q", tc.slug, tc.name, got, tc.want)
		}
	}
}

func TestIngressRejectInvalidSignaturesAnnotations(t *testing.T) {
	for _, tc := range []struct {
		annotations map[string]string
		want        *bool
	}{
		{map[string]string{}, nil},
		{map[string]string{"hookbase.io/source-verify-signature": "true"}, boolPtr(true)},
		{map[string]string{"hookbase.io/source-reject-invalid-signatures": "true"}, boolPtr(true)},
		{map[string]string{"hookbase.io/source-reject-invalid-signatures": "false", "hookbase.io/source-verify-signature": "true"}, boolPtr(false)},
		{map[string]string{"hookbase.io/source-verify-signature": "yes please"}, nil},
	} {
		got := sourceRejectInvalidSignatures(tc.annotations)
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%v: got %v, want %v", tc.annotations, got, tc.want)
		}
	}
}

func TestSourceRemovedListsAreClearedOnTheServer(t *testing.T) {
	h := newSourceHarness(t, apiSource("custom", nil), hookbasev1alpha1.WebhookSourceSpec{
		Name:           "Hooks",
		Provider:       "custom",
		IPFilterMode:   "both",
		IPAllowlist:    []string{"10.0.0.0/8"},
		IPDenylist:     []string{"10.1.0.0/16"},
		AllowedMethods: []hookbasev1alpha1.IngestMethod{"POST"},
	})
	h.sync()
	lists := []string{"ipAllowlist", "ipDenylist", "allowedMethods"}

	// Still set: the update restates them.
	got := h.reconcile()
	patches := h.api.find("PATCH", "/api/sources/src_1")
	for _, field := range lists {
		if values, ok := patches[0].Body[field].([]interface{}); !ok || len(values) != 1 {
			t.Errorf("update %s = %v", field, patches[0].Body[field])
		}
	}

	// Removed from the manifest: each must go out as an explicit null, or the server keeps it.
	got.Spec.IPFilterMode = "none"
	got.Spec.IPAllowlist = nil
	got.Spec.IPDenylist = []string{} // an explicit empty list clears too
	got.Spec.AllowedMethods = nil
	if err := h.k8s.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	patches = h.api.find("PATCH", "/api/sources/src_1")
	last := patches[len(patches)-1].Body
	for _, field := range lists {
		if value, ok := last[field]; !ok || value != nil {
			t.Errorf("update should clear %s with null, sent %v (present: %v)", field, value, ok)
		}
	}
}

func TestSourceCatalogProviderSentOnCreateOnly(t *testing.T) {
	h := newSourceHarness(t, apiSource("stripe", map[string]interface{}{"catalogProvider": "stripe"}),
		hookbasev1alpha1.WebhookSourceSpec{Name: "Hooks", Provider: "stripe", CatalogProvider: "stripe"})
	h.sync()
	h.reconcile()

	if got := h.api.find("POST", "/api/sources")[0].Body["catalogProvider"]; got != "stripe" {
		t.Fatalf("create catalogProvider = %v", got)
	}
	if _, ok := h.api.find("PATCH", "/api/sources/src_1")[0].Body["catalogProvider"]; ok {
		t.Fatal("update must not send catalogProvider: the API sets it on create only")
	}
}
