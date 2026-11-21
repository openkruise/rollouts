/*
Copyright 2026 The Kruise Authors.

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

package v1alpha1

import "testing"

func TestSetDefaultDeploymentStrategyDefaultsMaxSurge(t *testing.T) {
	strategy := &DeploymentStrategy{RollingStyle: PartitionRollingStyle}

	SetDefaultDeploymentStrategy(strategy)

	if strategy.RollingUpdate == nil {
		t.Fatalf("rollingUpdate = nil, want defaulted")
	}
	if strategy.RollingUpdate.MaxSurge == nil || strategy.RollingUpdate.MaxSurge.StrVal != "25%" {
		t.Fatalf("maxSurge = %v, want 25%%", strategy.RollingUpdate.MaxSurge)
	}
	if strategy.RollingUpdate.MaxUnavailable == nil || strategy.RollingUpdate.MaxUnavailable.StrVal != "25%" {
		t.Fatalf("maxUnavailable = %v, want 25%%", strategy.RollingUpdate.MaxUnavailable)
	}
}
