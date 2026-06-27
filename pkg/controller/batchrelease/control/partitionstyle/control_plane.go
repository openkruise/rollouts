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

package partitionstyle

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/labelpatch"
	"github.com/openkruise/rollouts/pkg/util"
)

type realBatchControlPlane struct {
	Interface
	client.Client
	record.EventRecorder
	patcher   labelpatch.LabelPatcher
	ctx       context.Context
	release   *v1beta1.BatchRelease
	newStatus *v1beta1.BatchReleaseStatus
}

type NewInterfaceFunc func(cli client.Client, key types.NamespacedName, gvk schema.GroupVersionKind) Interface

// NewControlPlane creates a new release controller with partitioned-style to drive batch release state machine
func NewControlPlane(ctx context.Context, f NewInterfaceFunc, cli client.Client, recorder record.EventRecorder, release *v1beta1.BatchRelease, newStatus *v1beta1.BatchReleaseStatus, key types.NamespacedName, gvk schema.GroupVersionKind) *realBatchControlPlane {
	return &realBatchControlPlane{
		Client:        cli,
		EventRecorder: recorder,
		newStatus:     newStatus,
		Interface:     f(cli, key, gvk),
		ctx:           nonNilContext(ctx),
		release:       release.DeepCopy(),
		patcher:       labelpatch.NewLabelPatcher(cli, klog.KObj(release), release.Spec.ReleasePlan.Batches),
	}
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func (rc *realBatchControlPlane) bindMinReadyStatus(controller Interface) {
	if binder, ok := controller.(MinReadyStatusBinder); ok {
		binder.BindMinReadyStatus(rc.release, rc.newStatus, rc.EventRecorder)
	}
}

func (rc *realBatchControlPlane) reportOperationFailed(controller Interface, reason string, err error) {
	if err == nil {
		return
	}
	if lifecycle, ok := controller.(MinReadyLifecycle); ok {
		lifecycle.RecordOperationFailed(reason, err)
		return
	}
	klog.ErrorS(err, "Partition-style control plane failed", "release", klog.KObj(rc.release), "reason", reason)
}

func (rc *realBatchControlPlane) Initialize() (err error) {
	controller := rc.Interface
	var reportErr error
	defer func() {
		rc.reportOperationFailed(controller, "MinReadyInitializeFailed", reportErr)
	}()

	controller, err = rc.BuildController()
	if err != nil {
		reportErr = err
		return err
	}
	rc.bindMinReadyStatus(controller)

	// claim workload under our control
	err = controller.Initialize(rc.ctx, rc.release)
	if err != nil {
		reportErr = err
		return err
	}
	if lifecycle, ok := controller.(MinReadyLifecycle); ok {
		lifecycle.RecordInitialized()
	}

	// record revision and replicas
	workloadInfo := controller.GetWorkloadInfo()
	rc.newStatus.StableRevision = workloadInfo.Status.StableRevision
	rc.newStatus.UpdateRevision = workloadInfo.Status.UpdateRevision
	rc.newStatus.ObservedWorkloadReplicas = workloadInfo.Replicas

	// mark the pods that no need to update if it needs
	noNeedUpdateReplicas, err := rc.markNoNeedUpdatePodsIfNeeds()
	if noNeedUpdateReplicas != nil && err == nil {
		rc.newStatus.CanaryStatus.NoNeedUpdateReplicas = noNeedUpdateReplicas
	}
	return err
}

func (rc *realBatchControlPlane) UpgradeBatch() (err error) {
	controller := rc.Interface
	var reportErr error
	defer func() {
		rc.reportOperationFailed(controller, "MinReadyBatchingFailed", reportErr)
	}()

	controller, err = rc.BuildController()
	if err != nil {
		reportErr = err
		return err
	}
	rc.bindMinReadyStatus(controller)

	if controller.GetWorkloadInfo().Replicas == 0 {
		if lifecycle, ok := controller.(MinReadyLifecycle); ok {
			lifecycle.RecordZeroReplicaBatching()
		}
		return nil
	}

	err = rc.countAndUpdateNoNeedUpdateReplicas()
	if err != nil {
		reportErr = err
		return err
	}

	batchContext, err := controller.CalculateBatchContext(rc.release)
	if err != nil {
		reportErr = err
		return err
	}
	klog.Infof("BatchRelease %v calculated context when upgrade batch: %s",
		klog.KObj(rc.release), batchContext.Log())

	err = controller.UpgradeBatch(rc.ctx, batchContext)
	if err != nil {
		reportErr = err
		return err
	}

	if err := rc.patcher.PatchPodBatchLabel(batchContext); err != nil {
		reportErr = err
		return err
	}
	if lifecycle, ok := controller.(MinReadyLifecycle); ok {
		lifecycle.RecordBatchAdvanced()
	}
	return nil
}

func (rc *realBatchControlPlane) EnsureBatchPodsReadyAndLabeled() (err error) {
	controller := rc.Interface
	var reportErr error
	defer func() {
		rc.reportOperationFailed(controller, "MinReadyBatchingFailed", reportErr)
	}()

	controller, err = rc.BuildController()
	if err != nil {
		reportErr = err
		return err
	}
	rc.bindMinReadyStatus(controller)

	if controller.GetWorkloadInfo().Replicas == 0 {
		if lifecycle, ok := controller.(MinReadyLifecycle); ok {
			lifecycle.RecordZeroReplicaBatchReady()
		}
		return nil
	}

	// do not countAndUpdateNoNeedUpdateReplicas when checking,
	// the target calculated should be consistent with UpgradeBatch.
	batchContext, err := controller.CalculateBatchContext(rc.release)
	if err != nil {
		reportErr = err
		return err
	}

	klog.Infof("BatchRelease %v calculated context when check batch ready: %s",
		klog.KObj(rc.release), batchContext.Log())

	if reconciler, ok := controller.(MinReadyDriftReconciler); ok {
		if err := reconciler.ReconcileMaxUnavailableDrift(rc.ctx, batchContext); err != nil {
			reportErr = err
			return err
		}
	}

	if err := batchContext.IsBatchReady(); err != nil {
		if lifecycle, ok := controller.(MinReadyLifecycle); ok {
			lifecycle.ObserveBatchWait()
		}
		return err
	}
	if lifecycle, ok := controller.(MinReadyLifecycle); ok {
		lifecycle.RecordBatchReady()
	}
	return nil
}

func (rc *realBatchControlPlane) Finalize() (err error) {
	controller := rc.Interface
	var reportErr error
	defer func() {
		rc.reportOperationFailed(controller, "MinReadyFinalizeFailed", reportErr)
	}()

	controller, err = rc.BuildController()
	if err != nil {
		if err := client.IgnoreNotFound(err); err != nil {
			reportErr = err
			return err
		}
		return nil
	}
	rc.bindMinReadyStatus(controller)

	// release workload control info and clean up resources if it needs
	if err := controller.Finalize(rc.ctx, rc.release); err != nil {
		reportErr = err
		return err
	}
	if lifecycle, ok := controller.(MinReadyLifecycle); ok {
		lifecycle.RecordFinalized()
	}
	return nil
}

func (rc *realBatchControlPlane) SyncWorkloadInformation() (control.WorkloadEventType, *util.WorkloadInfo, error) {
	// ignore the sync if the release plan is deleted
	if rc.release.DeletionTimestamp != nil {
		return control.WorkloadNormalState, nil, nil
	}

	controller, err := rc.BuildController()
	if err != nil {
		if errors.IsNotFound(err) {
			return control.WorkloadHasGone, nil, err
		}
		return control.WorkloadUnknownState, nil, err
	}

	workloadInfo := controller.GetWorkloadInfo()
	if !workloadInfo.IsStable() {
		klog.Infof("Workload(%v) still reconciling, waiting for it to complete, generation: %v, observed: %v",
			workloadInfo.LogKey, workloadInfo.Generation, workloadInfo.Status.ObservedGeneration)
		return control.WorkloadStillReconciling, workloadInfo, nil
	}

	if workloadInfo.IsPromoted() {
		klog.Infof("Workload(%v) has been promoted, no need to rollout again actually, replicas: %v, updated: %v",
			workloadInfo.LogKey, workloadInfo.Replicas, workloadInfo.Status.UpdatedReadyReplicas)
		return control.WorkloadNormalState, workloadInfo, nil
	}

	if workloadInfo.IsScaling(rc.newStatus.ObservedWorkloadReplicas) {
		klog.Warningf("Workload(%v) replicas is modified, replicas from: %v to -> %v",
			workloadInfo.LogKey, rc.newStatus.ObservedWorkloadReplicas, workloadInfo.Replicas)
		return control.WorkloadReplicasChanged, workloadInfo, nil
	}

	if workloadInfo.IsRollback(rc.newStatus.StableRevision, rc.newStatus.UpdateRevision) {
		klog.Warningf("Workload(%v) is rolling back", workloadInfo.LogKey)
		return control.WorkloadRollbackInBatch, workloadInfo, nil
	}

	if workloadInfo.IsRevisionNotEqual(rc.newStatus.UpdateRevision) {
		klog.Warningf("Workload(%v) updateRevision is modified, updateRevision from: %v to -> %v",
			workloadInfo.LogKey, rc.newStatus.UpdateRevision, workloadInfo.Status.UpdateRevision)
		return control.WorkloadPodTemplateChanged, workloadInfo, nil
	}

	return control.WorkloadNormalState, workloadInfo, nil
}

/* --------------------------------------------
   The functions below are helper functions
----------------------------------------------- */

// MarkNoNeedUpdatePods makes sure that the updated pods have been patched no-need-update label.
// return values:
// - *int32: how many pods have been patched;
// - err: whether error occurs.
func (rc *realBatchControlPlane) markNoNeedUpdatePodsIfNeeds() (*int32, error) {
	// currently, we only support rollback scene, in the future, we may support more scenes.
	if rc.release.Annotations[v1alpha1.RollbackInBatchAnnotation] == "" {
		return nil, nil
	}
	// currently, if rollout-id is not set, it is no scene which require patch this label
	// we only return the current updated replicas.
	if rc.release.Spec.ReleasePlan.RolloutID == "" {
		return pointer.Int32(rc.newStatus.CanaryStatus.UpdatedReplicas), nil
	}

	var err error
	var pods []*v1.Pod
	var filterPods []*v1.Pod
	noNeedUpdateReplicas := int32(0)
	rolloutID := rc.release.Spec.ReleasePlan.RolloutID
	if pods, err = rc.ListOwnedPods(); err != nil {
		return nil, err
	}

	for i := range pods {
		if !pods[i].DeletionTimestamp.IsZero() {
			continue
		}
		if !util.IsConsistentWithRevision(pods[i].GetLabels(), rc.newStatus.UpdateRevision) {
			continue
		}
		if pods[i].Labels[util.NoNeedUpdatePodLabel] == rolloutID {
			noNeedUpdateReplicas++
			continue
		}
		filterPods = append(filterPods, pods[i])
	}

	if len(filterPods) == 0 {
		return &noNeedUpdateReplicas, nil
	}

	for _, pod := range filterPods {
		clone := util.GetEmptyObjectWithKey(pod)
		body := fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s"}}}`, util.NoNeedUpdatePodLabel, rolloutID)
		err = rc.Patch(rc.ctx, clone, client.RawPatch(types.StrategicMergePatchType, []byte(body)))
		if err != nil {
			klog.Errorf("Failed to patch no-need-update label(%v) to pod %v, err: %v", rolloutID, klog.KObj(pod), err)
			return &noNeedUpdateReplicas, err
		} else {
			klog.Infof("Succeeded to patch no-need-update label(%v) to pod %v", rolloutID, klog.KObj(pod))
		}
		noNeedUpdateReplicas++
	}

	return &noNeedUpdateReplicas, fmt.Errorf("initilization not yet: patch and find %d pods with no-need-update-label", noNeedUpdateReplicas)
}

// countAndUpdateNoNeedUpdateReplicas will count the pods with no-need-update
// label and update corresponding field for BatchRelease
func (rc *realBatchControlPlane) countAndUpdateNoNeedUpdateReplicas() error {
	if rc.release.Spec.ReleasePlan.RolloutID == "" || rc.release.Status.CanaryStatus.NoNeedUpdateReplicas == nil {
		return nil
	}

	pods, err := rc.ListOwnedPods()
	if err != nil {
		return err
	}

	noNeedUpdateReplicas := int32(0)
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		if !util.IsConsistentWithRevision(pod.GetLabels(), rc.release.Status.UpdateRevision) {
			continue
		}
		id, ok := pod.Labels[util.NoNeedUpdatePodLabel]
		if ok && id == rc.release.Spec.ReleasePlan.RolloutID {
			noNeedUpdateReplicas++
		}
	}

	// refresh newStatus for updating
	rc.newStatus.CanaryStatus.NoNeedUpdateReplicas = &noNeedUpdateReplicas
	// refresh release.Status for calculation of BatchContext
	rc.release.Status.CanaryStatus.NoNeedUpdateReplicas = &noNeedUpdateReplicas
	return nil
}
