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

package bluegreenstyle

import (
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/labelpatch"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type realBatchControlPlane struct {
	Interface
	client.Client
	record.EventRecorder
	patcher   labelpatch.LabelPatcher
	release   *v1beta1.BatchRelease
	newStatus *v1beta1.BatchReleaseStatus
}

type NewInterfaceFunc func(cli client.Client, key types.NamespacedName, gvk schema.GroupVersionKind) Interface

// NewControlPlane creates a new release controller with blue-green style to drive batch release state machine
func NewControlPlane(f NewInterfaceFunc, cli client.Client, recorder record.EventRecorder, release *v1beta1.BatchRelease, newStatus *v1beta1.BatchReleaseStatus, key types.NamespacedName, gvk schema.GroupVersionKind) *realBatchControlPlane {
	return &realBatchControlPlane{
		Client:        cli,
		EventRecorder: recorder,
		newStatus:     newStatus,
		Interface:     f(cli, key, gvk),
		release:       release.DeepCopy(),
		patcher:       labelpatch.NewLabelPatcher(cli, klog.KObj(release), release.Spec.ReleasePlan.Batches),
	}
}

func (rc *realBatchControlPlane) Initialize() error {
	controller, err := rc.BuildController()
	if err != nil {
		return err
	}

	// claim workload under our control
	err = controller.Initialize(rc.release)
	if err != nil {
		return err
	}

	// record revision and replicas
	workloadInfo := controller.GetWorkloadInfo()
	rc.newStatus.StableRevision = workloadInfo.Status.StableRevision
	rc.newStatus.UpdateRevision = workloadInfo.Status.UpdateRevision
	rc.newStatus.ObservedWorkloadReplicas = workloadInfo.Replicas
	return err
}

func (rc *realBatchControlPlane) patchPodLabels(batchContext *context.BatchContext) error {
	pods, err := rc.ListOwnedPods() // add pods to rc for patching pod batch labels
	if err != nil {
		return err
	}
	batchContext.Pods = pods
	return rc.patcher.PatchPodBatchLabel(batchContext)
}

func (rc *realBatchControlPlane) UpgradeBatch() error {
	controller, err := rc.BuildController()
	if err != nil {
		return err
	}

	if controller.GetWorkloadInfo().Replicas == 0 {
		return nil
	}

	batchContext, err := controller.CalculateBatchContext(rc.release)
	if err != nil {
		return err
	}
	klog.Infof("BatchRelease %v calculated context when upgrade batch: %s",
		klog.KObj(rc.release), batchContext.Log())

	err = controller.UpgradeBatch(batchContext)
	if err != nil {
		return err
	}

	return nil
}

func (rc *realBatchControlPlane) EnsureBatchPodsReadyAndLabeled() error {
	controller, err := rc.BuildController()
	if err != nil {
		return err
	}

	if controller.GetWorkloadInfo().Replicas == 0 {
		return nil
	}

	// do not countAndUpdateNoNeedUpdateReplicas when checking,
	// the target calculated should be consistent with UpgradeBatch.
	batchContext, err := controller.CalculateBatchContext(rc.release)
	if err != nil {
		return err
	}

	klog.Infof("BatchRelease %v calculated context when check batch ready: %s",
		klog.KObj(rc.release), batchContext.Log())
	if err = rc.patchPodLabels(batchContext); err != nil {
		klog.ErrorS(err, "failed to patch pod labels", "release", klog.KObj(rc.release))
	}
	return batchContext.IsBatchReady()
}

func (rc *realBatchControlPlane) Finalize() error {
	controller, err := rc.BuildController()
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	// release workload control info and clean up resources if it needs
	return controller.Finalize(rc.release)
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
