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

package canarystyle

import (
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
)

type Interface interface {
	CanaryInterface
	StableInterface
	// BuildStableController will get stable workload object and parse
	// stable workload info, and return a controller for stable workload.
	BuildStableController() (StableInterface, error)
	// BuildCanaryController will get canary workload object and parse
	// canary workload info, and return a controller for canary workload.
	BuildCanaryController(release *v1beta1.BatchRelease) (CanaryInterface, error)
	// CalculateBatchContext calculate the current batch context according to
	// our release plan and the statues of stable workload and canary workload.
	CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error)
}

// CanaryInterface contains the methods about canary workload
type CanaryInterface interface {
	// GetCanaryInfo return the information about canary workload
	GetCanaryInfo() *util.WorkloadInfo
	// UpgradeBatch upgrade canary workload according to current batch context
	UpgradeBatch(*batchcontext.BatchContext) error
	// Create creates canary workload before rolling out
	Create(controller *v1beta1.BatchRelease) error
	// Delete deletes canary workload after rolling out
	Delete(controller *v1beta1.BatchRelease) error
}

// StableInterface contains the methods about stable workload
type StableInterface interface {
	// GetStableInfo return the information about stable workload
	GetStableInfo() *util.WorkloadInfo
	// Initialize claim the stable workload is under rollout control
	Initialize(controller *v1beta1.BatchRelease) error
	// Finalize do something after rolling out, for example:
	// - free the stable workload from rollout control;
	// - resume stable workload and wait all pods updated if we need.
	Finalize(controller *v1beta1.BatchRelease) error
}
