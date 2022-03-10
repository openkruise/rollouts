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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type WorkloadChangeEventType string

const (
	IgnoreWorkloadEvent        WorkloadChangeEventType = "workload-not-cared"
	WorkloadRollback           WorkloadChangeEventType = "workload-rollback"
	WorkloadPodTemplateChanged WorkloadChangeEventType = "workload-pod-template-changed"
	WorkloadReplicasChanged    WorkloadChangeEventType = "workload-replicas-changed"
	WorkloadStillReconciling   WorkloadChangeEventType = "workload-is-reconciling"
	WorkloadUnHealthy          WorkloadChangeEventType = "workload-is-unhealthy"
)

type WorkloadStatus struct {
	Replicas             int32
	ReadyReplicas        int32
	UpdatedReplicas      int32
	UpdatedReadyReplicas int32
	ObservedGeneration   int64
}

type WorkloadInfo struct {
	Paused         bool
	Replicas       *int32
	UpdateRevision *string
	Status         *WorkloadStatus
	Metadata       *metav1.ObjectMeta
}

type workloadController struct {
	client           client.Client
	recorder         record.EventRecorder
	releasePlan      *v1alpha1.ReleasePlan
	releaseStatus    *v1alpha1.BatchReleaseStatus
	parentController *v1alpha1.BatchRelease
}

// WorkloadController is the interface that all type of cloneSet controller implements
type WorkloadController interface {
	// IfNeedToProgress makes sure that the resources can be upgraded according to the release plan.
	// it returns 'true' if the verification is succeeded.
	// it returns 'false' if the verification should retry.
	// it returns not-empty error if the verification has something wrong, and should not retry.
	IfNeedToProgress() (bool, error)

	// PrepareBeforeProgress make sure that the resource is ready to be progressed.
	// this function is tasked to do any initialization work on the resources.
	// it returns 'true' if the preparation is succeeded.
	// it returns 'false' if the preparation should retry.
	// it returns not-empty error if the preparation has something wrong, and should not retry.
	PrepareBeforeProgress() (bool, error)

	// ProgressOneBatchReplicas tries to upgrade old replicas following the release plan.
	// it will upgrade the old replicas as the release plan allows in the current batch.
	// it returns 'true' if the progress is succeeded.
	// it returns 'false' if the progress should retry.
	// it returns not-empty error if the progress has something wrong, and should not retry.
	ProgressOneBatchReplicas() (bool, error)

	// CheckOneBatchReplicas checks how many replicas are ready to serve requests in the current batch.
	// it returns 'true' if the batch has been ready.
	// it returns 'false' if the batch should be reset and recheck.
	// it returns not-empty error if the check operation has something wrong, and should not retry.
	CheckOneBatchReplicas() (bool, error)

	// FinalizeOneBatch makes sure that the rollout can start the next batch
	// it returns 'true' if the operation is succeeded.
	// it returns 'false' if the operation should be retried.
	// it returns not-empty error if the check operation has something wrong, and should not retry.
	FinalizeOneBatch() (bool, error)

	// FinalizeProgress makes sure the resources are in a good final state.
	// It might depend on if the rollout succeeded or not.
	// For example, we may remove the objects which created by batchRelease.
	// this function will always retry util it returns 'true'.
	// parameters:
	// - pause: 'nil' means keep current state, 'true' means pause workload, 'false' means do not pause workload
	// - cleanup: 'true' means clean up canary settings, 'false' means do not clean up.
	FinalizeProgress(pause *bool, cleanup bool) bool

	// SyncWorkloadInfo will watch and compare the status recorded in BatchRelease.Status
	// and the real-time workload info. If workload status is inconsistent with that recorded
	// in release.status, will return the corresponding WorkloadChangeEventType and info.
	SyncWorkloadInfo() (WorkloadChangeEventType, *WorkloadInfo, error)
}
