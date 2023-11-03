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

package cloneset

import (
	"context"
	"fmt"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/labelpatch"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type realController struct {
	*util.WorkloadInfo
	client client.Client
	pods   []*corev1.Pod
	key    types.NamespacedName
	object *kruiseappsv1alpha1.CloneSet
}

func NewController(cli client.Client, key types.NamespacedName, _ schema.GroupVersionKind) partitionstyle.Interface {
	return &realController{
		key:    key,
		client: cli,
	}
}

func (rc *realController) GetWorkloadInfo() *util.WorkloadInfo {
	return rc.WorkloadInfo
}

func (rc *realController) BuildController() (partitionstyle.Interface, error) {
	if rc.object != nil {
		return rc, nil
	}
	object := &kruiseappsv1alpha1.CloneSet{}
	if err := rc.client.Get(context.TODO(), rc.key, object); err != nil {
		return rc, err
	}
	rc.object = object
	rc.WorkloadInfo = util.ParseWorkload(object)
	return rc, nil
}

func (rc *realController) ListOwnedPods() ([]*corev1.Pod, error) {
	if rc.pods != nil {
		return rc.pods, nil
	}
	var err error
	rc.pods, err = util.ListOwnedPods(rc.client, rc.object)
	return rc.pods, err
}

func (rc *realController) Initialize(release *v1beta1.BatchRelease) error {
	if control.IsControlledByBatchRelease(release, rc.object) {
		return nil
	}

	clone := util.GetEmptyObjectWithKey(rc.object)
	owner := control.BuildReleaseControlInfo(release)
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}},"spec":{"updateStrategy":{"paused":%v,"partition":"%s"}}}`,
		util.BatchReleaseControlAnnotation, owner, false, "100%")
	return rc.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))
}

func (rc *realController) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	var body string
	var desired int
	switch partition := ctx.DesiredPartition; partition.Type {
	case intstr.Int:
		desired = int(partition.IntVal)
		body = fmt.Sprintf(`{"spec":{"updateStrategy":{"partition": %d }}}`, partition.IntValue())
	case intstr.String:
		desired, _ = intstr.GetScaledValueFromIntOrPercent(&partition, int(ctx.Replicas), true)
		body = fmt.Sprintf(`{"spec":{"updateStrategy":{"partition":"%s"}}}`, partition.String())
	}
	current, _ := intstr.GetScaledValueFromIntOrPercent(&ctx.CurrentPartition, int(ctx.Replicas), true)

	// current less than desired, which means current revision replicas will be less than desired,
	// in other word, update revision replicas will be more than desired, no need to update again.
	if current <= desired {
		return nil
	}

	clone := util.GetEmptyObjectWithKey(rc.object)
	return rc.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))
}

func (rc *realController) Finalize(release *v1beta1.BatchRelease) error {
	if rc.object == nil {
		return nil
	}

	var specBody string
	// if batchPartition == nil, workload should be promoted.
	if release.Spec.ReleasePlan.BatchPartition == nil {
		specBody = `,"spec":{"updateStrategy":{"partition":null,"paused":false}}`
	}

	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}%s}`, util.BatchReleaseControlAnnotation, specBody)

	clone := util.GetEmptyObjectWithKey(rc.object)
	return rc.client.Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))
}

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

	// make sure at least one pod is upgrade is canaryReplicas is not "0%"
	desiredPartition := intstr.FromInt(int(desiredStable))
	batchPlan := release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas
	if batchPlan.Type == intstr.String {
		desiredPartition = control.ParseIntegerAsPercentageIfPossible(desiredStable, rc.Replicas, &batchPlan)
	}

	currentPartition := intstr.FromInt(0)
	if rc.object.Spec.UpdateStrategy.Partition != nil {
		currentPartition = *rc.object.Spec.UpdateStrategy.Partition
	}

	batchContext := &batchcontext.BatchContext{
		Pods:             rc.pods,
		RolloutID:        rolloutID,
		CurrentBatch:     currentBatch,
		UpdateRevision:   release.Status.UpdateRevision,
		DesiredPartition: desiredPartition,
		CurrentPartition: currentPartition,
		FailureThreshold: release.Spec.ReleasePlan.FailureThreshold,

		Replicas:               rc.Replicas,
		UpdatedReplicas:        rc.Status.UpdatedReplicas,
		UpdatedReadyReplicas:   rc.Status.UpdatedReadyReplicas,
		NoNeedUpdatedReplicas:  noNeedUpdate,
		PlannedUpdatedReplicas: plannedUpdate,
		DesiredUpdatedReplicas: desiredUpdate,
	}

	if noNeedUpdate != nil {
		batchContext.FilterFunc = labelpatch.FilterPodsForUnorderedUpdate
	}
	return batchContext, nil
}
