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

package batchrelease

import rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"

// BatchRelease is not the actual controller of the BatchRelease controller,
// but rather the ability to interact with the BatchRelease controller through the BatchRelease CRD to achieve a batch release
type BatchRelease interface {
	// Verify will create batchRelease or update batchRelease steps configuration and
	// return whether the batchRelease configuration is consistent with the rollout step
	Verify(index int32) (bool, error)

	// SyncRolloutID will sync rollout id from Rollout to BatchRelease
	SyncRolloutID(currentID string) error

	// 1. Promote release workload in step(index), 1<=index<=len(step)
	// 2. Promote will resume stable workload if the last batch(index=-1) is finished
	Promote(index int32, isRollback, checkReady bool) (bool, error)

	// FetchBatchRelease fetch batchRelease
	FetchBatchRelease() (*rolloutv1alpha1.BatchRelease, error)

	// Finalize clean up batchRelease
	// 1. delete canary deployments
	// 2. delete batchRelease CRD
	Finalize() (bool, error)
}
