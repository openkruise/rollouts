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

package patch

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openkruise/rollouts/pkg/util"
)

func TestCommonPatch(t *testing.T) {
	condition := v1.PodCondition{Type: v1.ContainersReady, Status: v1.ConditionTrue, Message: "just for test"}
	patchReq := NewStrategicPatch().
		AddFinalizer("new-finalizer").
		RemoveFinalizer("old-finalizer").
		InsertLabel("new-label", "foo1").
		DeleteLabel("old-label").
		InsertAnnotation("new-annotation", "foo2").
		DeleteAnnotation("old-annotation").
		UpdatePodCondition(condition)

	expectedPatchBody := fmt.Sprintf(`{"metadata":{"$deleteFromPrimitiveList/finalizers":["old-finalizer"],"annotations":{"new-annotation":"foo2","old-annotation":null},"finalizers":["new-finalizer"],"labels":{"new-label":"foo1","old-label":null}},"status":{"conditions":[%s]}}`, util.DumpJSON(condition))

	if !reflect.DeepEqual(patchReq.String(), expectedPatchBody) {
		t.Fatalf("Not equal: \n%s \n%s", expectedPatchBody, patchReq.String())
	}
}

func TestMergePatchHelpers(t *testing.T) {
	patchReq := NewMergePatch().
		OverrideFinalizer([]string{"finalizer-a"}).
		InsertLabel("label-a", "value-a").
		DeleteLabel("label-b").
		InsertAnnotation("annotation-a", "value-b").
		DeleteAnnotation("annotation-b")

	if patchReq.Type() != types.MergePatchType {
		t.Fatalf("Type() = %s, want %s", patchReq.Type(), types.MergePatchType)
	}
	data, err := patchReq.Data(nil)
	if err != nil {
		t.Fatalf("Data() error = %v", err)
	}
	if string(data) != patchReq.String() {
		t.Fatalf("Data() = %s, want %s", string(data), patchReq.String())
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("patch json is malformed: %v", err)
	}
	metadata := got["metadata"].(map[string]interface{})
	finalizers := metadata["finalizers"].([]interface{})
	if finalizers[0] != "finalizer-a" {
		t.Fatalf("finalizers = %v, want finalizer-a", finalizers)
	}
	labels := metadata["labels"].(map[string]interface{})
	if labels["label-a"] != "value-a" || labels["label-b"] != nil {
		t.Fatalf("labels = %v", labels)
	}
	annotations := metadata["annotations"].(map[string]interface{})
	if annotations["annotation-a"] != "value-b" || annotations["annotation-b"] != nil {
		t.Fatalf("annotations = %v", annotations)
	}
}

func TestDeploymentPatchHelpers(t *testing.T) {
	progressDeadlineSeconds := int32(600)
	maxSurge := intstr.FromString("25%")
	maxUnavailable := intstr.FromInt(1)
	strategy := apps.DeploymentStrategy{Type: apps.RollingUpdateDeploymentStrategyType}

	strategyPatch := NewDeploymentPatch().UpdateStrategy(strategy)
	var strategyOnly map[string]interface{}
	if err := json.Unmarshal([]byte(strategyPatch.String()), &strategyOnly); err != nil {
		t.Fatalf("strategy patch json is malformed: %v", err)
	}
	if strategyOnly["spec"].(map[string]interface{})["strategy"].(map[string]interface{})["type"] != string(apps.RollingUpdateDeploymentStrategyType) {
		t.Fatalf("strategy patch = %v", strategyOnly)
	}

	patchReq := NewDeploymentPatch().
		UpdatePaused(true).
		UpdateMinReadySeconds(30).
		UpdateProgressDeadlineSeconds(&progressDeadlineSeconds).
		UpdateMaxSurge(&maxSurge).
		UpdateMaxUnavailable(&maxUnavailable)

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(patchReq.String()), &got); err != nil {
		t.Fatalf("patch json is malformed: %v", err)
	}
	spec := got["spec"].(map[string]interface{})
	if spec["paused"] != true {
		t.Fatalf("paused = %v, want true", spec["paused"])
	}
	if spec["minReadySeconds"] != float64(30) {
		t.Fatalf("minReadySeconds = %v, want 30", spec["minReadySeconds"])
	}
	if spec["progressDeadlineSeconds"] != float64(600) {
		t.Fatalf("progressDeadlineSeconds = %v, want 600", spec["progressDeadlineSeconds"])
	}
	rollingUpdate := spec["strategy"].(map[string]interface{})["rollingUpdate"].(map[string]interface{})
	if rollingUpdate["maxSurge"] != "25%" {
		t.Fatalf("maxSurge = %v", rollingUpdate["maxSurge"])
	}
	if rollingUpdate["maxUnavailable"] != float64(1) {
		t.Fatalf("maxUnavailable = %v", rollingUpdate["maxUnavailable"])
	}
}

func TestDeploymentPatchUpdateRecreateStrategyClearsRollingUpdate(t *testing.T) {
	patchReq := NewDeploymentPatch().UpdateRecreateStrategy()

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(patchReq.String()), &got); err != nil {
		t.Fatalf("patch json is malformed: %v", err)
	}
	strategy := got["spec"].(map[string]interface{})["strategy"].(map[string]interface{})
	if strategy["type"] != string(apps.RecreateDeploymentStrategyType) {
		t.Fatalf("strategy.type = %v, want Recreate", strategy["type"])
	}
	if _, ok := strategy["rollingUpdate"]; !ok {
		t.Fatalf("rollingUpdate field missing, want explicit null")
	}
	if strategy["rollingUpdate"] != nil {
		t.Fatalf("rollingUpdate = %v, want nil", strategy["rollingUpdate"])
	}
}

func TestClonesetPatchHelpers(t *testing.T) {
	partition := intstr.FromInt(3)
	maxSurge := intstr.FromString("20%")
	maxUnavailable := intstr.FromInt(1)

	patchReq := NewClonesetPatch().
		UpdateMinReadySeconds(10).
		UpdatePaused(true).
		UpdatePartiton(&partition).
		UpdateMaxSurge(&maxSurge).
		UpdateMaxUnavailable(&maxUnavailable)

	if patchReq.Type() != types.MergePatchType {
		t.Fatalf("Type() = %s, want %s", patchReq.Type(), types.MergePatchType)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(patchReq.String()), &got); err != nil {
		t.Fatalf("patch json is malformed: %v", err)
	}
	spec := got["spec"].(map[string]interface{})
	if spec["minReadySeconds"] != float64(10) {
		t.Fatalf("minReadySeconds = %v, want 10", spec["minReadySeconds"])
	}
	updateStrategy := spec["updateStrategy"].(map[string]interface{})
	if updateStrategy["paused"] != true {
		t.Fatalf("paused = %v, want true", updateStrategy["paused"])
	}
	if updateStrategy["partition"] != float64(3) {
		t.Fatalf("partition = %v", updateStrategy["partition"])
	}
	if updateStrategy["maxSurge"] != "20%" {
		t.Fatalf("maxSurge = %v", updateStrategy["maxSurge"])
	}
	if updateStrategy["maxUnavailable"] != float64(1) {
		t.Fatalf("maxUnavailable = %v", updateStrategy["maxUnavailable"])
	}
}
