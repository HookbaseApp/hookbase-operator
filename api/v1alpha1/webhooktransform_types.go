package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// WebhookTransformSpec defines the desired state of a webhook transform.
type WebhookTransformSpec struct {
	// Name of the transform in Hookbase.
	Name string `json:"name"`

	// Slug is the URL-friendly identifier. Immutable after creation.
	// +optional
	Slug string `json:"slug,omitempty"`

	// Description of the transform.
	// +optional
	Description string `json:"description,omitempty"`

	// TransformType is the type of transformation (jsonata, javascript, mapping).
	// +kubebuilder:validation:Enum=jsonata;javascript;mapping
	TransformType string `json:"transformType"`

	// Code is the inline transform code.
	// Mutually exclusive with CodeFrom.
	// +optional
	Code string `json:"code,omitempty"`

	// CodeFrom references a ConfigMap containing the transform code.
	// Mutually exclusive with Code.
	// +optional
	CodeFrom *ConfigMapKeyRef `json:"codeFrom,omitempty"`

	// InputFormat is the expected input format (json, xml, form, text).
	// +kubebuilder:validation:Enum=json;xml;form;text
	// +kubebuilder:default=json
	// +optional
	InputFormat string `json:"inputFormat,omitempty"`

	// OutputFormat is the output format (json, xml, form, text).
	// +kubebuilder:validation:Enum=json;xml;form;text
	// +kubebuilder:default=json
	// +optional
	OutputFormat string `json:"outputFormat,omitempty"`

	// APIKeyRef references a HookbaseAPIKey or Secret for authentication.
	// +optional
	APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`
}

// WebhookTransformStatus defines the observed state of a webhook transform.
type WebhookTransformStatus struct {
	// TransformID is the Hookbase API resource ID.
	TransformID string `json:"transformId,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the last time the resource was synced.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Transform ID",type=string,JSONPath=`.status.transformId`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.transformType`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WebhookTransform is the Schema for the webhooktransforms API.
type WebhookTransform struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookTransformSpec   `json:"spec,omitempty"`
	Status WebhookTransformStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookTransformList contains a list of WebhookTransform.
type WebhookTransformList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookTransform `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookTransform{}, &WebhookTransformList{})
}
