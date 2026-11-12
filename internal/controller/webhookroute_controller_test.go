package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/hookbase"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type routeHarness struct {
	t   *testing.T
	api *fakeAPI
	k8s client.Client
	rec *WebhookRouteReconciler
}

// newRouteHarness serves a route whose source "src" is src_1 and whose destinations "primary" and
// "backup" are dst_1 and dst_2; "unsynced" is a destination with no ID yet.
func newRouteHarness(t *testing.T, spec hookbasev1alpha1.WebhookRouteSpec) *routeHarness {
	t.Helper()
	api := &fakeAPI{handle: func(method, path string) (int, interface{}) {
		switch {
		case method == "POST" && path == "/api/routes":
			return http.StatusCreated, map[string]interface{}{"route": map[string]interface{}{"id": "rt_1", "name": "Route"}}
		case method == "PATCH" && path == "/api/routes/rt_1":
			return http.StatusOK, map[string]interface{}{"success": true}
		}
		return http.StatusNotFound, map[string]interface{}{"error": "not found"}
	}}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	meta := func(name string) metav1.ObjectMeta { return metav1.ObjectMeta{Name: name, Namespace: "default"} }
	src := &hookbasev1alpha1.WebhookSource{ObjectMeta: meta("src"), Status: hookbasev1alpha1.WebhookSourceStatus{SourceID: "src_1"}}
	primary := &hookbasev1alpha1.WebhookDestination{ObjectMeta: meta("primary"), Status: hookbasev1alpha1.WebhookDestinationStatus{DestinationID: "dst_1"}}
	backup := &hookbasev1alpha1.WebhookDestination{ObjectMeta: meta("backup"), Status: hookbasev1alpha1.WebhookDestinationStatus{DestinationID: "dst_2"}}
	unsynced := &hookbasev1alpha1.WebhookDestination{ObjectMeta: meta("unsynced")}
	if spec.Name == "" {
		spec.Name = "Route"
	}
	spec.SourceRef, spec.DestinationRef = "src", "primary"
	route := &hookbasev1alpha1.WebhookRoute{ObjectMeta: meta("route"), Spec: spec}

	scheme := testScheme(t)
	k8s := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&hookbasev1alpha1.WebhookRoute{}, &hookbasev1alpha1.WebhookSource{}, &hookbasev1alpha1.WebhookDestination{}).
		WithObjects(src, primary, backup, unsynced, route).
		Build()
	rec := &WebhookRouteReconciler{
		Client:        k8s,
		Scheme:        scheme,
		ClientManager: hookbase.NewClientManager(k8s, server.URL, "whr_bootstrap_test_key"),
	}
	return &routeHarness{t: t, api: api, k8s: k8s, rec: rec}
}

func (h *routeHarness) reconcile() *hookbasev1alpha1.WebhookRoute {
	h.t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "route"}}
	if _, err := h.rec.Reconcile(context.Background(), req); err != nil {
		h.t.Fatalf("reconcile: %v", err)
	}
	var got hookbasev1alpha1.WebhookRoute
	if err := h.k8s.Get(context.Background(), req.NamespacedName, &got); err != nil {
		h.t.Fatal(err)
	}
	return &got
}

func (h *routeHarness) sync() *hookbasev1alpha1.WebhookRoute {
	h.t.Helper()
	got := h.reconcile()
	if got.Status.RouteID == "" {
		got = h.reconcile()
	}
	if got.Status.RouteID == "" {
		h.t.Fatalf("route was not created; conditions: %+v", got.Status.Conditions)
	}
	return got
}

func (h *routeHarness) lastPatch() map[string]interface{} {
	h.t.Helper()
	patches := h.api.find("PATCH", "/api/routes/rt_1")
	if len(patches) == 0 {
		h.t.Fatal("expected an update")
	}
	return patches[len(patches)-1].Body
}

func TestRouteRemovingFailoverRefsClearsThemOnTheServer(t *testing.T) {
	h := newRouteHarness(t, hookbasev1alpha1.WebhookRouteSpec{FailoverDestinationRefs: []string{"backup"}})
	got := h.sync()

	create := h.api.find("POST", "/api/routes")[0].Body
	if ids, ok := create["failoverDestinationIds"].([]interface{}); !ok || len(ids) != 1 || ids[0] != "dst_2" {
		t.Fatalf("create failoverDestinationIds = %v", create["failoverDestinationIds"])
	}

	// Still named: the update restates the list.
	got = h.reconcile()
	if ids, ok := h.lastPatch()["failoverDestinationIds"].([]interface{}); !ok || len(ids) != 1 {
		t.Fatalf("update failoverDestinationIds = %v", h.lastPatch()["failoverDestinationIds"])
	}

	// Removed from the manifest: the update must send an explicit null, or the server keeps them.
	got.Spec.FailoverDestinationRefs = nil
	if err := h.k8s.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	value, ok := h.lastPatch()["failoverDestinationIds"]
	if !ok || value != nil {
		t.Fatalf("update should clear failoverDestinationIds with null, body: %v", h.lastPatch())
	}
}

func TestRouteUnsyncedFailoverRefDoesNotClearTheServerList(t *testing.T) {
	h := newRouteHarness(t, hookbasev1alpha1.WebhookRouteSpec{FailoverDestinationRefs: []string{"unsynced"}})
	h.sync()
	h.reconcile()

	if _, ok := h.lastPatch()["failoverDestinationIds"]; ok {
		t.Fatalf("a named but unsynced failover ref must leave the server's list alone, body: %v", h.lastPatch())
	}
}

func TestRouteHoldOnCircuitOpenSentOnlyWhenSet(t *testing.T) {
	h := newRouteHarness(t, hookbasev1alpha1.WebhookRouteSpec{})
	h.sync()
	got := h.reconcile()

	if _, ok := h.api.find("POST", "/api/routes")[0].Body["holdOnCircuitOpen"]; ok {
		t.Error("create should leave holdOnCircuitOpen to the API's default when unset")
	}
	if _, ok := h.lastPatch()["holdOnCircuitOpen"]; ok {
		t.Error("update should not send holdOnCircuitOpen when unset")
	}

	got.Spec.HoldOnCircuitOpen = boolPtr(false)
	if err := h.k8s.Update(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	h.reconcile()
	if v, ok := h.lastPatch()["holdOnCircuitOpen"]; !ok || v != false {
		t.Fatalf("update holdOnCircuitOpen = %v (sent: %v)", v, ok)
	}
}

func TestRouteHoldOnCircuitOpenSentOnCreate(t *testing.T) {
	h := newRouteHarness(t, hookbasev1alpha1.WebhookRouteSpec{HoldOnCircuitOpen: boolPtr(true)})
	h.sync()

	if v := h.api.find("POST", "/api/routes")[0].Body["holdOnCircuitOpen"]; v != true {
		t.Fatalf("create holdOnCircuitOpen = %v", v)
	}
}
