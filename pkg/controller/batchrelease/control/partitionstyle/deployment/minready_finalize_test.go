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

package deployment

import (
	"strings"
	"testing"
)

func TestMinReadyFinalizeRestoresOriginalValues(t *testing.T) {
	deployment := newInflatedMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds:         "7",
		AnnotationOriginalProgressDeadlineSeconds: "60",
		AnnotationOriginalMaxUnavailable:          "25%",
		AnnotationOriginalMaxSurge:                "1",
	}
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Finalize(releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	if got.Spec.MinReadySeconds != 7 {
		t.Fatalf("minReadySeconds = %d, want 7", got.Spec.MinReadySeconds)
	}
	if got.Spec.ProgressDeadlineSeconds == nil || *got.Spec.ProgressDeadlineSeconds != 60 {
		t.Fatalf("progressDeadlineSeconds = %v, want 60", got.Spec.ProgressDeadlineSeconds)
	}
	if unavailable := got.Spec.Strategy.RollingUpdate.MaxUnavailable; unavailable == nil || unavailable.StrVal != "25%" {
		t.Fatalf("maxUnavailable = %v, want 25%%", unavailable)
	}
	if surge := got.Spec.Strategy.RollingUpdate.MaxSurge; surge == nil || surge.IntVal != 1 {
		t.Fatalf("maxSurge = %v, want 1", surge)
	}
	for _, key := range AllOriginalAnnotations {
		if _, ok := got.Annotations[key]; ok {
			t.Fatalf("annotation %s still exists", key)
		}
	}
}

func TestMinReadyFinalizeRestoresKubernetesDefaults(t *testing.T) {
	deployment := newInflatedMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds:         "0",
		AnnotationOriginalProgressDeadlineSeconds: AnnotationValueKubernetesDefault,
		AnnotationOriginalMaxUnavailable:          AnnotationValueKubernetesDefault,
		AnnotationOriginalMaxSurge:                AnnotationValueKubernetesDefault,
	}
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Finalize(releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	if got.Spec.MinReadySeconds != 0 {
		t.Fatalf("minReadySeconds = %d, want 0", got.Spec.MinReadySeconds)
	}
	if got.Spec.ProgressDeadlineSeconds != nil {
		t.Fatalf("progressDeadlineSeconds = %v, want nil", got.Spec.ProgressDeadlineSeconds)
	}
	if got.Spec.Strategy.RollingUpdate != nil {
		t.Fatalf("rollingUpdate = %v, want nil", got.Spec.Strategy.RollingUpdate)
	}
}

func TestMinReadyFinalizeNoopWhenAnnotationsAbsentAndFieldsRestored(t *testing.T) {
	deployment := newMinReadyDeployment()
	deployment.Annotations = nil
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Finalize(releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	if got.Spec.MinReadySeconds != 7 {
		t.Fatalf("minReadySeconds = %d, want original value 7", got.Spec.MinReadySeconds)
	}
}

func TestMinReadyFinalizeRejectsMissingAnnotationsWhileFieldsInflated(t *testing.T) {
	deployment := newInflatedMinReadyDeployment()
	deployment.Annotations = nil
	control := newBuiltMinReadyControl(t, deployment)

	err := control.Finalize(releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), "annotation state missing") {
		t.Fatalf("Finalize error = %v, want missing annotation state error", err)
	}

	got := fetchMinReadyDeployment(t, control)
	assertMinReadyInflated(t, got)
}

func TestMinReadyFinalizeRejectsPartialAnnotations(t *testing.T) {
	deployment := newInflatedMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds: "7",
	}
	control := newBuiltMinReadyControl(t, deployment)

	err := control.Finalize(releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), AnnotationOriginalProgressDeadlineSeconds) {
		t.Fatalf("Finalize error = %v, want missing annotation error", err)
	}
	got := fetchMinReadyDeployment(t, control)
	assertMinReadyInflated(t, got)
}

func TestMinReadyFinalizeRejectsMalformedAnnotations(t *testing.T) {
	deployment := newInflatedMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds:         "7",
		AnnotationOriginalProgressDeadlineSeconds: "bad",
		AnnotationOriginalMaxUnavailable:          "25%",
		AnnotationOriginalMaxSurge:                "1",
	}
	control := newBuiltMinReadyControl(t, deployment)

	err := control.Finalize(releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), "malformed int32") {
		t.Fatalf("Finalize error = %v, want malformed int32 error", err)
	}
	got := fetchMinReadyDeployment(t, control)
	assertMinReadyInflated(t, got)
}
