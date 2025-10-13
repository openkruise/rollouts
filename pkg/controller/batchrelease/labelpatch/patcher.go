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
	"errors"
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

	revisionHashCache := map[types.UID]string{}               // to prevent duplicate computing for revision hash
	podsToPatchControllerRevision := map[*corev1.Pod]string{} // to record pods to patch controller-revision-hash
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			klog.InfoS("Pod is being deleted, skip patching", "pod", klog.KObj(pod), "rollout", r.logKey)
			continue
		}
		labels := make(map[string]string, len(pod.Labels))
		for k, v := range pod.Labels {
			labels[k] = v
		}
		if labels[v1.ControllerRevisionHashLabelKey] == "" {
			// For native deployment, we need to get the revision hash from ReplicaSet, which is exactly constants with the update revision
			// The reason is that, The status of the Deployment that KCM sees may differ from the Deployment seen by
			// the Rollouts controller due to some default values not being assigned yet. Therefore, even if both use
			// exactly the same algorithm, they cannot compute the same pod-template-hash. The fact that K8S does not
			// expose the method for computing the pod-template-hash also confirms that third-party components relying
			// on the pod-template-hash is not recommended. Thus, we use the CloneSet algorithm to compute the
			// controller-revision-hash: this method is fully defined by OpenKruise and can ensure that the same
			// ReplicaSet produces the same value.
			owner := metav1.GetControllerOfNoCopy(pod)
			if owner != nil && owner.Kind == "ReplicaSet" {
				var hash string
				if cache, ok := revisionHashCache[owner.UID]; ok {
					hash = cache
				} else {
					rs := &v1.ReplicaSet{}
					if err := r.Get(context.Background(), types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, rs); err != nil {
						klog.ErrorS(err, "Failed to get ReplicaSet when patching pod", "pod", klog.KObj(pod), "rollout", r.logKey, "owner", owner.Name, "namespace", pod.Namespace)
						return err
					}
					// Under normal circumstances, if pod-template-hash is not specified in the Deployment's PodTemplate,
					// then one will be generated in the ReplicaSet it manages. This means that there may be differences
					// between the Deployment and the ReplicaSet's PodTemplate in whether pod-template-hash is included.
					// When BatchRelease calculates the update revision, it uses the Deployment's PodTemplate, while when
					// calculating the pod's revision, it uses the ReplicaSet's revision. The above difference may cause
					// the pod revision to be inconsistent with the update revision, which in turn causes the subsequent
					// IsConsistentWithRevision function to return false and skip patching the label, ultimately causing
					// the BatchRelease to be stuck in the Verifying phase. This is the reason why the label is removed
					// from the ReplicaSet before calculating the pod hash.
					//
					// However, the Deployment's PodTemplate is also allowed to carry pod-template-hash. In this case,
					// directly deleting this label will also cause the two hashes to be inconsistent. Therefore, we
					// need to determine whether the Deployment contains this label to decide whether it needs to be
					// deleted from the ReplicaSet in the final analysis.
					rsOwner := metav1.GetControllerOfNoCopy(rs)
					if rsOwner == nil || rsOwner.Kind != "Deployment" {
						klog.ErrorS(nil, "ReplicaSet has no deployment owner, skip patching")
						return errors.New("ReplicaSet has no deployment owner")
					}
					deploy := &v1.Deployment{}
					if err := r.Get(context.Background(), types.NamespacedName{Namespace: pod.Namespace, Name: rsOwner.Name}, deploy); err != nil {
						klog.ErrorS(err, "Failed to get Deployment when patching pod", "pod", klog.KObj(pod), "rollout", r.logKey, "owner", owner.Name, "namespace", pod.Namespace)
						return err
					}
					if deploy.Spec.Template.Labels[v1.DefaultDeploymentUniqueLabelKey] == "" {
						delete(rs.Spec.Template.ObjectMeta.Labels, v1.DefaultDeploymentUniqueLabelKey)
					}
					hash = util.ComputeHash(&rs.Spec.Template, nil)
					revisionHashCache[owner.UID] = hash
				}
				labels[v1.ControllerRevisionHashLabelKey] = hash
				podsToPatchControllerRevision[pod] = hash
				klog.InfoS("Pod controller-revision-hash updated", "pod", klog.KObj(pod), "rollout", r.logKey, "hash", hash)
			}
		}

		// we don't patch label for the active old revision pod
		if !util.IsConsistentWithRevision(labels, ctx.UpdateRevision) {
			klog.InfoS("Pod is not consistent with revision, skip patching", "pod", klog.KObj(pod),
				"revision", ctx.UpdateRevision, "pod-template-hash", labels[v1.DefaultDeploymentUniqueLabelKey],
				"controller-revision-hash", labels[v1.ControllerRevisionHashLabelKey], "rollout", r.logKey)
			continue
		}
		if labels[v1beta1.RolloutIDLabel] != ctx.RolloutID {
			// for example: new/recreated pods
			updatedButUnpatchedPods = append(updatedButUnpatchedPods, pod)
			klog.InfoS("Find a pod to add updatedButUnpatchedPods", "pod", klog.KObj(pod), "rollout", r.logKey)
			continue
		}

		podBatchID, err := strconv.Atoi(labels[v1beta1.RolloutBatchIDLabel])
		if err != nil {
			klog.InfoS("Pod batchID is not a number, will overwrite it", "pod", klog.KObj(pod),
				"rollout", r.logKey, "batchID", labels[v1beta1.RolloutBatchIDLabel])
			updatedButUnpatchedPods = append(updatedButUnpatchedPods, pod)
			continue
		}
		if podBatchID > 0 && podBatchID <= len(plannedUpdatedReplicasForBatches) {
			plannedUpdatedReplicasForBatches[podBatchID-1]--
		} else {
			klog.InfoS("Pod batchID is not valid, will overwrite it", "pod", klog.KObj(pod),
				"rollout", r.logKey, "batchID", labels[v1beta1.RolloutBatchIDLabel])
			updatedButUnpatchedPods = append(updatedButUnpatchedPods, pod)
		}
	}
	klog.InfoS("updatedButUnpatchedPods amount calculated", "amount", len(updatedButUnpatchedPods),
		"rollout", r.logKey, "plan", plannedUpdatedReplicasForBatches)
	// patch the pods
	for i := len(plannedUpdatedReplicasForBatches) - 1; i >= 0; i-- {
		for ; plannedUpdatedReplicasForBatches[i] > 0; plannedUpdatedReplicasForBatches[i]-- {
			if len(updatedButUnpatchedPods) == 0 {
				klog.Warningf("no pods to patch for %v, batch %d", r.logKey, i+1)
				i = -1
				break
			}
			// patch the updated but unpatched pod
			pod := updatedButUnpatchedPods[len(updatedButUnpatchedPods)-1]
			clone := util.GetEmptyObjectWithKey(pod)
			var patchStr string
			if hash, ok := podsToPatchControllerRevision[pod]; ok {
				patchStr = fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s","%s":"%d","%s":"%s"}}}`,
					v1beta1.RolloutIDLabel, ctx.RolloutID, v1beta1.RolloutBatchIDLabel, i+1, v1.ControllerRevisionHashLabelKey, hash)
				delete(podsToPatchControllerRevision, pod)
			} else {
				patchStr = fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s","%s":"%d"}}}`,
					v1beta1.RolloutIDLabel, ctx.RolloutID, v1beta1.RolloutBatchIDLabel, i+1)
			}
			if err := r.Patch(context.TODO(), clone, client.RawPatch(types.StrategicMergePatchType, []byte(patchStr))); err != nil {
				return err
			}
			klog.InfoS("Successfully patched Pod batchID", "batchID", i+1, "pod", klog.KObj(pod), "rollout", r.logKey)
			// update the counter
			updatedButUnpatchedPods = updatedButUnpatchedPods[:len(updatedButUnpatchedPods)-1]
		}
		klog.InfoS("All pods has been patched batchID", "batchID", i+1, "rollout", r.logKey)
	}

	// for rollback in batch, it is possible that some updated pods are remained unpatched, we won't report error
	if len(updatedButUnpatchedPods) != 0 {
		klog.Warningf("still has %d pods to patch for %v", len(updatedButUnpatchedPods), r.logKey)
	}

	// pods with controller-revision-hash label updated but not in the rollout release need to be patched too
	// We must promptly patch the computed controller-revision-hash label to the Pod so that it can be directly read
	// during frequent Reconcile processes, avoiding a large amount of redundant computation.
	for pod, hash := range podsToPatchControllerRevision {
		clone := util.GetEmptyObjectWithKey(pod)
		patchStr := fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s"}}}`, v1.ControllerRevisionHashLabelKey, hash)
		if err := r.Patch(context.TODO(), clone, client.RawPatch(types.StrategicMergePatchType, []byte(patchStr))); err != nil {
			return err
		}
		klog.InfoS("Successfully patch Pod controller-revision-hash", "pod", klog.KObj(pod), "rollout", r.logKey, "hash", hash)
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
