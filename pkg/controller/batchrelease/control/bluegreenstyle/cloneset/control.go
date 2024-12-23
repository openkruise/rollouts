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
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/bluegreenstyle"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/bluegreenstyle/hpa"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/openkruise/rollouts/pkg/util/errors"
	"github.com/openkruise/rollouts/pkg/util/patch"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type realController struct {
	*util.WorkloadInfo
	client client.Client
	pods   []*corev1.Pod
	key    types.NamespacedName
	object *kruiseappsv1alpha1.CloneSet
}

func NewController(cli client.Client, key types.NamespacedName, _ schema.GroupVersionKind) bluegreenstyle.Interface {
	return &realController{
		key:    key,
		client: cli,
	}
}

func (rc *realController) GetWorkloadInfo() *util.WorkloadInfo {
	return rc.WorkloadInfo
}

func (rc *realController) BuildController() (bluegreenstyle.Interface, error) {
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
	if rc.object == nil || control.IsControlledByBatchRelease(release, rc.object) {
		return nil
	}

	// disable the hpa
	if err := hpa.DisableHPA(rc.client, rc.object); err != nil {
		return err
	}
	klog.InfoS("Initialize: disable hpa for cloneset successfully", "cloneset", klog.KObj(rc.object))

	// patch the cloneset
	setting, err := control.GetOriginalSetting(rc.object)
	if err != nil {
		return errors.NewBadRequestError(fmt.Errorf("cannot get original setting for cloneset %v: %s from annotation", klog.KObj(rc.object), err.Error()))
	}
	control.InitOriginalSetting(&setting, rc.object)
	patchData := patch.NewClonesetPatch()
	patchData.InsertAnnotation(v1beta1.OriginalDeploymentStrategyAnnotation, util.DumpJSON(&setting))
	patchData.InsertAnnotation(util.BatchReleaseControlAnnotation, util.DumpJSON(metav1.NewControllerRef(
		release, release.GetObjectKind().GroupVersionKind())))
	// we use partition = 100% to function as "paused" instead of setting pasued field as true
	// it is manily to keep consistency with partition style (partition is already set as 100% in webhook)
	patchData.UpdatePaused(false)
	maxSurge := intstr.FromInt(1) // select the minimum positive number as initial value
	maxUnavailable := intstr.FromInt(0)
	patchData.UpdateMaxSurge(&maxSurge)
	patchData.UpdateMaxUnavailable(&maxUnavailable)
	patchData.UpdateMinReadySeconds(v1beta1.MaxReadySeconds)
	klog.InfoS("Initialize: try to update cloneset", "cloneset", klog.KObj(rc.object), "patchData", patchData.String())
	return rc.client.Patch(context.TODO(), util.GetEmptyObjectWithKey(rc.object), patchData)
}

func (rc *realController) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	if err := control.ValidateReadyForBlueGreenRelease(rc.object); err != nil {
		return errors.NewBadRequestError(fmt.Errorf("cannot upgrade batch, because cloneset %v doesn't satisfy conditions: %s", klog.KObj(rc.object), err.Error()))
	}
	desired, _ := intstr.GetScaledValueFromIntOrPercent(&ctx.DesiredSurge, int(ctx.Replicas), true)
	current, _ := intstr.GetScaledValueFromIntOrPercent(&ctx.CurrentSurge, int(ctx.Replicas), true)
	if current >= desired {
		klog.InfoS("No need to upgrade batch, because current >= desired", "cloneset", klog.KObj(rc.object), "current", current, "desired", desired)
		return nil
	} else {
		klog.InfoS("Will update batch for cloneset, because current < desired", "cloneset", klog.KObj(rc.object), "current", current, "desired", desired)
	}
	patchData := patch.NewClonesetPatch()
	// avoid interference from partition
	patchData.UpdatePartiton(nil)
	patchData.UpdateMaxSurge(&ctx.DesiredSurge)
	return rc.client.Patch(context.TODO(), util.GetEmptyObjectWithKey(rc.object), patchData)
}

