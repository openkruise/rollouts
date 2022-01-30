/*
Copyright 2021.

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
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewRolloutCondition creates a new rollout condition.
func NewRolloutCondition(condType appsv1alpha1.RolloutConditionType, status corev1.ConditionStatus, reason, message string) appsv1alpha1.RolloutCondition {
	return appsv1alpha1.RolloutCondition{
		Type:               condType,
		Status:             status,
		LastUpdateTime:     metav1.Now(),
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// GetRolloutCondition returns the condition with the provided type.
func GetRolloutCondition(status appsv1alpha1.RolloutStatus, condType appsv1alpha1.RolloutConditionType) *appsv1alpha1.RolloutCondition {
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
func SetRolloutCondition(status *appsv1alpha1.RolloutStatus, condition appsv1alpha1.RolloutCondition) bool {
	currentCond := GetRolloutCondition(*status, condition.Type)
	if currentCond != nil && currentCond.Status == condition.Status && currentCond.Reason == condition.Reason {
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

// RemoveRolloutCondition removes the rollout condition with the provided type.
func RemoveRolloutCondition(status *appsv1alpha1.RolloutStatus, condType appsv1alpha1.RolloutConditionType) {
	status.Conditions = filterOutCondition(status.Conditions, condType)
}

// filterOutCondition returns a new slice of rollout conditions without conditions with the provided type.
func filterOutCondition(conditions []appsv1alpha1.RolloutCondition, condType appsv1alpha1.RolloutConditionType) []appsv1alpha1.RolloutCondition {
	var newConditions []appsv1alpha1.RolloutCondition
	for _, c := range conditions {
		if c.Type == condType {
			continue
		}
		newConditions = append(newConditions, c)
	}
	return newConditions
}
