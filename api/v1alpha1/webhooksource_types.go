package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// IngestMethod is an HTTP method a source's ingest endpoint can accept. A named type so the enum
// lands on the array items in the generated CRD rather than on the array itself.
// +kubebuilder:validation:Enum=GET;POST;PUT;PATCH;DELETE;HEAD
type IngestMethod string

// WebhookSourceSpec defines the desired state of a webhook source.
//
// Field names follow the Hookbase API's source schema (POST/PATCH /api/sources), which rejects
// unknown keys with a 400.
type WebhookSourceSpec struct {
	// Name of the source in Hookbase.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	Name string `json:"name"`

	// Slug is the URL-friendly identifier that forms the ingest URL. Immutable after creation.
	// When omitted it is derived from name (lowercased, runs of other characters collapsed to "-").
	// +kubebuilder:validation:MaxLength=50
	// +kubebuilder:validation:Pattern=`^[a-z0-9-]+$`
	// +optional
	Slug string `json:"slug,omitempty"`

	// Provider is the webhook provider (generic, github, stripe, whatsapp, ...). It is an open
	// string: the Hookbase API validates it and reports an unknown provider as a 400, surfaced on
	// the Ready condition. Changes are sent to the API on update.
	// +kubebuilder:default=generic
	// +optional
	Provider string `json:"provider,omitempty"`

	// Description of the source.
	// +optional
	Description string `json:"description,omitempty"`

	// SigningSecretRef references a K8s Secret key (default key "signingSecret") holding the
	// signing secret. Its value is sent to the API on create, and again on update whenever the
	// Secret changes. The operator never writes to a referenced Secret. If not set, and the
	// provider has Hookbase generate a secret, the generated secret is stored in the
	// operator-owned Secret "<name>-signing-secret" under key "signingSecret".
	// +optional
	SigningSecretRef *SecretKeyRef `json:"signingSecretRef,omitempty"`

	// VerifyTokenRef references a K8s Secret key (default key "verifyToken") holding the Meta
	// verify token (whatsapp only; the API rejects it for any other provider). If not set for a
	// whatsapp source, the API-generated token is stored in the operator-owned Secret
	// "<name>-signing-secret" under key "verifyToken".
	// +optional
	VerifyTokenRef *SecretKeyRef `json:"verifyTokenRef,omitempty"`

	// RejectInvalidSignatures rejects events whose signature fails verification.
	// +optional
	RejectInvalidSignatures *bool `json:"rejectInvalidSignatures,omitempty"`

	// VerifySignature is the old name for rejectInvalidSignatures.
	// Deprecated: use rejectInvalidSignatures. Honoured only when rejectInvalidSignatures is unset.
	// +optional
	VerifySignature *bool `json:"verifySignature,omitempty"`

	// RateLimitPerMinute sets the per-minute rate limit for this source.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100000
	// +optional
	RateLimitPerMinute *int `json:"rateLimitPerMinute,omitempty"`

	// IPFilterMode controls IP filtering (none, allowlist, denylist, both).
	// +kubebuilder:validation:Enum=none;allowlist;denylist;both
	// +optional
	IPFilterMode string `json:"ipFilterMode,omitempty"`

	// IPAllowlist is a list of allowed CIDR ranges.
	// +optional
	IPAllowlist []string `json:"ipAllowlist,omitempty"`

	// IPDenylist is a list of denied CIDR ranges.
	// +optional
	IPDenylist []string `json:"ipDenylist,omitempty"`

	// DedupStrategy sets the deduplication strategy (auto, provider_id, payload_hash,
	// idempotency_key, none). The API defaults to auto.
	// +kubebuilder:validation:Enum=auto;provider_id;payload_hash;idempotency_key;none
	// +optional
	DedupStrategy string `json:"dedupStrategy,omitempty"`

	// DedupWindowHours sets the deduplication window in hours (1 to 168).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=168
	// +optional
	DedupWindowHours *int `json:"dedupWindowHours,omitempty"`

	// TransientMode means payloads are never stored at rest (HIPAA/GDPR).
	// +optional
	TransientMode *bool `json:"transientMode,omitempty"`

	// AllowedMethods restricts which HTTP methods the ingest endpoint accepts.
	// Omit or leave empty to accept any method. OPTIONS is excluded because CORS
	// preflight is answered before ingest runs.
	// +optional
	AllowedMethods []IngestMethod `json:"allowedMethods,omitempty"`

	// APIKeyRef references a HookbaseAPIKey or Secret for authentication.
	// +optional
	APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`
}

// WebhookSourceStatus defines the observed state of a webhook source.
type WebhookSourceStatus struct {
	// SourceID is the Hookbase API resource ID.
	SourceID string `json:"sourceId,omitempty"`

	// IngestURL is the public URL for receiving webhooks.
	IngestURL string `json:"ingestUrl,omitempty"`

	// Provider is the provider the API last reported for the source.
	// +optional
	Provider string `json:"provider,omitempty"`

	// HasSigningSecret reports whether the source has a signing secret set.
	// +optional
	HasSigningSecret bool `json:"hasSigningSecret,omitempty"`

	// VerifiesWithoutSecret reports that the provider verifies against a key Hookbase already
	// holds (wise, wise-sandbox, aws-sns), so no signing secret is expected.
	// +optional
	VerifiesWithoutSecret bool `json:"verifiesWithoutSecret,omitempty"`

	// AwaitingSigningSecret reports that the provider issues its secret only after the ingest URL
	// is registered with it (asana, notion) and none has been set yet.
	// +optional
	AwaitingSigningSecret bool `json:"awaitingSigningSecret,omitempty"`

	// SigningSecretRefVersion is the resourceVersion of the signingSecretRef Secret last sent to
	// the API, used to resend the secret only when it changes.
	// +optional
	SigningSecretRefVersion string `json:"signingSecretRefVersion,omitempty"`

	// VerifyTokenRefVersion is the resourceVersion of the verifyTokenRef Secret last sent to the
	// API, used to resend the token only when it changes.
	// +optional
	VerifyTokenRefVersion string `json:"verifyTokenRefVersion,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the last time the resource was synced.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Source ID",type=string,JSONPath=`.status.sourceId`
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.provider`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WebhookSource is the Schema for the webhooksources API.
type WebhookSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookSourceSpec   `json:"spec,omitempty"`
	Status WebhookSourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookSourceList contains a list of WebhookSource.
type WebhookSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookSource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookSource{}, &WebhookSourceList{})
}
