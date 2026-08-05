package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// FieldMappingSpec defines a mapping between source and target fields for warehouse destinations.
type FieldMappingSpec struct {
	// Source is the source field path (e.g., "$.id").
	Source string `json:"source"`

	// Target is the target column name in the warehouse.
	Target string `json:"target"`

	// Type is the data type (string, number, boolean, timestamp, json).
	// +kubebuilder:validation:Enum=string;number;boolean;timestamp;json
	Type string `json:"type"`

	// Default is the default value if the source field is missing.
	// +optional
	Default string `json:"default,omitempty"`
}

// ThrottleSpec configures outbound delivery throttling for a destination.
// When Mode is "rate", RateLimit and RateUnit are required. When Mode is
// "concurrency", MaxConcurrency is required. QueueLimit is optional in both modes.
type ThrottleSpec struct {
	// Mode is the throttling strategy (off, rate, concurrency).
	// +kubebuilder:default=off
	// +kubebuilder:validation:Enum=off;rate;concurrency
	// +optional
	Mode string `json:"mode,omitempty"`

	// RateLimit is the number of deliveries allowed per RateUnit. Required when Mode is "rate".
	// +optional
	RateLimit *int `json:"rateLimit,omitempty"`

	// RateUnit is the time unit RateLimit applies to (second, minute, hour). Required when Mode is "rate".
	// +kubebuilder:validation:Enum=second;minute;hour
	// +optional
	RateUnit string `json:"rateUnit,omitempty"`

	// MaxConcurrency is the maximum number of concurrent deliveries. Required when Mode is "concurrency".
	// +optional
	MaxConcurrency *int `json:"maxConcurrency,omitempty"`

	// QueueLimit is the maximum number of deliveries to queue while throttled.
	// +optional
	QueueLimit *int `json:"queueLimit,omitempty"`
}

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

	// Type is the destination type.
	// +kubebuilder:default=http
	// +kubebuilder:validation:Enum=http;s3;r2;gcs;azure_blob
	// +optional
	Type string `json:"type,omitempty"`

	// URL is the endpoint to deliver webhooks to. Required for HTTP destinations.
	// +optional
	URL string `json:"url,omitempty"`

	// ConfigSecretRef references a K8s Secret containing warehouse configuration.
	// For S3: keys "bucket", "region", "accessKeyId", "secretAccessKey", "prefix" (optional).
	// For R2: keys "bucket", "endpoint" (S3 API URL), "accessKeyId", "secretAccessKey", "prefix" (optional).
	// For GCS: keys "bucket", "projectId", "credentials", "prefix" (optional).
	// For Azure Blob: keys "container", "storageAccount", "accessKey", "prefix" (optional).
	// +optional
	ConfigSecretRef *SecretKeyRef `json:"configSecretRef,omitempty"`

	// FieldMapping defines field mappings for warehouse destinations.
	// +optional
	FieldMapping []FieldMappingSpec `json:"fieldMapping,omitempty"`

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

	// Throttle configures outbound delivery throttling for this destination.
	// +optional
	Throttle *ThrottleSpec `json:"throttle,omitempty"`

	// UseStaticIP enables static IP delivery, routing webhooks through a dedicated
	// proxy with a fixed egress IP address. Available on Pro and Business plans.
	// Defaults to true on qualifying plans.
	// +optional
	UseStaticIP *bool `json:"useStaticIp,omitempty"`

	// BatchSize is the number of events to accumulate before flushing to
	// a warehouse destination. Only applicable to warehouse types (s3, r2, gcs, azure_blob).
	// +optional
	BatchSize *int `json:"batchSize,omitempty"`

	// BatchWindowSeconds is the maximum number of seconds to wait before
	// flushing a batch to a warehouse destination, even if BatchSize has not been reached.
	// +optional
	BatchWindowSeconds *int `json:"batchWindowSeconds,omitempty"`

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
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
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
