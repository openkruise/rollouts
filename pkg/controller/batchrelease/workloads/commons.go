package workloads

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"k8s.io/utils/integer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func filterPodsForUnorderedRollback(pods []*corev1.Pod, plannedBatchCanaryReplicas, expectedBatchStableReplicas, replicas int32, rolloutID, updateRevision string) []*corev1.Pod {
	var noNeedRollbackReplicas int32
	var realNeedRollbackReplicas int32
	var expectedRollbackReplicas int32 // total need rollback

	var terminatingPods []*corev1.Pod
	var needRollbackPods []*corev1.Pod
	var noNeedRollbackPods []*corev1.Pod

	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			terminatingPods = append(terminatingPods, pod)
			continue
		}
		if !util.IsConsistentWithRevision(pod, updateRevision) {
			continue
		}
		podRolloutID := pod.Labels[util.RolloutIDLabel]
		podRollbackID := pod.Labels[util.NoNeedUpdatePodLabel]
		if podRollbackID == rolloutID && podRolloutID != rolloutID {
			noNeedRollbackReplicas++
			noNeedRollbackPods = append(noNeedRollbackPods, pod)
		} else {
			needRollbackPods = append(needRollbackPods, pod)
		}
	}

	expectedRollbackReplicas = replicas - expectedBatchStableReplicas
	realNeedRollbackReplicas = expectedRollbackReplicas - noNeedRollbackReplicas
	if realNeedRollbackReplicas <= 0 { // may never occur
		return pods
	}

	diff := plannedBatchCanaryReplicas - realNeedRollbackReplicas
	if diff <= 0 {
		return append(needRollbackPods, terminatingPods...)
	}

	lastIndex := integer.Int32Min(diff, int32(len(noNeedRollbackPods)))
	return append(append(needRollbackPods, noNeedRollbackPods[:lastIndex]...), terminatingPods...)
}

// TODO: support advanced statefulSet reserveOrdinal feature
func filterPodsForOrderedRollback(pods []*corev1.Pod, plannedBatchCanaryReplicas, expectedBatchStableReplicas, replicas int32, rolloutID, updateRevision string) []*corev1.Pod {
	var terminatingPods []*corev1.Pod
	var needRollbackPods []*corev1.Pod
	var noNeedRollbackPods []*corev1.Pod

	sortPodsByOrdinal(pods)
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			terminatingPods = append(terminatingPods, pod)
			continue
		}
		if !util.IsConsistentWithRevision(pod, updateRevision) {
			continue
		}
		if getPodOrdinal(pod) >= int(expectedBatchStableReplicas) {
			needRollbackPods = append(needRollbackPods, pod)
		} else {
			noNeedRollbackPods = append(noNeedRollbackPods, pod)
		}
	}
	realNeedRollbackReplicas := replicas - expectedBatchStableReplicas
	if realNeedRollbackReplicas <= 0 { // may never occur
		return pods
	}

	diff := plannedBatchCanaryReplicas - realNeedRollbackReplicas
	if diff <= 0 {
		return append(needRollbackPods, terminatingPods...)
	}

	lastIndex := integer.Int32Min(diff, int32(len(noNeedRollbackPods)))
	return append(append(needRollbackPods, noNeedRollbackPods[:lastIndex]...), terminatingPods...)
}

func countNoNeedRollbackReplicas(pods []*corev1.Pod, updateRevision, rolloutID string) int32 {
	noNeedRollbackReplicas := int32(0)
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		if !util.IsConsistentWithRevision(pod, updateRevision) {
			continue
		}
		id, ok := pod.Labels[util.NoNeedUpdatePodLabel]
		if ok && id == rolloutID {
			noNeedRollbackReplicas++
		}
	}
	return noNeedRollbackReplicas
}

