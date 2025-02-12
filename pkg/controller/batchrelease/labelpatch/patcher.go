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
	"context"
	"fmt"
	"strconv"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type LabelPatcher interface {
	PatchPodBatchLabel(ctx *batchcontext.BatchContext) error
}

func NewLabelPatcher(cli client.Client, logKey klog.ObjectRef, batches []v1beta1.ReleaseBatch) *realPatcher {
	return &realPatcher{Client: cli, logKey: logKey, batches: batches}
}

func NewDeploymentLabelPatcher(cli client.Client, logKey klog.ObjectRef, batches []v1beta1.ReleaseBatch) *nativeDeploymentPatcher {
	return &nativeDeploymentPatcher{
		rp: &realPatcher{Client: cli, logKey: logKey, batches: batches},
	}
}

type realPatcher struct {
	client.Client
	logKey  klog.ObjectRef
	batches []v1beta1.ReleaseBatch
}

func (r *realPatcher) PatchPodBatchLabel(ctx *batchcontext.BatchContext) error {
	if ctx.RolloutID == "" || len(ctx.Pods) == 0 {
		return nil
	}
	pods := ctx.Pods
	if ctx.FilterFunc != nil {
		pods = ctx.FilterFunc(pods, ctx)
	}
	return r.patchPodBatchLabel(pods, ctx)
}

// PatchPodBatchLabel will patch rollout-id && batch-id to pods
func (r *realPatcher) patchPodBatchLabel(pods []*corev1.Pod, ctx *batchcontext.BatchContext) error {
	plannedUpdatedReplicasForBatches := r.calculatePlannedStepIncrements(r.batches, int(ctx.Replicas), int(ctx.CurrentBatch))
	var updatedButUnpatchedPods []*corev1.Pod

	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			klog.InfoS("Pod is being deleted, skip patching", "pod", klog.KObj(pod), "rollout", r.logKey)
			continue
		}
		// we don't patch label for the active old revision pod
		if !util.IsConsistentWithRevision(pod, ctx.UpdateRevision) {
			klog.InfoS("Pod is not consistent with revision, skip patching", "pod", klog.KObj(pod), "rollout", r.logKey)
			continue
		}
		if pod.Labels[v1beta1.RolloutIDLabel] != ctx.RolloutID {
			// for example: new/recreated pods
			updatedButUnpatchedPods = append(updatedButUnpatchedPods, pod)
			klog.InfoS("Find a pod to add updatedButUnpatchedPods", "pod", klog.KObj(pod), "rollout", r.logKey)
			continue
		}

		podBatchID, err := strconv.Atoi(pod.Labels[v1beta1.RolloutBatchIDLabel])
		if err != nil {
			klog.InfoS("Pod batchID is not a number, skip patching", "pod", klog.KObj(pod), "rollout", r.logKey)
			continue
		}
		plannedUpdatedReplicasForBatches[podBatchID-1]--
	}
	klog.InfoS("updatedButUnpatchedPods amount calculated", "amount", len(updatedButUnpatchedPods), "rollout", r.logKey)
	// patch the pods
	for i := len(plannedUpdatedReplicasForBatches) - 1; i >= 0; i-- {
		for ; plannedUpdatedReplicasForBatches[i] > 0; plannedUpdatedReplicasForBatches[i]-- {
			if len(updatedButUnpatchedPods) == 0 {
				klog.Warningf("no pods to patch for %v, batch %d", r.logKey, i+1)
				return nil
			}
			// patch the updated but unpatced pod
			pod := updatedButUnpatchedPods[len(updatedButUnpatchedPods)-1]
			clone := util.GetEmptyObjectWithKey(pod)
			by := fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s","%s":"%d"}}}`,
				v1beta1.RolloutIDLabel, ctx.RolloutID, v1beta1.RolloutBatchIDLabel, i+1)
			if err := r.Patch(context.TODO(), clone, client.RawPatch(types.StrategicMergePatchType, []byte(by))); err != nil {
				return err
			}
			klog.InfoS("Successfully patch Pod batchID", "batchID", i+1, "pod", klog.KObj(pod), "rollout", r.logKey)
			// update the counter
			updatedButUnpatchedPods = updatedButUnpatchedPods[:len(updatedButUnpatchedPods)-1]
		}
		klog.InfoS("All pods has been patched batchID", "batchID", i+1, "rollout", r.logKey)
	}

	// for rollback in batch, it is possible that some updated pods are remained unpatched, we won't report error
	if len(updatedButUnpatchedPods) != 0 {
		klog.Warningf("still has %d pods to patch for %v", len(updatedButUnpatchedPods), r.logKey)
	}
	return nil
}

