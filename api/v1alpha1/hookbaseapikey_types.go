package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// HookbaseAPIKeySpec defines the desired state of a Hookbase API key.
type HookbaseAPIKeySpec struct {
	// Name of the API key in Hookbase.
	Name string `json:"name"`

	// Scopes are the permissions for this API key.
	// +optional
	Scopes []string `json:"scopes,omitempty"`

	// ExpiresInDays is the number of days until the key expires. Defaults to never.
	//
	// Days, not seconds, is deliberate: this is the unit users write in their manifests, and it
	// matches expiresInDays on portal tokens. The API reads expiresIn in SECONDS, and the Go SDK
	// converts on the way out (CreateAPIKeyParams.MarshalJSON). Do not "correct" this field to
	// seconds -- that would silently change the meaning of every existing HookbaseAPIKey manifest.
	// +optional
	ExpiresInDays *int `json:"expiresInDays,omitempty"`

	// SecretRef defines where the raw API key will be stored.
	// The operator creates this Secret on key creation.
	SecretRef SecretKeyRef `json:"secretRef"`

	// BootstrapAPIKeySecretRef references a Secret containing the bootstrap API key
	// used to create this key. Falls back to HookbaseConfig default if not set.
	// +optional
	BootstrapAPIKeySecretRef *SecretKeyRef `json:"bootstrapAPIKeySecretRef,omitempty"`
}

// HookbaseAPIKeyStatus defines the observed state of a Hookbase API key.
type HookbaseAPIKeyStatus struct {
	// APIKeyID is the Hookbase API resource ID.
	APIKeyID string `json:"apiKeyId,omitempty"`

	// KeyPrefix is the visible prefix of the API key (e.g., "whr_abc...").
	KeyPrefix string `json:"keyPrefix,omitempty"`

	// ExpiresAt is when the key expires.
	ExpiresAt string `json:"expiresAt,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the last time the resource was synced.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Key ID",type=string,JSONPath=`.status.apiKeyId`
// +kubebuilder:printcolumn:name="Prefix",type=string,JSONPath=`.status.keyPrefix`
// +kubebuilder:printcolumn:name="Expires",type=string,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HookbaseAPIKey is the Schema for the hookbaseapikeys API.
type HookbaseAPIKey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HookbaseAPIKeySpec   `json:"spec,omitempty"`
	Status HookbaseAPIKeyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HookbaseAPIKeyList contains a list of HookbaseAPIKey.
type HookbaseAPIKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HookbaseAPIKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HookbaseAPIKey{}, &HookbaseAPIKeyList{})
}
