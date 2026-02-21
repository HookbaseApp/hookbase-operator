package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// WebhookDestinationSpec defines the desired state of a webhook destination.
type WebhookDestinationSpec struct {
	// Name of the destination in Hookbase.
	Name string `json:"name"`

	// Slug is the URL-friendly identifier. Immutable after creation.
	// +optional
	Slug string `json:"slug,omitempty"`

	// Description of the destination.
	// +optional
	Description string `json:"description,omitempty"`

	// URL is the endpoint to deliver webhooks to.
	URL string `json:"url"`

	// Method is the HTTP method for delivery.
	// +kubebuilder:default=POST
	// +kubebuilder:validation:Enum=GET;POST;PUT;PATCH;DELETE
	// +optional
	Method string `json:"method,omitempty"`

	// Headers are additional headers to include in deliveries.
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// AuthType is the authentication type (none, basic, bearer, api_key, custom_header).
	// +kubebuilder:validation:Enum=none;basic;bearer;api_key;custom_header
	// +optional
	AuthType string `json:"authType,omitempty"`

	// AuthSecretRef references a K8s Secret containing auth credentials.
	// For bearer: key "token". For basic: keys "username" and "password".
	// For api_key: keys "headerName" and "headerValue".
	// +optional
	AuthSecretRef *SecretKeyRef `json:"authSecretRef,omitempty"`

	// TimeoutMs is the delivery timeout in milliseconds.
	// +optional
	TimeoutMs *int `json:"timeoutMs,omitempty"`

	// RetryCount is the number of retries on failure.
	// +optional
	RetryCount *int `json:"retryCount,omitempty"`

	// RateLimitPerMinute sets the per-minute rate limit.
	// +optional
	RateLimitPerMinute *int `json:"rateLimitPerMinute,omitempty"`

	// APIKeyRef references a HookbaseAPIKey or Secret for authentication.
	// +optional
	APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`
}

// WebhookDestinationStatus defines the observed state of a webhook destination.
type WebhookDestinationStatus struct {
	// DestinationID is the Hookbase API resource ID.
	DestinationID string `json:"destinationId,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the last time the resource was synced.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Destination ID",type=string,JSONPath=`.status.destinationId`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WebhookDestination is the Schema for the webhookdestinations API.
type WebhookDestination struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookDestinationSpec   `json:"spec,omitempty"`
	Status WebhookDestinationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookDestinationList contains a list of WebhookDestination.
type WebhookDestinationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookDestination `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookDestination{}, &WebhookDestinationList{})
}
