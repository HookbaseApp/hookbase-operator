package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// WebhookSchemaSpec defines the desired state of a webhook schema.
type WebhookSchemaSpec struct {
	// Name of the schema in Hookbase.
	Name string `json:"name"`

	// Slug is the URL-friendly identifier. Immutable after creation.
	// +optional
	Slug string `json:"slug,omitempty"`

	// Description of the schema.
	// +optional
	Description string `json:"description,omitempty"`

	// JSONSchema is the inline JSON schema for validation.
	// Mutually exclusive with JSONSchemaFrom.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	JSONSchema *runtime.RawExtension `json:"jsonSchema,omitempty"`

	// JSONSchemaFrom references a ConfigMap containing the JSON schema.
	// Mutually exclusive with JSONSchema.
	// +optional
	JSONSchemaFrom *ConfigMapKeyRef `json:"jsonSchemaFrom,omitempty"`

	// APIKeyRef references a HookbaseAPIKey or Secret for authentication.
	// +optional
	APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`
}

// WebhookSchemaStatus defines the observed state of a webhook schema.
type WebhookSchemaStatus struct {
	// SchemaID is the Hookbase API resource ID.
	SchemaID string `json:"schemaId,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the last time the resource was synced.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Schema ID",type=string,JSONPath=`.status.schemaId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WebhookSchema is the Schema for the webhookschemas API.
type WebhookSchema struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookSchemaSpec   `json:"spec,omitempty"`
	Status WebhookSchemaStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookSchemaList contains a list of WebhookSchema.
type WebhookSchemaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookSchema `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookSchema{}, &WebhookSchemaList{})
}
