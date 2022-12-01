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

package expectations

import (
	"testing"
)

func TestResourceExpectations(t *testing.T) {
	e := NewResourceExpectations()
	controllerKey01 := "default/cs01"
	controllerKey02 := "default/cs02"
	pod01 := "pod01"
	pod02 := "pod02"

	e.Expect(controllerKey01, Create, pod01)
	e.Expect(controllerKey01, Create, pod02)
	e.Expect(controllerKey01, Delete, pod01)
	if ok, _, _ := e.SatisfiedExpectations(controllerKey01); ok {
		t.Fatalf("expected not satisfied")
	}

	e.Observe(controllerKey01, Create, pod02)
	e.Observe(controllerKey01, Create, pod01)
	if ok, _, _ := e.SatisfiedExpectations(controllerKey01); ok {
		t.Fatalf("expected not satisfied")
	}

	e.Observe(controllerKey02, Delete, pod01)
	if ok, _, _ := e.SatisfiedExpectations(controllerKey01); ok {
		t.Fatalf("expected not satisfied")
	}

	e.Observe(controllerKey01, Delete, pod01)
	if ok, _, _ := e.SatisfiedExpectations(controllerKey01); !ok {
		t.Fatalf("expected satisfied")
	}

	e.Observe(controllerKey01, Create, pod01)
	e.Observe(controllerKey01, Create, pod02)
	e.DeleteExpectations(controllerKey01)
	if ok, _, _ := e.SatisfiedExpectations(controllerKey01); !ok {
		t.Fatalf("expected satisfied")
	}
}
