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

package mutating

import (
	"testing"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	appsv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
)

func TestEnrollMinReadyDeploymentSnapshotsAndInflates(t *testing.T) {
	deployment := newWebhookMinReadyDeployment()

	if err := enrollMinReadyDeployment(deployment); err != nil {
		t.Fatalf("enrollMinReadyDeployment failed: %v", err)
	}

	assertWebhookMinReadyAnnotation(t, deployment, appsv1beta1.MinReadyOriginalMinReadySecondsAnnotation, "7")
	assertWebhookMinReadyAnnotation(t, deployment, appsv1beta1.MinReadyOriginalProgressDeadlineSecondsAnnotation, "60")
	assertWebhookMinReadyAnnotation(t, deployment, appsv1beta1.MinReadyOriginalMaxUnavailableAnnotation, "25%")
	assertWebhookMinReadyInflated(t, deployment)
}

func TestEnrollMinReadyDeploymentValidatesExistingAnnotations(t *testing.T) {
	deployment := newWebhookInflatedMinReadyDeployment()
	addWebhookMinReadyOriginalAnnotations(deployment)
	original := deployment.Annotations[appsv1beta1.MinReadyOriginalMinReadySecondsAnnotation]

	if err := enrollMinReadyDeployment(deployment); err != nil {
		t.Fatalf("enrollMinReadyDeployment failed: %v", err)
	}

	if deployment.Annotations[appsv1beta1.MinReadyOriginalMinReadySecondsAnnotation] != original {
		t.Fatalf("original annotation was rewritten: %q -> %q", original, deployment.Annotations[appsv1beta1.MinReadyOriginalMinReadySecondsAnnotation])
	}
}

func TestEnrollMinReadyDeploymentRefreshesAvailabilityAnnotationsForContinuousRelease(t *testing.T) {
	deployment := newWebhookInflatedMinReadyDeployment()
	addWebhookMinReadyOriginalAnnotations(deployment)
	deployment.Spec.MinReadySeconds = 9
	deployment.Spec.ProgressDeadlineSeconds = pointer.Int32(90)

	if err := enrollMinReadyDeployment(deployment); err != nil {
		t.Fatalf("enrollMinReadyDeployment failed: %v", err)
	}

	assertWebhookMinReadyAnnotation(t, deployment, appsv1beta1.MinReadyOriginalMinReadySecondsAnnotation, "9")
	assertWebhookMinReadyAnnotation(t, deployment, appsv1beta1.MinReadyOriginalProgressDeadlineSecondsAnnotation, "90")
	assertWebhookMinReadyAnnotation(t, deployment, appsv1beta1.MinReadyOriginalMaxUnavailableAnnotation, "25%")
	assertWebhookMinReadyInflated(t, deployment)
}

func TestEnrollMinReadyDeploymentRejectsRecreate(t *testing.T) {
	deployment := newWebhookMinReadyDeployment()
	deployment.Spec.Strategy.Type = apps.RecreateDeploymentStrategyType

	if err := enrollMinReadyDeployment(deployment); err == nil {
		t.Fatalf("enrollMinReadyDeployment accepted Recreate strategy, want error")
	}
}

func newWebhookMinReadyDeployment() *apps.Deployment {
	progressDeadline := int32(60)
	maxUnavailable := intstr.FromString("25%")
	maxSurge := intstr.FromInt(1)
	return &apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}},
		Spec: apps.DeploymentSpec{
			MinReadySeconds:         7,
			ProgressDeadlineSeconds: &progressDeadline,
			Strategy: apps.DeploymentStrategy{
				Type: apps.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
					MaxSurge:       &maxSurge,
				},
			},
		},
	}
}

func newWebhookInflatedMinReadyDeployment() *apps.Deployment {
	deployment := newWebhookMinReadyDeployment()
	inflateMinReadyDeploymentStrategy(deployment)
	return deployment
}

func addWebhookMinReadyOriginalAnnotations(deployment *apps.Deployment) {
	if deployment.Annotations == nil {
		deployment.Annotations = map[string]string{}
	}
	deployment.Annotations[appsv1beta1.MinReadyOriginalMinReadySecondsAnnotation] = "7"
	deployment.Annotations[appsv1beta1.MinReadyOriginalProgressDeadlineSecondsAnnotation] = "60"
	deployment.Annotations[appsv1beta1.MinReadyOriginalMaxUnavailableAnnotation] = "25%"
}

func assertWebhookMinReadyAnnotation(t *testing.T, deployment *apps.Deployment, key, want string) {
	t.Helper()
	if got := deployment.Annotations[key]; got != want {
		t.Fatalf("annotation %s = %q, want %q", key, got, want)
	}
}

func assertWebhookMinReadyInflated(t *testing.T, deployment *apps.Deployment) {
	t.Helper()
	if deployment.Spec.MinReadySeconds != inflatedMinReadySeconds {
		t.Fatalf("minReadySeconds = %d, want %d", deployment.Spec.MinReadySeconds, inflatedMinReadySeconds)
	}
	if deployment.Spec.ProgressDeadlineSeconds == nil || *deployment.Spec.ProgressDeadlineSeconds != inflatedProgressDeadlineSeconds {
		t.Fatalf("progressDeadlineSeconds = %v, want %d", deployment.Spec.ProgressDeadlineSeconds, inflatedProgressDeadlineSeconds)
	}
	if unavailable := deployment.Spec.Strategy.RollingUpdate.MaxUnavailable; unavailable == nil || unavailable.IntVal != 0 {
		t.Fatalf("maxUnavailable = %v, want 0", unavailable)
	}
}
