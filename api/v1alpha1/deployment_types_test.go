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

func TestSetDefaultDeploymentStrategySetsMaxSurge(t *testing.T) {
	strategy := &DeploymentStrategy{RollingStyle: PartitionRollingStyle}

	SetDefaultDeploymentStrategy(strategy)

	if strategy.RollingUpdate.MaxSurge == nil {
		t.Fatal("expected MaxSurge default to be set")
	}
	if strategy.RollingUpdate.MaxSurge.String() != "25%" {
		t.Fatalf("expected MaxSurge default 25%%, got %s", strategy.RollingUpdate.MaxSurge.String())
	}
	if strategy.RollingUpdate.MaxUnavailable == nil {
		t.Fatal("expected MaxUnavailable default to be set")
	}
	if strategy.RollingUpdate.MaxUnavailable.String() != "25%" {
		t.Fatalf("expected MaxUnavailable default 25%%, got %s", strategy.RollingUpdate.MaxUnavailable.String())
	}
}
