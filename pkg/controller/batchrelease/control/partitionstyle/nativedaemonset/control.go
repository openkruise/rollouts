package nativedaemonset

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/labelpatch"
	"github.com/openkruise/rollouts/pkg/util"
)

type realController struct {
	*util.WorkloadInfo
	client client.Client
	pods   []*corev1.Pod
	key    types.NamespacedName
	gvk    schema.GroupVersionKind
	object *apps.DaemonSet
}

func NewController(cli client.Client, key types.NamespacedName, gvk schema.GroupVersionKind) partitionstyle.Interface {
	return &realController{
		key:    key,
		client: cli,
		gvk:    gvk,
	}
}

// GetWorkloadInfo return workload information.
func (rc *realController) GetWorkloadInfo() *util.WorkloadInfo {
	return rc.WorkloadInfo
}

// BuildController will get workload object and parse workload info,
// and return an initialized controller for workload.
func (rc *realController) BuildController() (partitionstyle.Interface, error) {
	if rc.object != nil {
		return rc, nil
	}
	object := &apps.DaemonSet{}
	if err := rc.client.Get(context.TODO(), rc.key, object); err != nil {
		return rc, err
	}
	rc.object = object

	// Parse workload info for native DaemonSet
	rc.WorkloadInfo = util.ParseWorkload(object)

	// For native DaemonSet which has no updatedReadyReplicas field, we should
	// list and count its owned Pods one by one.
	if rc.WorkloadInfo != nil && rc.WorkloadInfo.Status.UpdatedReadyReplicas <= 0 {
		pods, err := rc.ListOwnedPods()
		if err != nil {
			return nil, err
		}
		updatedReadyReplicas := util.WrappedPodCount(pods, func(pod *corev1.Pod) bool {
			if !pod.DeletionTimestamp.IsZero() {
				return false
			}
			// Use rc.WorkloadInfo.Status.UpdateRevision for consistency
			if !util.IsConsistentWithRevision(pod.GetLabels(), rc.WorkloadInfo.Status.UpdateRevision) {
				return false
			}
			return util.IsPodReady(pod)
		})
		rc.WorkloadInfo.Status.UpdatedReadyReplicas = int32(updatedReadyReplicas)
	}
	return rc, nil
}

// ListOwnedPods fetch the pods owned by the workload.
func (rc *realController) ListOwnedPods() ([]*corev1.Pod, error) {
	if rc.pods != nil {
		return rc.pods, nil
	}
	var err error
	rc.pods, err = util.ListOwnedPods(rc.client, rc.object)
	return rc.pods, err
}

// Initialize prepares the native DaemonSet for batch release by setting the appropriate update strategy.
func (rc *realController) Initialize(release *v1beta1.BatchRelease) error {
	if control.IsControlledByBatchRelease(release, rc.object) {
		return nil
	}

	// For native DaemonSet, we set the update strategy to OnDelete to enable manual control
	daemon := util.GetEmptyObjectWithKey(rc.object)
	owner := control.BuildReleaseControlInfo(release)

	unescapedOwner, err := strconv.Unquote(`"` + owner + `"`)
	if err != nil {
		return fmt.Errorf("failed to unescape owner info: %v", err)
	}

	// Create a proper JSON patch using map structure to avoid escaping issues
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				util.BatchReleaseControlAnnotation: unescapedOwner,
			},
		},
		"spec": map[string]interface{}{
			"updateStrategy": map[string]interface{}{
				"type": "OnDelete",
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %v", err)
	}

	return rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, patchBytes))
}

// UpgradeBatch handles the batch upgrade for native DaemonSet by managing annotations.
// The actual pod deletion is handled by the advanced-daemonset-controller.
func (rc *realController) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	// Check if the DaemonSet already has the partition annotation
	currentPartitionStr, hasPartition := rc.object.Annotations[util.DaemonSetPartitionAnnotation]
	desiredPartitionStr := ctx.DesiredPartition.String()

	// If annotation is missing or doesn't equal desired value, patch the DaemonSet
	if !hasPartition || currentPartitionStr != desiredPartitionStr {
		klog.Infof("Updating partition annotation for DaemonSet %s/%s: %s -> %s",
			rc.object.Namespace, rc.object.Name, currentPartitionStr, desiredPartitionStr)
		return rc.patchBatchAnnotations(ctx)
	}

	// Partition annotation already matches desired value, no action needed
	klog.Infof("DaemonSet %s/%s partition annotation already matches desired value: %s",
		rc.object.Namespace, rc.object.Name, desiredPartitionStr)
	return nil
}

// patchBatchAnnotations patches the DaemonSet with batch control annotations
func (rc *realController) patchBatchAnnotations(ctx *batchcontext.BatchContext) error {
	// Create patch with batch annotations
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				util.DaemonSetPartitionAnnotation:     ctx.DesiredPartition.String(),
				util.DaemonSetBatchRevisionAnnotation: ctx.UpdateRevision,
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %v", err)
	}

	daemon := util.GetEmptyObjectWithKey(rc.object)
	return rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, patchBytes))
}

