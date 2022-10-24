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
	"time"

	"github.com/openkruise/rollouts/api/v1alpha1"
)

type RolloutContext struct {
	Rollout   *v1alpha1.Rollout
	NewStatus *v1alpha1.RolloutStatus
	// related workload
	Workload *Workload
	// reconcile RequeueAfter recheckTime
	RecheckTime *time.Time
	// wait stable workload pods ready
	WaitReady bool
}
