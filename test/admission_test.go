package test

import (
	"context"
	"encoding/json"
	"testing"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"github.com/hookbase/hookbase-operator/internal/admission"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	webhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	hookbasev1alpha1.SchemeBuilder.AddToScheme(scheme)
	return scheme
}

func makeAdmissionRequest(t *testing.T, kind string, obj interface{}, oldObj interface{}) webhookadmission.Request {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("failed to marshal object: %v", err)
	}

	req := webhookadmission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "hookbase.io",
				Version: "v1alpha1",
				Kind:    kind,
			},
			Object: runtime.RawExtension{Raw: raw},
		},
	}

	if oldObj != nil {
		oldRaw, err := json.Marshal(oldObj)
		if err != nil {
			t.Fatalf("failed to marshal old object: %v", err)
		}
		req.OldObject = runtime.RawExtension{Raw: oldRaw}
		req.Operation = admissionv1.Update
	} else {
		req.Operation = admissionv1.Create
	}

	return req
}

func TestValidateWebhookSourceCreate(t *testing.T) {
	validator := admission.NewHookbaseValidator(testScheme())

	source := &hookbasev1alpha1.WebhookSource{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hookbase.io/v1alpha1",
			Kind:       "WebhookSource",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-source",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookSourceSpec{
			Name:     "Test Source",
			Slug:     "test-source",
			Provider: "github",
		},
	}

	req := makeAdmissionRequest(t, "WebhookSource", source, nil)
	resp := validator.Handle(context.Background(), req)

	if !resp.Allowed {
		t.Errorf("expected create to be allowed, got denied: %s", resp.Result.Message)
	}
}

func TestValidateWebhookSourceSlugImmutability(t *testing.T) {
	validator := admission.NewHookbaseValidator(testScheme())

	oldSource := &hookbasev1alpha1.WebhookSource{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hookbase.io/v1alpha1",
			Kind:       "WebhookSource",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-source",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookSourceSpec{
			Name:     "Test Source",
			Slug:     "original-slug",
			Provider: "github",
		},
	}

	newSource := oldSource.DeepCopy()
	newSource.Spec.Slug = "changed-slug"

	req := makeAdmissionRequest(t, "WebhookSource", newSource, oldSource)
	resp := validator.Handle(context.Background(), req)

	if resp.Allowed {
		t.Error("expected slug change to be denied")
	}
	if resp.Result == nil || resp.Result.Message != "spec.slug is immutable after creation" {
		t.Errorf("unexpected denial message: %v", resp.Result)
	}
}

func TestValidateWebhookSourceSlugSameAllowed(t *testing.T) {
	validator := admission.NewHookbaseValidator(testScheme())

	source := &hookbasev1alpha1.WebhookSource{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hookbase.io/v1alpha1",
			Kind:       "WebhookSource",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-source",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookSourceSpec{
			Name:     "Test Source",
			Slug:     "same-slug",
			Provider: "github",
		},
	}

	updatedSource := source.DeepCopy()
	updatedSource.Spec.Name = "Updated Name" // name change is fine

	req := makeAdmissionRequest(t, "WebhookSource", updatedSource, source)
	resp := validator.Handle(context.Background(), req)

	if !resp.Allowed {
		t.Errorf("expected update with same slug to be allowed, got denied: %s", resp.Result.Message)
	}
}

func TestValidateWebhookSourceCIDRValid(t *testing.T) {
	validator := admission.NewHookbaseValidator(testScheme())

	source := &hookbasev1alpha1.WebhookSource{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hookbase.io/v1alpha1",
			Kind:       "WebhookSource",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-source",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookSourceSpec{
			Name:        "Test Source",
			Provider:    "github",
			IPAllowlist: []string{"192.168.1.0/24", "10.0.0.0/8"},
			IPDenylist:  []string{"172.16.0.0/12"},
		},
	}

	req := makeAdmissionRequest(t, "WebhookSource", source, nil)
	resp := validator.Handle(context.Background(), req)

	if !resp.Allowed {
		t.Errorf("expected valid CIDRs to be allowed, got denied: %s", resp.Result.Message)
	}
}

