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
	"context"
	"fmt"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/pkg/util"
)

func (mc *MinReadyControl) minReadyUpdatedReadyReplicas(updateRevision string) (int32, error) {
	if len(mc.pods) > 0 {
		return countUpdatedReadyPods(mc.pods, updateRevision), nil
	}
	return mc.updatedReadyReplicasFromReplicaSet(updateRevision)
}

func countUpdatedReadyPods(pods []*corev1.Pod, updateRevision string) int32 {
	return int32(util.WrappedPodCount(pods, func(pod *corev1.Pod) bool {
		if !pod.DeletionTimestamp.IsZero() {
			return false
		}
		return util.IsConsistentWithRevision(pod.Labels, updateRevision) && util.IsPodReady(pod)
	}))
}

func (mc *MinReadyControl) updatedReadyReplicasFromReplicaSet(updateRevision string) (int32, error) {
	rsList := &apps.ReplicaSetList{}
	if err := mc.client.List(context.TODO(), rsList, client.InNamespace(mc.object.Namespace)); err != nil {
		return 0, fmt.Errorf("list ReplicaSets: %w", err)
	}

	var ready int32
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if !metav1.IsControlledBy(rs, mc.object) {
			continue
		}
		if !replicaSetMatchesUpdateRevision(rs, updateRevision) {
			continue
		}
		ready += rs.Status.ReadyReplicas
	}
	return ready, nil
}

func replicaSetMatchesUpdateRevision(rs *apps.ReplicaSet, updateRevision string) bool {
	if util.ComputeHash(&rs.Spec.Template, nil) == updateRevision {
		return true
	}
	return util.IsConsistentWithRevision(rs.Labels, updateRevision)
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
