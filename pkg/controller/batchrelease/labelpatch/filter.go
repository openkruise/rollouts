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

package labelpatch

import (
	"sort"
	"strconv"
	"strings"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/integer"
)

// FilterPodsForUnorderedUpdate can filter pods before patch pod batch label when rolling back in batches.
// for example:
// * There are 20 replicas: 10 updated replicas (version 2), 10 replicas (version 1), and the release plan is
//   - batch 0: 20%
//   - batch 1: 50%
//   - batch 2: 100%
//     Currently, if we decide to roll back to version 1, if you use this function, can help you just rollback
//     the pods that are really need to be rolled back according to release plan, but patch batch label according
//     original release plan, and will patch the pods that are really rolled back in priority.
//   - in batch 0: really roll back (20 - 10) * 20% = 2 pods, but 20 * 20% = 4 pod will be patched batch label;
//   - in batch 1: really roll back (20 - 10) * 50% = 5 pods, but 20 * 50% = 10 pod will be patched batch label;
//   - in batch 2: really roll back (20 - 10) * 100% = 10 pods, but 20 * 100% = 20 pod will be patched batch label;
//
// Mainly for PaaS platform display pod list in conveniently.
//
// This function only works for such unordered update strategy, such as CloneSet, Deployment, or Advanced
// StatefulSet with unordered update strategy.
func FilterPodsForUnorderedUpdate(pods []*corev1.Pod, ctx *batchcontext.BatchContext) []*corev1.Pod {
	var terminatingPods []*corev1.Pod
	var lowPriorityPods []*corev1.Pod
	var highPriorityPods []*corev1.Pod

	noNeedUpdate := int32(0)
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			terminatingPods = append(terminatingPods, pod)
			continue
		}
		if !util.IsConsistentWithRevision(pod.GetLabels(), ctx.UpdateRevision) {
			continue
		}
		if pod.Labels[util.NoNeedUpdatePodLabel] == ctx.RolloutID && pod.Labels[v1beta1.RolloutIDLabel] != ctx.RolloutID {
			noNeedUpdate++
			lowPriorityPods = append(lowPriorityPods, pod)
		} else {
			highPriorityPods = append(highPriorityPods, pod)
		}
	}

	needUpdate := ctx.DesiredUpdatedReplicas - noNeedUpdate
	if needUpdate <= 0 { // may never occur
		return pods
	}

	diff := ctx.PlannedUpdatedReplicas - needUpdate
	if diff <= 0 {
		return append(highPriorityPods, terminatingPods...)
	}

	lastIndex := integer.Int32Min(diff, int32(len(lowPriorityPods)))
	return append(append(highPriorityPods, lowPriorityPods[:lastIndex]...), terminatingPods...)
}

// FilterPodsForOrderedUpdate can filter pods before patch pod batch label when rolling back in batches.
// for example:
// * There are 20 replicas: 10 updated replicas (version 2), 10 replicas (version 1), and the release plan is
//   - batch 0: 20%
//   - batch 1: 50%
//   - batch 2: 100%
//     Currently, if we decide to roll back to version 1, if you use this function, can help you just rollback
//     the pods that are really need to be rolled back according to release plan, but patch batch label according
//     original release plan, and will patch the pods that are really rolled back in priority.
//   - in batch 0: really roll back (20 - 10) * 20% = 2 pods, but 20 * 20% = 4 pod will be patched batch label;
//   - in batch 1: really roll back (20 - 10) * 50% = 5 pods, but 20 * 50% = 10 pod will be patched batch label;
//   - in batch 2: really roll back (20 - 10) * 100% = 10 pods, but 20 * 100% = 20 pod will be patched batch label;
//
// Mainly for PaaS platform display pod list in conveniently.
//
// This function only works for such unordered update strategy, such as Native StatefulSet, and Advanced StatefulSet
// with ordered update strategy.
// TODO: support advanced statefulSet reserveOrdinal feature
func FilterPodsForOrderedUpdate(pods []*corev1.Pod, ctx *batchcontext.BatchContext) []*corev1.Pod {
	var terminatingPods []*corev1.Pod
	var lowPriorityPods []*corev1.Pod
	var highPriorityPods []*corev1.Pod

	sortPodsByOrdinal(pods)
	partition, _ := intstr.GetScaledValueFromIntOrPercent(
		&ctx.DesiredPartition, int(ctx.Replicas), true)
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			terminatingPods = append(terminatingPods, pod)
			continue
		}
		if !util.IsConsistentWithRevision(pod.GetLabels(), ctx.UpdateRevision) {
			continue
		}
		if getPodOrdinal(pod) >= partition {
			highPriorityPods = append(highPriorityPods, pod)
		} else {
			lowPriorityPods = append(lowPriorityPods, pod)
		}
	}
	needUpdate := ctx.Replicas - int32(partition)
	if needUpdate <= 0 { // may never occur
		return pods
	}

	diff := ctx.PlannedUpdatedReplicas - needUpdate
	if diff <= 0 {
		return append(highPriorityPods, terminatingPods...)
	}

	lastIndex := integer.Int32Min(diff, int32(len(lowPriorityPods)))
	return append(append(highPriorityPods, lowPriorityPods[:lastIndex]...), terminatingPods...)
}

func sortPodsByOrdinal(pods []*corev1.Pod) {
	sort.Slice(pods, func(i, j int) bool {
		ordI, _ := strconv.Atoi(pods[i].Name[strings.LastIndex(pods[i].Name, "-"):])
		ordJ, _ := strconv.Atoi(pods[j].Name[strings.LastIndex(pods[j].Name, "-"):])
		return ordJ > ordI
	})
}

func getPodOrdinal(pod *corev1.Pod) int {
	ord, _ := strconv.Atoi(pod.Name[strings.LastIndex(pod.Name, "-")+1:])
	return ord
}