// Finalize cleans up the annotations and restores the original update strategy.
func (rc *realController) Finalize(release *v1beta1.BatchRelease) error {
	if rc.object == nil {
		return nil
	}

	var specBody string
	// if batchPartition == nil, workload should be promoted to use the original update strategy
	if release.Spec.ReleasePlan.BatchPartition == nil {
		updateStrategy := apps.DaemonSetUpdateStrategy{
			Type: apps.RollingUpdateDaemonSetStrategyType,
		}
		strategyBytes, _ := json.Marshal(updateStrategy)
		specBody = fmt.Sprintf(`,"spec":{"updateStrategy":%s}`, string(strategyBytes))
	}

	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null,"%s":null,"%s":null,"%s":null,"%s":null}}%s}`,
		util.BatchReleaseControlAnnotation,
		util.DaemonSetCanaryRevisionAnnotation,
		util.DaemonSetStableRevisionAnnotation,
		util.DaemonSetPartitionAnnotation,
		util.DaemonSetBatchRevisionAnnotation,
		specBody)

	daemon := util.GetEmptyObjectWithKey(rc.object)

	return rc.client.Patch(context.TODO(), daemon, client.RawPatch(types.MergePatchType, []byte(body)))
}

// CalculateBatchContext calculates the batch context for native DaemonSet.
func (rc *realController) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	rolloutID := release.Spec.ReleasePlan.RolloutID
	if rolloutID != "" {
		// if rollout-id is set, the pod will be patched batch label,
		// so we have to list pod here.
		if _, err := rc.ListOwnedPods(); err != nil {
			return nil, err
		}
	}

	// current batch index
	currentBatch := release.Status.CanaryStatus.CurrentBatch
	// the number of no need update pods that marked before rollout
	noNeedUpdate := release.Status.CanaryStatus.NoNeedUpdateReplicas
	// the number of upgraded pods according to release plan in current batch.
	plannedUpdate := int32(control.CalculateBatchReplicas(release, int(rc.Replicas), int(currentBatch)))
	// the number of pods that should be upgraded in real
	desiredUpdate := plannedUpdate
	// the number of pods that should not be upgraded in real
	desiredStable := rc.Replicas - desiredUpdate
	// if we should consider the no-need-update pods that were marked before progressing
	if noNeedUpdate != nil && *noNeedUpdate > 0 {
		// specially, we should ignore the pods that were marked as no-need-update, this logic is for Rollback scene
		desiredUpdateNew := int32(control.CalculateBatchReplicas(release, int(rc.Replicas-*noNeedUpdate), int(currentBatch)))
		desiredStable = rc.Replicas - *noNeedUpdate - desiredUpdateNew
		desiredUpdate = rc.Replicas - desiredStable
	}

	// For native DaemonSet, we calculate based on the number of pods to update
	// rather than partition (since OnDelete doesn't use partition)
	desiredPartition := intstr.FromInt(int(desiredStable))
	if desiredStable <= 0 {
		desiredPartition = intstr.FromInt(0)
	}

	currentPartition := intstr.FromInt(0)

	// compute updatedReadyReplicas by pods
	// because DaemonSet has no updatedReadyReplicas field in status
	// we have to calculate it by ourselves
	var updatedReadyReplicas int32
	if rc.pods != nil {
		for _, pod := range rc.pods {
			// Skip if pod is marked for deletion
			if !pod.DeletionTimestamp.IsZero() {
				continue
			}

			// Check if pod is on the update revision
			if util.IsConsistentWithRevision(pod.GetLabels(), release.Status.UpdateRevision) {
				if util.IsPodReady(pod) {
					updatedReadyReplicas++
				}
			}
		}
	} else {
		// Fallback to using status values if pods are not available
		updatedReadyReplicas = rc.Status.UpdatedReadyReplicas
	}

	batchContext := &batchcontext.BatchContext{
		Pods:                   rc.pods,
		RolloutID:              rolloutID,
		CurrentBatch:           currentBatch,
		UpdateRevision:         release.Status.UpdateRevision,
		DesiredPartition:       desiredPartition,
		CurrentPartition:       currentPartition,
		FailureThreshold:       release.Spec.ReleasePlan.FailureThreshold,
		Replicas:               rc.Replicas,
		UpdatedReplicas:        rc.Status.UpdatedReplicas,
		UpdatedReadyReplicas:   updatedReadyReplicas,
		NoNeedUpdatedReplicas:  noNeedUpdate,
		PlannedUpdatedReplicas: plannedUpdate,
		DesiredUpdatedReplicas: desiredUpdate,
	}

	if noNeedUpdate != nil {
		batchContext.FilterFunc = labelpatch.FilterPodsForUnorderedUpdate
	}
	return batchContext, nil
}
