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
	"fmt"
	"reflect"
	"testing"

	"github.com/openkruise/rollouts/pkg/util"
	v1 "k8s.io/api/core/v1"
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
