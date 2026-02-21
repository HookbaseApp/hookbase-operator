package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// SecretKeyRef references a key in a Kubernetes Secret.
type SecretKeyRef struct {
	// Name of the Secret.
	Name string `json:"name"`
	// Key within the Secret.
	// +optional
	Key string `json:"key,omitempty"`
}

// ConfigMapKeyRef references a key in a ConfigMap.
type ConfigMapKeyRef struct {
	// Name of the ConfigMap.
	Name string `json:"name"`
	// Key within the ConfigMap.
	Key string `json:"key"`
}

// ConditionType is the type of a condition.
type ConditionType string

const (
	// ConditionReady indicates the resource is ready.
	ConditionReady ConditionType = "Ready"
	// ConditionSynced indicates the resource is synced with the Hookbase API.
	ConditionSynced ConditionType = "Synced"
	// ConditionAPIKeyValid indicates the API key is valid.
	ConditionAPIKeyValid ConditionType = "APIKeyValid"
)

// Finalizer is the finalizer used by all Hookbase CRDs.
const Finalizer = "hookbase.io/finalizer"

// SetCondition sets or updates a condition on the given conditions slice.
func SetCondition(conditions *[]metav1.Condition, conditionType ConditionType, status metav1.ConditionStatus, reason, message string, generation int64) {
	now := metav1.Now()
	for i, c := range *conditions {
		if c.Type == string(conditionType) {
			if c.Status != status || c.Reason != reason || c.Message != message {
				(*conditions)[i].Status = status
				(*conditions)[i].Reason = reason
				(*conditions)[i].Message = message
				(*conditions)[i].LastTransitionTime = now
				(*conditions)[i].ObservedGeneration = generation
			}
			return
		}
	}
	*conditions = append(*conditions, metav1.Condition{
		Type:               string(conditionType),
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: generation,
	})
}