func TestValidateWebhookSourceCIDRInvalid(t *testing.T) {
	validator := admission.NewHookbaseValidator(testScheme())

	tests := []struct {
		name        string
		allowlist   []string
		denylist    []string
		expectDeny  bool
		denyContain string
	}{
		{
			name:        "invalid allowlist CIDR",
			allowlist:   []string{"not-a-cidr"},
			expectDeny:  true,
			denyContain: "ipAllowlist",
		},
		{
			name:        "invalid denylist CIDR",
			denylist:    []string{"999.999.999.999/99"},
			expectDeny:  true,
			denyContain: "ipDenylist",
		},
		{
			name:       "valid allowlist no denylist",
			allowlist:  []string{"10.0.0.0/8"},
			expectDeny: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &hookbasev1alpha1.WebhookSource{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "hookbase.io/v1alpha1",
					Kind:       "WebhookSource",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: hookbasev1alpha1.WebhookSourceSpec{
					Name:        "Test",
					Provider:    "generic",
					IPAllowlist: tt.allowlist,
					IPDenylist:  tt.denylist,
				},
			}

			req := makeAdmissionRequest(t, "WebhookSource", source, nil)
			resp := validator.Handle(context.Background(), req)

			if tt.expectDeny && resp.Allowed {
				t.Error("expected denial for invalid CIDR")
			}
			if !tt.expectDeny && !resp.Allowed {
				t.Errorf("expected allowed, got denied: %s", resp.Result.Message)
			}
		})
	}
}

func TestValidateWebhookRouteFilterMutualExclusion(t *testing.T) {
	validator := admission.NewHookbaseValidator(testScheme())

	// Both filterRef and filterConditions set — should be denied
	route := &hookbasev1alpha1.WebhookRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hookbase.io/v1alpha1",
			Kind:       "WebhookRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookRouteSpec{
			Name:           "Test Route",
			SourceRef:      "my-source",
			DestinationRef: "my-dest",
			FilterRef:      "my-filter",
			FilterConditions: []hookbasev1alpha1.FilterCondition{
				{Field: "headers.x-type", Operator: "eq", Value: "payment"},
			},
		},
	}

	req := makeAdmissionRequest(t, "WebhookRoute", route, nil)
	resp := validator.Handle(context.Background(), req)

	if resp.Allowed {
		t.Error("expected denial when both filterRef and filterConditions are set")
	}
}

func TestValidateWebhookRouteFilterRefOnly(t *testing.T) {
	validator := admission.NewHookbaseValidator(testScheme())

	route := &hookbasev1alpha1.WebhookRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hookbase.io/v1alpha1",
			Kind:       "WebhookRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookRouteSpec{
			Name:           "Test Route",
			SourceRef:      "my-source",
			DestinationRef: "my-dest",
			FilterRef:      "my-filter",
		},
	}

	req := makeAdmissionRequest(t, "WebhookRoute", route, nil)
	resp := validator.Handle(context.Background(), req)

	if !resp.Allowed {
		t.Errorf("expected filterRef only to be allowed, got denied: %v", resp.Result)
	}
}

func TestValidateWebhookRouteFilterConditionsOnly(t *testing.T) {
	validator := admission.NewHookbaseValidator(testScheme())

	route := &hookbasev1alpha1.WebhookRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hookbase.io/v1alpha1",
			Kind:       "WebhookRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "default",
		},
		Spec: hookbasev1alpha1.WebhookRouteSpec{
			Name:           "Test Route",
			SourceRef:      "my-source",
			DestinationRef: "my-dest",
			FilterConditions: []hookbasev1alpha1.FilterCondition{
				{Field: "body.type", Operator: "eq", Value: "payment.completed"},
			},
		},
	}

	req := makeAdmissionRequest(t, "WebhookRoute", route, nil)
	resp := validator.Handle(context.Background(), req)

	if !resp.Allowed {
		t.Errorf("expected filterConditions only to be allowed, got denied: %v", resp.Result)
	}
}

func TestValidateUnknownKindAllowed(t *testing.T) {
	validator := admission.NewHookbaseValidator(testScheme())

	// A kind that the validator doesn't specifically handle should be allowed
	req := webhookadmission.Request{
		AdmissionRequest: admissionv1.AdmissionRequest{
			Kind: metav1.GroupVersionKind{
				Group:   "hookbase.io",
				Version: "v1alpha1",
				Kind:    "WebhookTransform",
			},
			Object: runtime.RawExtension{Raw: []byte(`{}`)},
		},
	}

	resp := validator.Handle(context.Background(), req)
	if !resp.Allowed {
		t.Errorf("expected unknown kind to be allowed, got denied: %v", resp.Result)
	}
}
