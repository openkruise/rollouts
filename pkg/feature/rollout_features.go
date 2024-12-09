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

package feature

import (
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/component-base/featuregate"

	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
)

const (
	// RolloutHistoryGate enable recording history for each rolling.
	RolloutHistoryGate featuregate.Feature = "RolloutHistory"
	// AdvancedDeploymentGate enable advanced deployment controller.
	AdvancedDeploymentGate featuregate.Feature = "AdvancedDeployment"
	// AppendServiceSelectorGate enable appending pod labels from PodTemplateMetadata to the canary service selector.
	AppendServiceSelectorGate featuregate.Feature = "AppendPodSelector"
)

var defaultFeatureGates = map[featuregate.Feature]featuregate.FeatureSpec{
	RolloutHistoryGate:        {Default: false, PreRelease: featuregate.Alpha},
	AdvancedDeploymentGate:    {Default: false, PreRelease: featuregate.Alpha},
	AppendServiceSelectorGate: {Default: false, PreRelease: featuregate.Alpha},
}

func init() {
	runtime.Must(utilfeature.DefaultMutableFeatureGate.Add(defaultFeatureGates))
}
