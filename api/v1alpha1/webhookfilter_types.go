package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// WebhookFilterSpec defines the desired state of a webhook filter.
type WebhookFilterSpec struct {
	// Name of the filter in Hookbase.
	Name string `json:"name"`

	// Slug is the URL-friendly identifier. Immutable after creation.
	// +optional
	Slug string `json:"slug,omitempty"`

	// Description of the filter.
	// +optional
	Description string `json:"description,omitempty"`

	// Conditions are the filter conditions to evaluate.
	Conditions []FilterCondition `json:"conditions"`

	// Logic is the logic for combining conditions (AND/OR).
	// +kubebuilder:validation:Enum=AND;OR
	// +kubebuilder:default=AND
	// +optional
	Logic string `json:"logic,omitempty"`

	// APIKeyRef references a HookbaseAPIKey or Secret for authentication.
	// +optional
	APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`
}

// WebhookFilterStatus defines the observed state of a webhook filter.
type WebhookFilterStatus struct {
	// FilterID is the Hookbase API resource ID.
	FilterID string `json:"filterId,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the last time the resource was synced.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Filter ID",type=string,JSONPath=`.status.filterId`
// +kubebuilder:printcolumn:name="Logic",type=string,JSONPath=`.spec.logic`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WebhookFilter is the Schema for the webhookfilters API.
type WebhookFilter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookFilterSpec   `json:"spec,omitempty"`
	Status WebhookFilterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookFilterList contains a list of WebhookFilter.
type WebhookFilterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookFilter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookFilter{}, &WebhookFilterList{})
}
