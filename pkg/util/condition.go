/*
Copyright 2022 The Kruise Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"github.com/openkruise/rollouts/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewRolloutCondition creates a new rollout condition.
func NewRolloutCondition(condType v1beta1.RolloutConditionType, status corev1.ConditionStatus, reason, message string) *v1beta1.RolloutCondition {
	return &v1beta1.RolloutCondition{
		Type:               condType,
		Status:             status,
		LastUpdateTime:     metav1.Now(),
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// GetRolloutCondition returns the condition with the provided type.
func GetRolloutCondition(status v1beta1.RolloutStatus, condType v1beta1.RolloutConditionType) *v1beta1.RolloutCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

func GetBatchReleaseCondition(status v1beta1.BatchReleaseStatus, condType v1beta1.RolloutConditionType) *v1beta1.RolloutCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

// SetRolloutCondition updates the rollout to include the provided condition. If the condition that
// we are about to add already exists and has the same status and reason, then we are not going to update
// by returning false. Returns true if the condition was updated
func SetRolloutCondition(status *v1beta1.RolloutStatus, condition v1beta1.RolloutCondition) bool {
	currentCond := GetRolloutCondition(*status, condition.Type)
	if currentCond != nil && currentCond.Status == condition.Status && currentCond.Reason == condition.Reason &&
		currentCond.Message == condition.Message {
		return false
	}
	// Do not update lastTransitionTime if the status of the condition doesn't change.
	if currentCond != nil && currentCond.Status == condition.Status {
		condition.LastTransitionTime = currentCond.LastTransitionTime
	}
	newConditions := filterOutCondition(status.Conditions, condition.Type)
	status.Conditions = append(newConditions, condition)
	return true
}

func SetBatchReleaseCondition(status *v1beta1.BatchReleaseStatus, condition v1beta1.RolloutCondition) bool {
	currentCond := GetBatchReleaseCondition(*status, condition.Type)
	if currentCond != nil && currentCond.Status == condition.Status && currentCond.Reason == condition.Reason &&
		currentCond.Message == condition.Message {
		return false
	}
	// Do not update lastTransitionTime if the status of the condition doesn't change.
	if currentCond != nil && currentCond.Status == condition.Status {
		condition.LastTransitionTime = currentCond.LastTransitionTime
	}
	newConditions := filterOutCondition(status.Conditions, condition.Type)
	status.Conditions = append(newConditions, condition)
	return true
}

// filterOutCondition returns a new slice of rollout conditions without conditions with the provided type.
func filterOutCondition(conditions []v1beta1.RolloutCondition, condType v1beta1.RolloutConditionType) []v1beta1.RolloutCondition {
	var newConditions []v1beta1.RolloutCondition
	for _, c := range conditions {
		if c.Type == condType {
			continue
		}
		newConditions = append(newConditions, c)
	}
	return newConditions
}

func RemoveRolloutCondition(status *v1beta1.RolloutStatus, condType v1beta1.RolloutConditionType) {
	status.Conditions = filterOutCondition(status.Conditions, condType)
}

func RemoveBatchReleaseCondition(status *v1beta1.BatchReleaseStatus, condType v1beta1.RolloutConditionType) {
	status.Conditions = filterOutCondition(status.Conditions, condType)
}
