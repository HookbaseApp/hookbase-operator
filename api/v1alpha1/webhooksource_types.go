package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// WebhookSourceSpec defines the desired state of a webhook source.
type WebhookSourceSpec struct {
	// Name of the source in Hookbase.
	Name string `json:"name"`

	// Slug is the URL-friendly identifier. Immutable after creation.
	// +optional
	Slug string `json:"slug,omitempty"`

	// Provider is the webhook provider type (generic, github, stripe, etc.).
	// +kubebuilder:default=generic
	// +optional
	Provider string `json:"provider,omitempty"`

	// Description of the source.
	// +optional
	Description string `json:"description,omitempty"`

	// SigningSecretRef references a K8s Secret containing the signing secret.
	// If not set, the operator will create one automatically on source creation.
	// +optional
	SigningSecretRef *SecretKeyRef `json:"signingSecretRef,omitempty"`

	// VerifySignature enables provider signature verification.
	// +optional
	VerifySignature *bool `json:"verifySignature,omitempty"`

	// RateLimitPerMinute sets the per-minute rate limit for this source.
	// +optional
	RateLimitPerMinute *int `json:"rateLimitPerMinute,omitempty"`

	// IPFilterMode controls IP filtering (none, allowlist, denylist).
	// +kubebuilder:validation:Enum=none;allowlist;denylist
	// +optional
	IPFilterMode string `json:"ipFilterMode,omitempty"`

	// IPAllowlist is a list of allowed CIDR ranges.
	// +optional
	IPAllowlist []string `json:"ipAllowlist,omitempty"`

	// IPDenylist is a list of denied CIDR ranges.
	// +optional
	IPDenylist []string `json:"ipDenylist,omitempty"`

	// DedupStrategy sets the deduplication strategy (none, header, payload_hash, event_id).
	// +kubebuilder:validation:Enum=none;header;payload_hash;event_id
	// +optional
	DedupStrategy string `json:"dedupStrategy,omitempty"`

	// DedupWindowHours sets the deduplication window in hours.
	// +optional
	DedupWindowHours *int `json:"dedupWindowHours,omitempty"`

	// TransientMode means payloads are never stored at rest (HIPAA/GDPR).
	// +optional
	TransientMode *bool `json:"transientMode,omitempty"`

	// AllowedMethods restricts which HTTP methods the ingest endpoint accepts.
	// Omit or leave empty to accept any method. OPTIONS is excluded because CORS
	// preflight is answered before ingest runs.
	// +kubebuilder:validation:Enum=GET;POST;PUT;PATCH;DELETE;HEAD
	// +optional
	AllowedMethods []string `json:"allowedMethods,omitempty"`

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