func (r *realPatcher) calculatePlannedStepIncrements(batches []v1beta1.ReleaseBatch, workloadReplicas, currentBatch int) (res []int) {
	// batchIndex greater than currentBatch will be patched with zero
	res = make([]int, len(batches))
	for i := 0; i <= currentBatch; i++ {
		res[i] = calculateBatchReplicas(batches, workloadReplicas, i)
	}
	for i := currentBatch; i > 0; i-- {
		res[i] -= res[i-1]
		if res[i] < 0 {
			klog.Warningf("Rollout %v batch replicas increment is less than 0", r.logKey)
		}
	}
	return
}

func calculateBatchReplicas(batches []v1beta1.ReleaseBatch, workloadReplicas, currentBatch int) int {
	batchSize, _ := intstr.GetScaledValueFromIntOrPercent(&batches[currentBatch].CanaryReplicas, workloadReplicas, true)
	if batchSize > workloadReplicas {
		klog.Warningf("releasePlan has wrong batch replicas, batches[%d].replicas %v is more than workload.replicas %v", currentBatch, batchSize, workloadReplicas)
		batchSize = workloadReplicas
	} else if batchSize < 0 {
		klog.Warningf("releasePlan has wrong batch replicas, batches[%d].replicas %v is less than 0 %v", currentBatch, batchSize)
		batchSize = 0
	}
	return batchSize
}

type nativeDeploymentPatcher struct {
	rp *realPatcher
}

// PatchPodBatchLabel patches label controller-revision-hash to pods owned by native deployments for pod-template-hash calculated
// by rollouts differs from the one calculated by k8s. The hash calculated by rollouts is set as the value of controller-revision-hash
func (p *nativeDeploymentPatcher) PatchPodBatchLabel(ctx *batchcontext.BatchContext) error {
	if ctx.RolloutID == "" || len(ctx.Pods) == 0 {
		return nil
	}
	pods := ctx.Pods
	if ctx.FilterFunc != nil {
		pods = ctx.FilterFunc(pods, ctx)
	}
	for i, pod := range pods {
		if pod.Labels[v1.ControllerRevisionHashLabelKey] != "" {
			continue
		}
		owner := metav1.GetControllerOf(pod)
		if owner == nil || owner.Kind != "ReplicaSet" {
			klog.InfoS("Deployment Pod owner is not ReplicaSet", "pod", klog.KObj(pod), "rollout", p.rp.logKey)
			continue
		}
		rs := &v1.ReplicaSet{}
		if err := p.rp.Get(context.Background(), types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, rs); err != nil {
			klog.ErrorS(err, "Failed to get ReplicaSet", "pod", klog.KObj(pod), "rollout", p.rp.logKey)
			return err
		}
		delete(rs.Spec.Template.ObjectMeta.Labels, v1.DefaultDeploymentUniqueLabelKey)
		podClone := pod.DeepCopy()
		hash := util.ComputeHash(&rs.Spec.Template, nil)
		podClone.Labels[v1.ControllerRevisionHashLabelKey] = hash
		by := fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s"}}}`, v1.ControllerRevisionHashLabelKey, hash)
		if err := p.rp.Patch(context.TODO(), podClone, client.RawPatch(types.StrategicMergePatchType, []byte(by))); err != nil {
			klog.ErrorS(err, "Failed to patch Pod controller-revision-hash label", "pod", klog.KObj(pod), "rollout", p.rp.logKey)
		}
		pods[i] = podClone
		klog.InfoS("Pod controller-revision-hash label patched", "pod", klog.KObj(pod), "rollout", p.rp.logKey, "label", hash)
	}
	return p.rp.patchPodBatchLabel(pods, ctx)
}
