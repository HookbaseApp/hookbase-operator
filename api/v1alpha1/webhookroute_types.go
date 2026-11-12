package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// FilterCondition represents a single filter condition for inline filters.
type FilterCondition struct {
	// Field is the JSON path to evaluate.
	Field string `json:"field"`
	// Operator is the comparison operator.
	Operator string `json:"operator"`
	// Value is the value to compare against.
	Value string `json:"value"`
}

// CircuitBreakerConfig configures the circuit breaker for a route.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of failures before opening the circuit.
	// +optional
	FailureThreshold *int `json:"failureThreshold,omitempty"`
	// CooldownSeconds is how long the circuit stays open before probing.
	// +optional
	CooldownSeconds *int `json:"cooldownSeconds,omitempty"`
	// ProbeSuccessThreshold is how many probes must succeed to close the circuit.
	// +optional
	ProbeSuccessThreshold *int `json:"probeSuccessThreshold,omitempty"`
}

// NotificationConfig configures notifications for a route.
type NotificationConfig struct {
	// OnFailure sends notification on delivery failure.
	// +optional
	OnFailure *bool `json:"onFailure,omitempty"`
	// OnSuccess sends notification on delivery success.
	// +optional
	OnSuccess *bool `json:"onSuccess,omitempty"`
	// OnRecovery sends notification when route recovers.
	// +optional
	OnRecovery *bool `json:"onRecovery,omitempty"`
	// Emails is a comma-separated list of notification emails.
	// +optional
	Emails string `json:"emails,omitempty"`
}

// WebhookRouteSpec defines the desired state of a webhook route.
type WebhookRouteSpec struct {
	// Name of the route in Hookbase.
	Name string `json:"name"`

	// SourceRef references a WebhookSource in the same namespace by name.
	SourceRef string `json:"sourceRef"`

	// DestinationRef references a WebhookDestination in the same namespace by name.
	DestinationRef string `json:"destinationRef"`

	// FilterRef references a WebhookFilter in the same namespace by name.
	// Mutually exclusive with FilterConditions.
	// +optional
	FilterRef string `json:"filterRef,omitempty"`

	// FilterConditions defines inline filter conditions.
	// Mutually exclusive with FilterRef.
	// +optional
	FilterConditions []FilterCondition `json:"filterConditions,omitempty"`

	// FilterLogic is the logic for combining filter conditions (AND/OR).
	// +kubebuilder:validation:Enum=AND;OR
	// +kubebuilder:default=AND
	// +optional
	FilterLogic string `json:"filterLogic,omitempty"`

	// TransformRef references a WebhookTransform in the same namespace by name.
	// +optional
	TransformRef string `json:"transformRef,omitempty"`

	// SchemaRef references a WebhookSchema in the same namespace by name.
	// +optional
	SchemaRef string `json:"schemaRef,omitempty"`

	// Priority determines the order of route evaluation (higher = first).
	// +optional
	Priority *int `json:"priority,omitempty"`

	// IsActive enables or disables the route.
	// +kubebuilder:default=true
	// +optional
	IsActive *bool `json:"isActive,omitempty"`

	// FailoverDestinationRefs are names of WebhookDestinations for failover. Removing them all
	// removes the route's failover destinations in Hookbase.
	// +optional
	FailoverDestinationRefs []string `json:"failoverDestinationRefs,omitempty"`

	// HoldOnCircuitOpen holds deliveries while the route's circuit breaker is open and sends them
	// once it recovers. With it off, a delivery is dead-lettered as soon as it meets the open
	// circuit. Holds need a plan with circuit breakers. Sent only when set; the CRD defaults it to
	// true, which is also the API's default.
	// +kubebuilder:default=true
	// +optional
	HoldOnCircuitOpen *bool `json:"holdOnCircuitOpen,omitempty"`

	// CircuitBreaker configures the circuit breaker for this route.
	// +optional
	CircuitBreaker *CircuitBreakerConfig `json:"circuitBreaker,omitempty"`

	// Notifications configures notifications for this route.
	// +optional
	Notifications *NotificationConfig `json:"notifications,omitempty"`

	// APIKeyRef references a HookbaseAPIKey or Secret for authentication.
	// +optional
	APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`
}

// WebhookRouteStatus defines the observed state of a webhook route.
type WebhookRouteStatus struct {
	// RouteID is the Hookbase API resource ID.
	RouteID string `json:"routeId,omitempty"`

	// ResolvedSourceID is the API ID of the referenced source.
	ResolvedSourceID string `json:"resolvedSourceId,omitempty"`
	// ResolvedDestinationID is the API ID of the referenced destination.
	ResolvedDestinationID string `json:"resolvedDestinationId,omitempty"`
	// ResolvedFilterID is the API ID of the referenced filter.
	ResolvedFilterID string `json:"resolvedFilterId,omitempty"`
	// ResolvedTransformID is the API ID of the referenced transform.
	ResolvedTransformID string `json:"resolvedTransformId,omitempty"`
	// ResolvedSchemaID is the API ID of the referenced schema.
	ResolvedSchemaID string `json:"resolvedSchemaId,omitempty"`

	// CircuitState is the current circuit breaker state.
	CircuitState string `json:"circuitState,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the last time the resource was synced.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Route ID",type=string,JSONPath=`.status.routeId`
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.sourceRef`
// +kubebuilder:printcolumn:name="Destination",type=string,JSONPath=`.spec.destinationRef`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WebhookRoute is the Schema for the webhookroutes API.
type WebhookRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WebhookRouteSpec   `json:"spec,omitempty"`
	Status WebhookRouteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebhookRouteList contains a list of WebhookRoute.
type WebhookRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebhookRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WebhookRoute{}, &WebhookRouteList{})
}
