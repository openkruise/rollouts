/*
Copyright 2021.

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

type BatchReleaseState struct {
	// UpdateRevision the hash of the current pod template
	UpdateRevision string
	// StableRevision indicates the revision pods that has successfully rolled out
	StableRevision string
	// current batch step
	CurrentBatch         int32
	UpdatedReplicas      int32
	UpdatedReadyReplicas int32
	Paused               bool
	State                BatchRollingState
}

type BatchRollingState string

const (
	BatchInRollingState BatchRollingState = "batchInRolling"
	// BatchReadyState indicates that all the pods in the are upgraded and its state is ready
	BatchReadyState BatchRollingState = "batchReady"
)