// patchPodBatchLabel will patch rollout-id && batch-id to pods
func patchPodBatchLabel(c client.Client, pods []*corev1.Pod, rolloutID string, batchID int32, updateRevision string, replicas int32, logKey types.NamespacedName) (bool, error) {
	// the number of active pods that has been patched successfully.
	patchedUpdatedReplicas := int32(0)
	for _, pod := range pods {
		if !util.IsConsistentWithRevision(pod, updateRevision) {
			continue
		}

		podRolloutID := pod.Labels[util.RolloutIDLabel]
		if pod.DeletionTimestamp.IsZero() && podRolloutID == rolloutID {
			patchedUpdatedReplicas++
		}
	}

	for _, pod := range pods {
		podRolloutID := pod.Labels[util.RolloutIDLabel]
		if pod.DeletionTimestamp.IsZero() {
			// we don't patch label for the active old revision pod
			if !util.IsConsistentWithRevision(pod, updateRevision) {
				continue
			}
			// we don't continue to patch if the goal is met
			if patchedUpdatedReplicas >= replicas {
				continue
			}
		}

		// if it has been patched, just ignore
		if podRolloutID == rolloutID {
			continue
		}

		podClone := pod.DeepCopy()
		by := fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s","%s":"%d"}}}`, util.RolloutIDLabel, rolloutID, util.RolloutBatchIDLabel, batchID)
		err := c.Patch(context.TODO(), podClone, client.RawPatch(types.StrategicMergePatchType, []byte(by)))
		if err != nil {
			klog.Errorf("Failed to patch Pod(%v) batchID, err: %v", client.ObjectKeyFromObject(pod), err)
			return false, err
		} else {
			klog.Infof("Succeed to patch Pod(%v) batchID, err: %v", client.ObjectKeyFromObject(pod), err)
		}

		if pod.DeletionTimestamp.IsZero() {
			patchedUpdatedReplicas++
		}
	}

	klog.V(3).Infof("Patch %v pods with batchID for batchRelease %v, goal is %d pods", patchedUpdatedReplicas, logKey, replicas)
	return patchedUpdatedReplicas >= replicas, nil
}

func releaseWorkload(c client.Client, object client.Object) error {
	_, found := object.GetAnnotations()[util.BatchReleaseControlAnnotation]
	if !found {
		klog.V(3).Infof("Workload(%v) is already released", client.ObjectKeyFromObject(object))
		return nil
	}

	clone := object.DeepCopyObject().(client.Object)
	patchByte := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, util.BatchReleaseControlAnnotation))
	return c.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte))
}

func claimWorkload(c client.Client, planController *v1alpha1.BatchRelease, object client.Object, patchUpdateStrategy map[string]interface{}) error {
	if controlInfo, ok := object.GetAnnotations()[util.BatchReleaseControlAnnotation]; ok && controlInfo != "" {
		ref := &metav1.OwnerReference{}
		err := json.Unmarshal([]byte(controlInfo), ref)
		if err == nil && ref.UID == planController.UID {
			klog.V(3).Infof("Workload(%v) has been controlled by this BatchRelease(%v), no need to claim again",
				client.ObjectKeyFromObject(object), client.ObjectKeyFromObject(planController))
			return nil
		} else {
			klog.Errorf("Failed to parse controller info from Workload(%v) annotation, error: %v, controller info: %+v",
				client.ObjectKeyFromObject(object), err, *ref)
		}
	}

	controlInfo, _ := json.Marshal(metav1.NewControllerRef(planController, planController.GetObjectKind().GroupVersionKind()))
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				util.BatchReleaseControlAnnotation: string(controlInfo),
			},
		},
		"spec": map[string]interface{}{
			"updateStrategy": patchUpdateStrategy,
		},
	}

	patchByte, _ := json.Marshal(patch)
	clone := object.DeepCopyObject().(client.Object)
	return c.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte))
}

func patchSpec(c client.Client, object client.Object, spec map[string]interface{}) error {
	patchByte, err := json.Marshal(map[string]interface{}{"spec": spec})
	if err != nil {
		return err
	}
	clone := object.DeepCopyObject().(client.Object)
	return c.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, patchByte))
}

func calculateNewBatchTarget(rolloutSpec *v1alpha1.ReleasePlan, workloadReplicas, currentBatch int) int {
	batchSize, _ := intstr.GetValueFromIntOrPercent(&rolloutSpec.Batches[currentBatch].CanaryReplicas, workloadReplicas, true)
	if batchSize > workloadReplicas {
		klog.Warningf("releasePlan has wrong batch replicas, batches[%d].replicas %v is more than workload.replicas %v", currentBatch, batchSize, workloadReplicas)
		batchSize = workloadReplicas
	} else if batchSize < 0 {
		klog.Warningf("releasePlan has wrong batch replicas, batches[%d].replicas %v is less than 0 %v", currentBatch, batchSize)
		batchSize = 0
	}

	klog.V(3).InfoS("calculated the number of new pod size", "current batch", currentBatch, "new pod target", batchSize)
	return batchSize
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
