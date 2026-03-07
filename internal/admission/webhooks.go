// Package admission provides validating and defaulting admission webhooks.
package admission

import (
	"context"
	"fmt"
	"net"
	"net/http"

	hookbasev1alpha1 "github.com/hookbase/hookbase-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-hookbase-io-v1alpha1,mutating=false,failurePolicy=fail,sideEffects=None,groups=hookbase.io,resources=webhooksources;webhookdestinations;webhooktunnels;webhooktransforms;webhookfilters;webhookschemas,verbs=create;update,versions=v1alpha1,name=vhookbase.kb.io,admissionReviewVersions=v1

// HookbaseValidator validates Hookbase CRDs.
type HookbaseValidator struct {
	decoder admission.Decoder
}

// NewHookbaseValidator creates a new validator with the given scheme.
func NewHookbaseValidator(scheme *runtime.Scheme) *HookbaseValidator {
	return &HookbaseValidator{
		decoder: admission.NewDecoder(scheme),
	}
}

// Handle validates incoming requests.
func (v *HookbaseValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	switch req.Kind.Kind {
	case "WebhookSource":
		return v.validateWebhookSource(ctx, req)
	case "WebhookDestination":
		return v.validateWebhookDestination(ctx, req)
	case "WebhookRoute":
		return v.validateWebhookRoute(ctx, req)
	default:
		return admission.Allowed("")
	}
}

func (v *HookbaseValidator) validateWebhookSource(ctx context.Context, req admission.Request) admission.Response {
	source := &hookbasev1alpha1.WebhookSource{}
	if err := v.decoder.Decode(req, source); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Slug immutability check on UPDATE
	if req.OldObject.Raw != nil {
		oldSource := &hookbasev1alpha1.WebhookSource{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldSource); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if oldSource.Spec.Slug != "" && source.Spec.Slug != oldSource.Spec.Slug {
			return admission.Denied("spec.slug is immutable after creation")
		}
	}

	// Validate CIDR ranges
	for _, cidr := range source.Spec.IPAllowlist {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return admission.Denied(fmt.Sprintf("invalid CIDR in ipAllowlist: %q: %v", cidr, err))
		}
	}
	for _, cidr := range source.Spec.IPDenylist {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return admission.Denied(fmt.Sprintf("invalid CIDR in ipDenylist: %q: %v", cidr, err))
		}
	}

	return admission.Allowed("")
}

func (v *HookbaseValidator) validateWebhookDestination(ctx context.Context, req admission.Request) admission.Response {
	dest := &hookbasev1alpha1.WebhookDestination{}
	if err := v.decoder.Decode(req, dest); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Slug immutability check on UPDATE
	if req.OldObject.Raw != nil {
		oldDest := &hookbasev1alpha1.WebhookDestination{}
		if err := v.decoder.DecodeRaw(req.OldObject, oldDest); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if oldDest.Spec.Slug != "" && dest.Spec.Slug != oldDest.Spec.Slug {
			return admission.Denied("spec.slug is immutable after creation")
		}
	}

	destType := dest.Spec.Type
	if destType == "" {
		destType = "http"
	}

	switch destType {
	case "http":
		if dest.Spec.URL == "" {
			return admission.Denied("spec.url is required for HTTP destinations")
		}
	case "s3", "r2", "gcs", "azure_blob":
		if dest.Spec.ConfigSecretRef == nil {
			return admission.Denied(fmt.Sprintf("spec.configSecretRef is required for %s destinations", destType))
		}
	default:
		return admission.Denied(fmt.Sprintf("unsupported destination type: %q", destType))
	}

	return admission.Allowed("")
}

func (v *HookbaseValidator) validateWebhookRoute(ctx context.Context, req admission.Request) admission.Response {
	route := &hookbasev1alpha1.WebhookRoute{}
	if err := v.decoder.Decode(req, route); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// filterRef and filterConditions are mutually exclusive
	if route.Spec.FilterRef != "" && len(route.Spec.FilterConditions) > 0 {
		return admission.Denied("spec.filterRef and spec.filterConditions are mutually exclusive")
	}

	return admission.Allowed("")
}

// +kubebuilder:webhook:path=/mutate-hookbase-io-v1alpha1,mutating=true,failurePolicy=fail,sideEffects=None,groups=hookbase.io,resources=webhooksources;webhookdestinations;webhookroutes;hookbasecronjobs,verbs=create,versions=v1alpha1,name=mhookbase.kb.io,admissionReviewVersions=v1

// HookbaseDefaulter defaults Hookbase CRDs.
type HookbaseDefaulter struct {
	decoder admission.Decoder
}

// NewHookbaseDefaulter creates a new defaulter with the given scheme.
func NewHookbaseDefaulter(scheme *runtime.Scheme) *HookbaseDefaulter {
	return &HookbaseDefaulter{
		decoder: admission.NewDecoder(scheme),
	}
}

// Handle applies defaults to incoming create requests.
func (d *HookbaseDefaulter) Handle(ctx context.Context, req admission.Request) admission.Response {
	// Defaulting is handled by kubebuilder +kubebuilder:default markers on the types.
	// This webhook is a placeholder for future complex defaulting logic.
	return admission.Allowed("")
}
