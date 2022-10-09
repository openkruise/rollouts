package v1alpha1

import "encoding/json"

// CanaryMessage record each changes of rollout-id in Rollout.Status.CanaryStatus.Message field.
// PaaS can use this message to judge whether pods would be upgraded when users publishing resources.
// For example, users may just modify Service instead of Workload, PaaS can use this message to distinguish such scene.
type CanaryMessage struct {
	// RolloutID indicate the corresponding to rollout-id recorded in this message
	RolloutID string `json:"rolloutID,omitempty"`
	// RevisionChanged is true if workload revision changed when rollout-id equals the above RolloutID.
	RevisionChanged bool `json:"revisionChanged,omitempty"`
}

// DecodeCanaryMessage decode message as CanaryMessage
func DecodeCanaryMessage(message string) *CanaryMessage {
	structured := &CanaryMessage{}
	_ = json.Unmarshal([]byte(message), structured)
	return structured
}

// EncodeCanaryMessage build a new message based on parameters
func EncodeCanaryMessage(revisionChanged bool, rolloutID string) string {
	structured := &CanaryMessage{
		RevisionChanged: revisionChanged,
		RolloutID:       rolloutID,
	}
	message, _ := json.Marshal(structured)
	return string(message)
}
