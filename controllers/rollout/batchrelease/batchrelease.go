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

type BatchController interface {
	// VerifyBatchInitial create batchRelease beyond rollout crd
	VerifyBatchInitial() (bool, string, error)

	// PromoteBatch release workload in step(index)
	PromoteBatch(index int32) error

	// BatchReleaseState fetch batchRelease status
	BatchReleaseState() (*BatchReleaseState, error)

	// ResumeStableWorkload in multi-deployment scenarios, upgrade the stable deployment to the latest version after rollout complete
	// and wait pods are ready
	ResumeStableWorkload(checkReady bool) (bool, error)

	// Finalize clean up batchRelease
	// 1. delete canary deployments
	// 2. delete batchRelease CRD
	Finalize() (bool, error)
}
