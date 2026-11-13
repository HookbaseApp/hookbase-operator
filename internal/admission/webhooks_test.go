package admission

import (
	"context"
	"encoding/json"
	"testing"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func newValidator(t *testing.T) *HookbaseValidator {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := hookbasev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return NewHookbaseValidator(scheme)
}

func request(t *testing.T, kind string, obj, old runtime.Object) admission.Request {
	t.Helper()
	raw := func(o runtime.Object) runtime.RawExtension {
		if o == nil {
			return runtime.RawExtension{}
		}
		o.GetObjectKind().SetGroupVersionKind(hookbasev1alpha1.GroupVersion.WithKind(kind))
		b, err := json.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		return runtime.RawExtension{Raw: b}
	}
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Kind:      metav1.GroupVersionKind{Group: "hookbase.io", Version: "v1alpha1", Kind: kind},
		Object:    raw(obj),
		OldObject: raw(old),
	}}
}

func TestDestinationTypes(t *testing.T) {
	cfg := &hookbasev1alpha1.SecretKeyRef{Name: "cfg"}
	bridge := &hookbasev1alpha1.BridgeSpec{EventTypeSource: "fixed", EventType: "order.created"}
	for _, tc := range []struct {
		name    string
		spec    hookbasev1alpha1.WebhookDestinationSpec
		allowed bool
	}{
		{"http", hookbasev1alpha1.WebhookDestinationSpec{URL: "https://x.test"}, true},
		{"http without url", hookbasev1alpha1.WebhookDestinationSpec{}, false},
		{"sqs", hookbasev1alpha1.WebhookDestinationSpec{Type: "sqs", ConfigSecretRef: cfg}, true},
		{"pubsub without config", hookbasev1alpha1.WebhookDestinationSpec{Type: "pubsub"}, false},
		{"oci_queue", hookbasev1alpha1.WebhookDestinationSpec{Type: "oci_queue", ConfigSecretRef: cfg}, true},
		{"azure_blob", hookbasev1alpha1.WebhookDestinationSpec{Type: "azure_blob", ConfigSecretRef: cfg}, true},
		{"bridge", hookbasev1alpha1.WebhookDestinationSpec{Type: "bridge", Bridge: bridge}, true},
		{"bridge without spec.bridge", hookbasev1alpha1.WebhookDestinationSpec{Type: "bridge"}, false},
		{"bridge with url", hookbasev1alpha1.WebhookDestinationSpec{Type: "bridge", Bridge: bridge, URL: "https://x.test"}, false},
		{"bridge with auth", hookbasev1alpha1.WebhookDestinationSpec{Type: "bridge", Bridge: bridge, AuthType: "bearer"}, false},
		{"bridge with authType none", hookbasev1alpha1.WebhookDestinationSpec{Type: "bridge", Bridge: bridge, AuthType: "none"}, true},
		{"unknown", hookbasev1alpha1.WebhookDestinationSpec{Type: "snowflake", ConfigSecretRef: cfg}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.Name = "d"
			dest := &hookbasev1alpha1.WebhookDestination{Spec: tc.spec}
			resp := newValidator(t).Handle(context.Background(), request(t, "WebhookDestination", dest, nil))
			if resp.Allowed != tc.allowed {
				t.Fatalf("allowed = %v, want %v (%v)", resp.Allowed, tc.allowed, resp.Result)
			}
		})
	}
}

func TestBridgeEventTypePairing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bridge hookbasev1alpha1.BridgeSpec
		valid  bool
	}{
		{"fixed", hookbasev1alpha1.BridgeSpec{EventTypeSource: "fixed", EventType: "a.b"}, true},
		{"fixed without eventType", hookbasev1alpha1.BridgeSpec{EventTypeSource: "fixed"}, false},
		{"fixed with field", hookbasev1alpha1.BridgeSpec{EventTypeSource: "fixed", EventType: "a.b", EventTypeField: "type"}, false},
		{"fixed with prefix", hookbasev1alpha1.BridgeSpec{EventTypeSource: "fixed", EventType: "a.b", EventTypePrefix: "x."}, false},
		{"field", hookbasev1alpha1.BridgeSpec{EventTypeSource: "field", EventTypeField: "data.type", EventTypePrefix: "x."}, true},
		{"field without eventTypeField", hookbasev1alpha1.BridgeSpec{EventTypeSource: "field"}, false},
		{"field with eventType", hookbasev1alpha1.BridgeSpec{EventTypeSource: "field", EventTypeField: "type", EventType: "a.b"}, false},
		{"no source", hookbasev1alpha1.BridgeSpec{}, false},
	} {
		bridge := tc.bridge
		msg := validateBridgeDestination(&hookbasev1alpha1.WebhookDestinationSpec{Type: "bridge", Bridge: &bridge})
		if (msg == "") != tc.valid {
			t.Errorf("%s: valid = %v, want %v (%q)", tc.name, msg == "", tc.valid, msg)
		}
	}
}

func TestSourceCatalogProviderImmutable(t *testing.T) {
	source := func(catalog string) *hookbasev1alpha1.WebhookSource {
		return &hookbasev1alpha1.WebhookSource{Spec: hookbasev1alpha1.WebhookSourceSpec{Name: "s", CatalogProvider: catalog}}
	}
	for _, tc := range []struct {
		name     string
		old, new string
		allowed  bool
	}{
		{"unchanged", "stripe", "stripe", true},
		{"unset both", "", "", true},
		{"changed", "stripe", "github", false},
		{"added", "", "stripe", false},
		{"removed", "stripe", "", false},
	} {
		resp := newValidator(t).Handle(context.Background(), request(t, "WebhookSource", source(tc.new), source(tc.old)))
		if resp.Allowed != tc.allowed {
			t.Errorf("%s: allowed = %v, want %v", tc.name, resp.Allowed, tc.allowed)
		}
	}
	if resp := newValidator(t).Handle(context.Background(), request(t, "WebhookSource", source("stripe"), nil)); !resp.Allowed {
		t.Errorf("create with catalogProvider should be allowed: %v", resp.Result)
	}
}
