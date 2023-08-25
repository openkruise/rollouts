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
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
)

type Interface interface {
	// BuildController will get workload object and parse workload info,
	// and return a initialized controller for workload.
	BuildController() (Interface, error)
	// GetWorkloadInfo return workload information.
	GetWorkloadInfo() *util.WorkloadInfo
	// ListOwnedPods fetch the pods owned by the workload.
	// Note that we should list pod only if we really need it.
	ListOwnedPods() ([]*corev1.Pod, error)
	// CalculateBatchContext calculate current batch context
	// according to release plan and current status of workload.
	CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error)

	// Initialize do something before rolling out, for example:
	// - claim the workload is under our control;
	// - other things related with specific type of workload, such as 100% partition settings.
	Initialize(release *v1beta1.BatchRelease) error
	// UpgradeBatch upgrade workload according current batch context.
	UpgradeBatch(ctx *batchcontext.BatchContext) error
	// Finalize do something after rolling out, for example:
	// - free the stable workload from rollout control;
	// - resume workload if we need.
	Finalize(release *v1beta1.BatchRelease) error
}
