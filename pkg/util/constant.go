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

package util

import (
	"fmt"

	"github.com/openkruise/rollouts/api/v1alpha1"
)

// For Rollout and BatchRelease
const (
	// BatchReleaseControlAnnotation is controller info about batchRelease when rollout
	BatchReleaseControlAnnotation = "batchrelease.rollouts.kruise.io/control-info"
	// InRolloutProgressingAnnotation marks workload as entering the rollout progressing process
	// and does not allow paused=false during this process. However, blueGreen is an exception,
	// which allows paused=false during progressing.
	InRolloutProgressingAnnotation = "rollouts.kruise.io/in-progressing"
	// RolloutHashAnnotation record observed rollout spec hash
	RolloutHashAnnotation = "rollouts.kruise.io/hash"
)

// For Workloads
const (
	// CanaryDeploymentLabel is to label canary deployment that is created by batchRelease controller
	CanaryDeploymentLabel = "rollouts.kruise.io/canary-deployment"
	// CanaryDeploymentFinalizer is a finalizer to resources patched by batchRelease controller
	CanaryDeploymentFinalizer = "finalizer.rollouts.kruise.io/batch-release"
	// KruiseRolloutFinalizer is a finalizer for deployment/service/ingress/gateway/etc
	KruiseRolloutFinalizer = "rollouts.kruise.io/rollout"
	// WorkloadTypeLabel is a label to identify workload type
	WorkloadTypeLabel = "rollouts.kruise.io/workload-type"
	// DeploymentRevisionAnnotation is the revision annotation of a deployment's replica sets which records its rollout sequence
	DeploymentRevisionAnnotation = "deployment.kubernetes.io/revision"
	// DaemonSetRevisionAnnotation contains revision information in key=value format
	// Including canary-revision and stable-revision
	DaemonSetRevisionAnnotation = "rollouts.kruise.io/daemonset-revision"
	// DaemonSetAdvancedControlAnnotation contains advanced control parameters in key=value format
	// Including partition and batch-revision
	// Partition is the number of pods with old version
	// Batch-revision is very important for continuous release, because update revision must be consistent with each partition
	DaemonSetAdvancedControlAnnotation = "rollouts.kruise.io/daemonset-advanced-control"
	// DaemonSetOriginalUpdateStrategy is the original update strategy type for the native daemonSet
	DaemonSetOriginalUpdateStrategy = "rollouts.kruise.io/daemonset-original-update-strategy"
)

const (
	TrafficRoutingFinalizer = "rollouts.kruise.io/trafficrouting"
)

// For Pods
const (
	// NoNeedUpdatePodLabel will be patched to pod when rollback in batches if the pods no need to rollback
	NoNeedUpdatePodLabel = "rollouts.kruise.io/no-need-update"
)

// For Others
const (
	// We omit vowels from the set of available characters to reduce the chances
	// of "bad words" being formed.
	alphanums = "bcdfghjklmnpqrstvwxz2456789"

	// CloneSetType DeploymentType and StatefulSetType are values to WorkloadTypeLabel
	CloneSetType    WorkloadType = "cloneset"
	DeploymentType  WorkloadType = "deployment"
	StatefulSetType WorkloadType = "statefulset"
	DaemonSetType   WorkloadType = "daemonset"

	AddFinalizerOpType    FinalizerOpType = "Add"
	RemoveFinalizerOpType FinalizerOpType = "Remove"
)

type WorkloadType string

type FinalizerOpType string

func ProgressingRolloutFinalizer(name string) string {
	return fmt.Sprintf("%s/%s", v1alpha1.ProgressingRolloutFinalizerPrefix, name)
}
