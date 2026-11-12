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