func (rc *realController) Finalize(release *v1beta1.BatchRelease) error {
	if release.Spec.ReleasePlan.BatchPartition != nil {
		// continuous release (not supported yet)
		/*
			patchData := patch.NewClonesetPatch()
			patchData.DeleteAnnotation(util.BatchReleaseControlAnnotation)
			return rc.client.Patch(context.TODO(), util.GetEmptyObjectWithKey(rc.object), patchData)
		*/
		klog.Warningf("continuous release is not supported yet for bluegreen style release")
		return nil
	}

	// restore the original setting and remove annotation
	if !rc.restored() {
		c := util.GetEmptyObjectWithKey(rc.object)
		setting, err := control.GetOriginalSetting(rc.object)
		if err != nil {
			return err
		}
		patchData := patch.NewClonesetPatch()
		patchData.UpdateMinReadySeconds(setting.MinReadySeconds)
		patchData.UpdateMaxSurge(setting.MaxSurge)
		patchData.UpdateMaxUnavailable(setting.MaxUnavailable)
		patchData.DeleteAnnotation(v1beta1.OriginalDeploymentStrategyAnnotation)
		patchData.DeleteAnnotation(util.BatchReleaseControlAnnotation)
		if err := rc.client.Patch(context.TODO(), c, patchData); err != nil {
			return err
		}
		klog.InfoS("Finalize: cloneset bluegreen release: wait all pods updated and ready", "cloneset", klog.KObj(rc.object))
	}

	// wait all pods updated and ready
	if rc.object.Status.ReadyReplicas != rc.object.Status.UpdatedReadyReplicas {
		return errors.NewRetryError(fmt.Errorf("cloneset %v finalize not done, readyReplicas %d != updatedReadyReplicas %d, current policy %s",
			klog.KObj(rc.object), rc.object.Status.ReadyReplicas, rc.object.Status.UpdatedReadyReplicas, release.Spec.ReleasePlan.FinalizingPolicy))
	}
	klog.InfoS("Finalize: cloneset bluegreen release: all pods updated and ready", "cloneset", klog.KObj(rc.object))

	// restore the hpa
	return hpa.RestoreHPA(rc.client, rc.object)
}

func (rc *realController) restored() bool {
	if rc.object == nil || rc.object.DeletionTimestamp != nil {
		return true
	}
	if rc.object.Annotations == nil || len(rc.object.Annotations[v1beta1.OriginalDeploymentStrategyAnnotation]) == 0 {
		return true
	}
	return false
}

// bluegreen doesn't support rollback in batch, because:
// - bluegreen support traffic rollback instead, rollback in batch is not necessary
// - it's diffcult for both Deployment and CloneSet to support rollback in batch, with the "minReadySeconds" implementation
func (rc *realController) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	// current batch index
	currentBatch := release.Status.CanaryStatus.CurrentBatch
	// the number of expected updated pods
	desiredSurge := release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas
	// the number of current updated pods
	currentSurge := intstr.FromInt(0)
	if rc.object.Spec.UpdateStrategy.MaxSurge != nil {
		currentSurge = *rc.object.Spec.UpdateStrategy.MaxSurge
		if currentSurge == intstr.FromInt(1) {
			// currentSurge == intstr.FromInt(1) means that currentSurge is the initial value
			// if the value is indeed set by user, setting it to 0 still does no harm
			currentSurge = intstr.FromInt(0)
		}
	}
	desired, _ := intstr.GetScaledValueFromIntOrPercent(&desiredSurge, int(rc.Replicas), true)

	batchContext := &batchcontext.BatchContext{
		Pods:           rc.pods,
		RolloutID:      release.Spec.ReleasePlan.RolloutID,
		CurrentBatch:   currentBatch,
		UpdateRevision: release.Status.UpdateRevision,
		DesiredSurge:   desiredSurge,
		CurrentSurge:   currentSurge,
		// the following fields isused to check if batch is ready
		Replicas:               rc.Replicas,
		UpdatedReplicas:        rc.Status.UpdatedReplicas,
		UpdatedReadyReplicas:   rc.Status.UpdatedReadyReplicas,
		DesiredUpdatedReplicas: int32(desired),
		PlannedUpdatedReplicas: int32(desired),
	}
	// the number of no need update pods that marked before rollout
	// if noNeedUpdate := release.Status.CanaryStatus.NoNeedUpdateReplicas; noNeedUpdate != nil {
	// 	batchContext.FilterFunc = labelpatch.FilterPodsForUnorderedUpdate
	// }
	return batchContext, nil
}
