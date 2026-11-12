package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/hookbase"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testDestinationName = "relay"

type destinationHarness struct {
	t   *testing.T
	api *fakeAPI
	k8s client.Client
	rec *WebhookDestinationReconciler

	apiDest map[string]interface{} // POST answers it; GET answers it without signingSecret
	reveal  string                 // GET /reveal-secret
}

func newDestinationHarness(t *testing.T, apiDest map[string]interface{}, dest *hookbasev1alpha1.WebhookDestination, objs ...client.Object) *destinationHarness {
	t.Helper()
	h := &destinationHarness{t: t, apiDest: apiDest}
	h.api = &fakeAPI{handle: func(method, path string) (int, interface{}) {
		switch {
		case method == "POST" && path == "/api/destinations":
			return http.StatusCreated, map[string]interface{}{"destination": h.apiDest}
		case method == "PATCH":
			return http.StatusOK, map[string]interface{}{"success": true}
		case method == "GET" && strings.HasSuffix(path, "/reveal-secret"):
			return http.StatusOK, map[string]interface{}{"signingSecret": h.reveal}
		case method == "GET":
			got := map[string]interface{}{}
			for k, v := range h.apiDest {
				if k != "signingSecret" {
					got[k] = v
				}
			}
			return http.StatusOK, map[string]interface{}{"destination": got}
		}
		return http.StatusNotFound, map[string]interface{}{"error": "not found"}
	}}
	server := httptest.NewServer(h.api)
	t.Cleanup(server.Close)

	scheme := testScheme(t)
	dest.ObjectMeta = metav1.ObjectMeta{
		Name: testDestinationName, Namespace: "default", UID: types.UID("dest-uid"),
		Finalizers: dest.Finalizers,
	}
	h.k8s = fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&hookbasev1alpha1.WebhookDestination{}).
		WithObjects(append(objs, dest)...).
		Build()
	h.rec = &WebhookDestinationReconciler{
		Client:        h.k8s,
		Scheme:        scheme,
		ClientManager: hookbase.NewClientManager(h.k8s, server.URL, "whr_bootstrap_test_key"),
	}
	return h
}

func (h *destinationHarness) reconcile() *hookbasev1alpha1.WebhookDestination {
	h.t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: testDestinationName}}
	if _, err := h.rec.Reconcile(context.Background(), req); err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}
	var got hookbasev1alpha1.WebhookDestination
	if err := h.k8s.Get(context.Background(), req.NamespacedName, &got); err != nil {
		h.t.Fatal(err)
	}
	return &got
}

// sync runs reconciles until the destination has been created (the first one adds the finalizer).
func (h *destinationHarness) sync() *hookbasev1alpha1.WebhookDestination {
	h.t.Helper()
	got := h.reconcile()
	if got.Status.DestinationID == "" {
		got = h.reconcile()
	}
	if got.Status.DestinationID == "" {
		h.t.Fatalf("destination was not created; conditions: %+v", got.Status.Conditions)
	}
	return got
}

