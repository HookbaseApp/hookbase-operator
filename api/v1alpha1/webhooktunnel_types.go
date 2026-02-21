package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SidecarInjectionConfig configures automatic sidecar injection into a target Deployment.
type SidecarInjectionConfig struct {
	// DeploymentRef is the name of the target Deployment.
	DeploymentRef string `json:"deploymentRef"`

	// AgentImage overrides the default agent container image.
	// +optional
	AgentImage string `json:"agentImage,omitempty"`

	// Resources overrides the default agent container resources.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
}

// WebhookTunnelSpec defines the desired state of a webhook tunnel.
type WebhookTunnelSpec struct {
	// Name of the tunnel in Hookbase.
	Name string `json:"name"`

	// Subdomain is the preferred tunnel subdomain. Immutable after creation.
	// +optional
	Subdomain string `json:"subdomain,omitempty"`

	// TargetPort is the local port to forward traffic to.
	TargetPort int `json:"targetPort"`

	// TargetService references a K8s Service to forward traffic to instead of localhost.
	// +optional
	TargetService string `json:"targetService,omitempty"`

	// SidecarInjection configures automatic agent sidecar injection.
	// +optional
	SidecarInjection *SidecarInjectionConfig `json:"sidecarInjection,omitempty"`

	// AuthTokenSecretRef references a Secret where the tunnel auth token will be stored.
	// +optional
	AuthTokenSecretRef *SecretKeyRef `json:"authTokenSecretRef,omitempty"`

	// APIKeyRef references a HookbaseAPIKey or Secret for authentication.
	// +optional
	APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`
}

// WebhookTunnelStatus defines the observed state of a webhook tunnel.
type WebhookTunnelStatus struct {
	// TunnelID is the Hookbase API resource ID.
	TunnelID string `json:"tunnelId,omitempty"`

	// PublicURL is the public URL of the tunnel.
	PublicURL string `json:"publicUrl,omitempty"`

	// ConnectionStatus is the current connection state (connected, disconnected).
	ConnectionStatus string `json:"connectionStatus,omitempty"`

	// SidecarInjected indicates whether the agent sidecar has been injected.
	SidecarInjected bool `json:"sidecarInjected,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the last time the resource was synced.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tunnel ID",type=string,JSONPath=`.status.tunnelId`
// +kubebuilder:printcolumn:name="Public URL",type=string,JSONPath=`.status.publicUrl`
// +kubebuilder:printcolumn:name="Connected",type=string,JSONPath=`.status.connectionStatus`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WebhookTunnel is the Schema for the webhooktunnels API.
type WebhookTunnel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookTunnelSpec   `json:"spec,omitempty"`
	Status WebhookTunnelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookTunnelList contains a list of WebhookTunnel.
type WebhookTunnelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookTunnel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookTunnel{}, &WebhookTunnelList{})
}
