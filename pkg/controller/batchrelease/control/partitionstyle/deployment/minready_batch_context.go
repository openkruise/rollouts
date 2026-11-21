/*
Copyright 2026 The Kruise Authors.

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

package deployment

import (
	"fmt"
	"time"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openkruise/rollouts/pkg/util"
)

func (mc *MinReadyControl) minReadyUpdatedReadyReplicas(updateRevision string, pods []*corev1.Pod) (int32, error) {
	original, err := parseOriginalDeploymentStrategy(mc.object.Annotations)
	if err != nil {
		return 0, err
	}
	return countUpdatedAvailablePods(pods, updateRevision, originalMinReadySeconds(original), time.Now()), nil
}

func countUpdatedAvailablePods(pods []*corev1.Pod, updateRevision string, minReadySeconds int32, now time.Time) int32 {
	return int32(util.WrappedPodCount(pods, func(pod *corev1.Pod) bool {
		if !util.IsPodActive(pod) {
			return false
		}
		if !util.IsConsistentWithRevision(pod.Labels, updateRevision) {
			return false
		}
		ready := util.GetPodReadyCondition(pod.Status)
		if ready == nil || ready.Status != corev1.ConditionTrue {
			return false
		}
		return !ready.LastTransitionTime.Add(time.Duration(minReadySeconds) * time.Second).After(now)
	}))
}

func originalMinReadySeconds(original *originalDeploymentStrategy) int32 {
	if original.minReadySeconds == nil {
		return 0
	}
	return *original.minReadySeconds
}

func minReadyDesiredUpdatedReplicas(desired intstr.IntOrString, deployment *apps.Deployment) (int32, error) {
	if deployment.Spec.Replicas == nil {
		return 0, fmt.Errorf("deployment replicas is nil")
	}
	replicas := int(*deployment.Spec.Replicas)
	target, err := intstr.GetScaledValueFromIntOrPercent(&desired, replicas, true)
	if err != nil {
		return 0, err
	}
	if target < 0 {
		return 0, nil
	}
	if target > replicas {
		return int32(replicas), nil
	}
	return int32(target), nil
}
