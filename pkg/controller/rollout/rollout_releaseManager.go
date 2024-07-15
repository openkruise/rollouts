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

package rollout

import (
	"github.com/openkruise/rollouts/api/v1beta1"
)

type ReleaseManager interface {
	// execute the core logic of a release step by step
	runCanary(c *RolloutContext) error
	// check the NextStepIndex field in status, if modifies detected, jump to target step
	doCanaryJump(c *RolloutContext) bool
	// called when user accomplishes a release / does a rollback, or disables/removes the Rollout Resource
	doCanaryFinalising(c *RolloutContext) (bool, error)
	// fetch the BatchRelease object
	fetchBatchRelease(ns, name string) (*v1beta1.BatchRelease, error)
	// remove the BatchRelease object
	removeBatchRelease(c *RolloutContext) (bool, error)
}
