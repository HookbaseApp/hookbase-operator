package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// HookbaseConfigSpec defines the desired state of the cluster-scoped Hookbase configuration.
type HookbaseConfigSpec struct {
	// APIBaseURL is the Hookbase API base URL.
	// +kubebuilder:default="https://api.hookbase.app"
	APIBaseURL string `json:"apiBaseUrl,omitempty"`

	// DefaultBootstrapAPIKeySecretRef references a Secret containing the default
	// bootstrap API key used when CRDs don't specify their own apiKeyRef.
	DefaultBootstrapAPIKeySecretRef SecretKeyRef `json:"defaultBootstrapAPIKeySecretRef"`

	// DefaultAgentImage is the default container image for the tunnel agent sidecar.
	// +kubebuilder:default="ghcr.io/hookbaseapp/hookbase-agent:latest"
	// +optional
	DefaultAgentImage string `json:"defaultAgentImage,omitempty"`

	// NamespaceSelector limits which namespaces the operator watches.
	// An empty selector watches all namespaces.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// DefaultReconcileInterval is the default requeue interval for drift detection.
	// +kubebuilder:default="5m"
	// +optional
	DefaultReconcileInterval string `json:"defaultReconcileInterval,omitempty"`
}

// HookbaseConfigStatus defines the observed state of HookbaseConfig.
type HookbaseConfigStatus struct {
	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="API URL",type=string,JSONPath=`.spec.apiBaseUrl`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HookbaseConfig is the Schema for the cluster-scoped Hookbase configuration.
type HookbaseConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HookbaseConfigSpec   `json:"spec,omitempty"`
	Status HookbaseConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HookbaseConfigList contains a list of HookbaseConfig.
type HookbaseConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HookbaseConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HookbaseConfig{}, &HookbaseConfigList{})
}
