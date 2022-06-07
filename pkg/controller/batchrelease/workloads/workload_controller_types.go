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

package workloads

import (
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type WorkloadEventType string

const (
	// IgnoreWorkloadEvent means workload event should be ignored.
	IgnoreWorkloadEvent WorkloadEventType = "workload-event-ignore"
	// WorkloadPodTemplateChanged means workload revision changed, should be stopped to execute batch release plan.
	WorkloadPodTemplateChanged WorkloadEventType = "workload-pod-template-changed"
	// WorkloadReplicasChanged means workload is scaling during rollout, should recalculate upgraded pods in current batch.
	WorkloadReplicasChanged WorkloadEventType = "workload-replicas-changed"
	// WorkloadStillReconciling means workload status is untrusted Untrustworthy, we should wait workload controller to reconcile.
	WorkloadStillReconciling WorkloadEventType = "workload-is-reconciling"
	// WorkloadHasGone means workload is deleted during rollout, we should do something finalizing works if this event occurs.
	WorkloadHasGone WorkloadEventType = "workload-has-gone"
	// WorkloadUnHealthy means workload is at some unexpected state that our controller cannot handle, we should stop reconcile.
	WorkloadUnHealthy WorkloadEventType = "workload-is-unhealthy"
	// WorkloadRollbackInBatch means workload is rollback according to BatchRelease batch plan.
	WorkloadRollbackInBatch WorkloadEventType = "workload-rollback-in-batch"
)

type workloadController struct {
	client           client.Client
	recorder         record.EventRecorder
	releasePlan      *v1alpha1.ReleasePlan
	releaseStatus    *v1alpha1.BatchReleaseStatus
	parentController *v1alpha1.BatchRelease
}

// WorkloadController is the interface that all type of cloneSet controller implements
type WorkloadController interface {
	// VerifyWorkload makes sure that the workload can be upgraded according to the release plan.
	// it returns 'true', if this verification is successful.
	// it returns 'false' or err != nil, if this verification is failed.
	// it returns not-empty error if the verification has something wrong, and should not retry.
	VerifyWorkload() (bool, error)

	// PrepareBeforeProgress make sure that the resource is ready to be progressed.
	// this function is tasked to do any initialization work on the resources.
	// it returns 'true' if the preparation is succeeded.
	// it returns 'false' if the preparation should retry.
	// it returns not-empty error if the preparation has something wrong, and should not retry.
	PrepareBeforeProgress() (bool, error)

	// UpgradeOneBatch tries to upgrade old replicas following the release plan.
	// it will upgrade the old replicas as the release plan allows in the current batch.
	// it returns 'true' if the progress is succeeded.
	// it returns 'false' if the progress should retry.
	// it returns not-empty error if the progress has something wrong, and should not retry.
	UpgradeOneBatch() (bool, error)

	// CheckOneBatchReady checks how many replicas are ready to serve requests in the current batch.
	// it returns 'true' if the batch has been ready.
	// it returns 'false' if the batch should be reset and recheck.
	// it returns not-empty error if the check operation has something wrong, and should not retry.
	CheckOneBatchReady() (bool, error)

	// FinalizeProgress makes sure the resources are in a good final state.
	// It might depend on if the rollout succeeded or not.
	// For example, we may remove the objects which created by batchRelease.
	// this function will always retry util it returns 'true'.
	// parameters:
	// - pause: 'nil' means keep current state, 'true' means pause workload, 'false' means do not pause workload
	// - cleanup: 'true' means clean up canary settings, 'false' means do not clean up.
	FinalizeProgress(cleanup bool) (bool, error)

	// SyncWorkloadInfo will watch and compare the status recorded in BatchRelease.Status
	// and the real-time workload info. If workload status is inconsistent with that recorded
	// in release.status, will return the corresponding WorkloadEventType and info.
	SyncWorkloadInfo() (WorkloadEventType, *util.WorkloadInfo, error)
}