func (h *destinationHarness) secret(name string) *corev1.Secret {
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

func httpDestination(spec hookbasev1alpha1.WebhookDestinationSpec) *hookbasev1alpha1.WebhookDestination {
	if spec.Name == "" {
		spec.Name = "Relay"
	}
	if spec.URL == "" && (spec.Type == "" || spec.Type == "http") {
		spec.URL = "https://receiver.example.test/hooks"
	}
	return &hookbasev1alpha1.WebhookDestination{Spec: spec}
}

// existing marks a destination as already created in Hookbase as dst_1.
func existing(dest *hookbasev1alpha1.WebhookDestination) *hookbasev1alpha1.WebhookDestination {
	dest.Finalizers = []string{hookbasev1alpha1.Finalizer}
	dest.Status.DestinationID = "dst_1"
	return dest
}

func apiDestination(destType string, extra map[string]interface{}) map[string]interface{} {
	d := map[string]interface{}{"id": "dst_1", "name": "Relay", "slug": "relay", "type": destType}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

func readyCondition(t *testing.T, conditions []metav1.Condition) metav1.Condition {
	t.Helper()
	for _, c := range conditions {
		if c.Type == string(hookbasev1alpha1.ConditionReady) {
			return c
		}
	}
	t.Fatalf("no Ready condition in %+v", conditions)
	return metav1.Condition{}
}

func TestDestinationHTTPCreateStoresSigningSecretInOwnedSecret(t *testing.T) {
	h := newDestinationHarness(t,
		apiDestination("http", map[string]interface{}{"hasSigningSecret": true, "signingSecret": "whsec_created"}),
		httpDestination(hookbasev1alpha1.WebhookDestinationSpec{TimeoutMs: intPtr(5000)}))
	got := h.sync()

	if !got.Status.HasSigningSecret {
		t.Error("status.hasSigningSecret should be true")
	}
	s := h.secret("relay-signing-secret")
	if s == nil {
		t.Fatal("expected operator-owned Secret relay-signing-secret")
	}
	if string(s.Data["signingSecret"]) != "whsec_created" || len(s.Data) != 1 {
		t.Fatalf("owned Secret data has keys %v", reflect.ValueOf(s.Data).MapKeys())
	}
	if !metav1.IsControlledBy(s, got) {
		t.Fatal("owned Secret should be controlled by the WebhookDestination")
	}
	if s.Labels["hookbase.io/destination"] != testDestinationName || s.Labels["app.kubernetes.io/managed-by"] != "hookbase-operator" {
		t.Errorf("owned Secret labels = %v", s.Labels)
	}
	if len(h.api.find("GET", "/reveal-secret")) != 0 {
		t.Error("a secret from the create response should not be revealed again")
	}

	create := h.api.find("POST", "/api/destinations")[0].Body
	if create["timeoutMs"] != float64(5000) {
		t.Errorf("create timeoutMs = %v", create["timeoutMs"])
	}
	for _, absent := range []string{"type", "config", "timeout", "description", "retryCount"} {
		if _, ok := create[absent]; ok {
			t.Errorf("create body must not carry %q: %v", absent, create)
		}
	}

	// The update path reads the destination back and finds the secret already stored.
	got = h.reconcile()
	if n := len(h.api.find("PATCH", "/api/destinations/dst_1")); n != 1 {
		t.Fatalf("expected one update, got %d", n)
	}
	if len(h.api.find("GET", "/reveal-secret")) != 0 {
		t.Error("a stored secret should not be revealed")
	}
	if !got.Status.HasSigningSecret {
		t.Error("status.hasSigningSecret should stay true")
	}

	status, _ := json.Marshal(got.Status)
	if strings.Contains(string(status), "whsec_created") {
		t.Fatal("the signing secret leaked into status")
	}
}

// The create schema has no "" for authType, so v0.4.1 could not create a destination without
// spec.authType: every create was a 400.
func TestDestinationCreateOmitsEmptyAuthType(t *testing.T) {
	h := newDestinationHarness(t, apiDestination("http", nil),
		httpDestination(hookbasev1alpha1.WebhookDestinationSpec{}))
	h.sync()

	create := h.api.find("POST", "/api/destinations")[0].Body
	if v, ok := create["authType"]; ok {
		t.Fatalf("create must omit an unset authType, sent %q", v)
	}
}

func TestDestinationCreateSendsAuthTypeWhenSet(t *testing.T) {
	auth := userSecret("relay-auth", map[string]string{"token": "tok"})
	h := newDestinationHarness(t, apiDestination("http", nil),
		httpDestination(hookbasev1alpha1.WebhookDestinationSpec{
			AuthType:      "bearer",
			AuthSecretRef: &hookbasev1alpha1.SecretKeyRef{Name: "relay-auth"},
		}), auth)
	h.sync()

	create := h.api.find("POST", "/api/destinations")[0].Body
	if create["authType"] != "bearer" {
		t.Fatalf("create authType = %v", create["authType"])
	}
}

func TestDestinationBackfillsSigningSecretThroughReveal(t *testing.T) {
	// A destination an older operator created, which never stored its secret.
	h := newDestinationHarness(t, apiDestination("http", map[string]interface{}{"hasSigningSecret": true}),
		existing(httpDestination(hookbasev1alpha1.WebhookDestinationSpec{})))
	h.reveal = "whsec_revealed"

	got := h.reconcile()
	if len(h.api.find("POST", "/api/destinations")) != 0 {
		t.Fatal("an existing destination must not be created again")
	}
	s := h.secret("relay-signing-secret")
	if s == nil || string(s.Data["signingSecret"]) != "whsec_revealed" {
		t.Fatalf("expected the revealed secret in the owned Secret, got %+v", s)
	}
	if !metav1.IsControlledBy(s, got) {
		t.Fatal("owned Secret should be controlled by the WebhookDestination")
	}
	if !got.Status.HasSigningSecret {
		t.Error("status.hasSigningSecret should be true")
	}

	h.reconcile()
	if n := len(h.api.find("GET", "/reveal-secret")); n != 1 {
		t.Fatalf("the secret should be revealed once, not on every reconcile: %d reveals", n)
	}
}

func TestDestinationForeignSecretWithOwnedNameIsNotOverwritten(t *testing.T) {
	foreign := userSecret("relay-signing-secret", map[string]string{"signingSecret": "someone-elses"})
	h := newDestinationHarness(t,
		apiDestination("http", map[string]interface{}{"hasSigningSecret": true, "signingSecret": "whsec_created"}),
		httpDestination(hookbasev1alpha1.WebhookDestinationSpec{}), foreign)
	h.reveal = "whsec_revealed"
	got := h.sync()
	h.reconcile()

	after := h.secret("relay-signing-secret")
	if string(after.Data["signingSecret"]) != "someone-elses" || len(after.Data) != 1 || len(after.OwnerReferences) != 0 {
		t.Fatalf("a Secret the destination does not own was modified: %+v", after)
	}
	if len(h.api.find("GET", "/reveal-secret")) != 0 {
		t.Error("nothing should be revealed when the owned name is taken")
	}
	if readyCondition(t, got.Status.Conditions).Status != metav1.ConditionTrue {
		t.Errorf("the destination itself should still sync: %+v", got.Status.Conditions)
	}
}

func TestDestinationQueueTypeConfigFromSecretAndNoSigningSecret(t *testing.T) {
	cfg := userSecret("sqs-config", map[string]string{
		"queueUrl": "https://sqs.us-east-1.amazonaws.com/123/q", "region": "us-east-1",
		"accessKeyId": "AKIA", "secretAccessKey": "s3cr3t",
	})
	h := newDestinationHarness(t,
		apiDestination("sqs", map[string]interface{}{"hasSigningSecret": false, "signingSecret": nil}),
		&hookbasev1alpha1.WebhookDestination{Spec: hookbasev1alpha1.WebhookDestinationSpec{
			Name: "Queue", Type: "sqs", ConfigSecretRef: &hookbasev1alpha1.SecretKeyRef{Name: "sqs-config"},
		}}, cfg)
	got := h.sync()

	create := h.api.find("POST", "/api/destinations")[0].Body
	if create["type"] != "sqs" {
		t.Errorf("create type = %v", create["type"])
	}
	wantConfig := map[string]interface{}{
		"queueUrl": "https://sqs.us-east-1.amazonaws.com/123/q", "region": "us-east-1",
		"accessKeyId": "AKIA", "secretAccessKey": "s3cr3t",
	}
	if !reflect.DeepEqual(create["config"], wantConfig) {
		t.Errorf("create config = %v", create["config"])
	}
	for _, absent := range []string{"url", "authType"} {
		if _, ok := create[absent]; ok {
			t.Errorf("create body must not carry %q", absent)
		}
	}

	got = h.reconcile()
	if h.secret("relay-signing-secret") != nil {
		t.Fatal("a queue destination has no signing secret to store")
	}
	if got.Status.HasSigningSecret {
		t.Error("status.hasSigningSecret should be false")
	}
	if len(h.api.find("GET", "/reveal-secret")) != 0 || len(h.api.find("GET", "/api/destinations/dst_1")) != 0 {
		t.Error("a non-http destination needs no read-back or reveal")
	}
	patch := h.api.find("PATCH", "/api/destinations/dst_1")[0].Body
	if !reflect.DeepEqual(patch["config"], wantConfig) {
		t.Errorf("update config = %v", patch["config"])
	}
}

func TestDestinationBridgeSendsStructuredConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bridge hookbasev1alpha1.BridgeSpec
		want   map[string]interface{}
	}{
		{
			name: "field",
			bridge: hookbasev1alpha1.BridgeSpec{
				EventTypeSource: "field", EventTypeField: "data.type", EventTypePrefix: "stripe.",
				ApplicationID: "7c9e6679-7425-40de-944b-e07fc1f90ae7",
			},
			want: map[string]interface{}{
				"eventTypeSource": "field", "eventTypeField": "data.type", "eventTypePrefix": "stripe.",
				"applicationId": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
			},
		},
		{
			name:   "fixed",
			bridge: hookbasev1alpha1.BridgeSpec{EventTypeSource: "fixed", EventType: "order.created", AllowUnverifiedSources: true},
			want:   map[string]interface{}{"eventTypeSource": "fixed", "eventType": "order.created", "allowUnverifiedSources": true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := tc.bridge
			h := newDestinationHarness(t,
				apiDestination("bridge", map[string]interface{}{"hasSigningSecret": false, "signingSecret": nil}),
				&hookbasev1alpha1.WebhookDestination{Spec: hookbasev1alpha1.WebhookDestinationSpec{
					Name: "Bridge", Type: "bridge", Bridge: &bridge,
				}})
			h.sync()
			h.reconcile()

			create := h.api.find("POST", "/api/destinations")[0].Body
			if create["type"] != "bridge" {
				t.Errorf("create type = %v", create["type"])
			}
			if !reflect.DeepEqual(create["config"], tc.want) {
				t.Errorf("create config = %v, want %v", create["config"], tc.want)
			}
			for _, absent := range []string{"url", "authType", "headers", "useStaticIp"} {
				if _, ok := create[absent]; ok {
					t.Errorf("a bridge create must not carry %q: %v", absent, create)
				}
			}
			patch := h.api.find("PATCH", "/api/destinations/dst_1")[0].Body
			if !reflect.DeepEqual(patch["config"], tc.want) {
				t.Errorf("update config = %v, want %v", patch["config"], tc.want)
			}
			if h.secret("relay-signing-secret") != nil {
				t.Error("a bridge has no signing secret to store")
			}
		})
	}
}

func TestDestinationBridgeWithoutBridgeSpecFails(t *testing.T) {
	h := newDestinationHarness(t, apiDestination("bridge", nil),
		&hookbasev1alpha1.WebhookDestination{Spec: hookbasev1alpha1.WebhookDestinationSpec{Name: "Bridge", Type: "bridge"}})
	h.reconcile()
	got := h.reconcile()

	if got.Status.DestinationID != "" || len(h.api.find("POST", "/api/destinations")) != 0 {
		t.Fatal("a bridge without spec.bridge must not be created")
	}
	if c := readyCondition(t, got.Status.Conditions); c.Status != metav1.ConditionFalse || c.Reason != "InvalidSpec" {
		t.Fatalf("Ready = %s/%s", c.Status, c.Reason)
	}
}
