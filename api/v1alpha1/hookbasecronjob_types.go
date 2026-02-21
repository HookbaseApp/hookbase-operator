package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// HookbaseCronJobSpec defines the desired state of a Hookbase cron job.
type HookbaseCronJobSpec struct {
	// Name of the cron job in Hookbase.
	Name string `json:"name"`

	// Description of the cron job.
	// +optional
	Description string `json:"description,omitempty"`

	// CronExpression is the cron schedule expression.
	CronExpression string `json:"cronExpression"`

	// URL is the endpoint to call on schedule.
	URL string `json:"url"`

	// Method is the HTTP method for the request.
	// +kubebuilder:default=POST
	// +kubebuilder:validation:Enum=GET;POST;PUT;PATCH;DELETE
	// +optional
	Method string `json:"method,omitempty"`

	// Headers are additional headers for the request.
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// Payload is the inline request body.
	// Mutually exclusive with PayloadFrom.
	// +optional
	Payload string `json:"payload,omitempty"`

	// PayloadFrom references a ConfigMap containing the request body.
	// Mutually exclusive with Payload.
	// +optional
	PayloadFrom *ConfigMapKeyRef `json:"payloadFrom,omitempty"`

	// Timezone for cron schedule evaluation.
	// +kubebuilder:default=UTC
	// +optional
	Timezone string `json:"timezone,omitempty"`

	// TimeoutMs is the request timeout in milliseconds.
	// +optional
	TimeoutMs *int `json:"timeoutMs,omitempty"`

	// IsActive enables or disables the cron job.
	// +kubebuilder:default=true
	// +optional
	IsActive *bool `json:"isActive,omitempty"`

	// Notifications configures notifications for this cron job.
	// +optional
	Notifications *NotificationConfig `json:"notifications,omitempty"`

	// APIKeyRef references a HookbaseAPIKey or Secret for authentication.
	// +optional
	APIKeyRef *SecretKeyRef `json:"apiKeyRef,omitempty"`
}

// HookbaseCronJobStatus defines the observed state of a Hookbase cron job.
type HookbaseCronJobStatus struct {
	// CronJobID is the Hookbase API resource ID.
	CronJobID string `json:"cronJobId,omitempty"`

	// LastRunAt is the last time the cron job ran.
	LastRunAt string `json:"lastRunAt,omitempty"`

	// NextRunAt is the next scheduled run time.
	NextRunAt string `json:"nextRunAt,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime is the last time the resource was synced.
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="CronJob ID",type=string,JSONPath=`.status.cronJobId`
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.cronExpression`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HookbaseCronJob is the Schema for the hookbasecronjobs API.
type HookbaseCronJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HookbaseCronJobSpec   `json:"spec,omitempty"`
	Status HookbaseCronJobStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HookbaseCronJobList contains a list of HookbaseCronJob.
type HookbaseCronJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HookbaseCronJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HookbaseCronJob{}, &HookbaseCronJobList{})
}
