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

	apps "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/feature"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
)

func init() {
	_ = policyv1.AddToScheme(scheme)
}

func TestMinReadyInitializeWritesOriginalAnnotationsAndInflatesFields(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Initialize(releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	assertMinReadyInflated(t, got)
	annotations := got.GetAnnotations()
	assertAnnotation(t, annotations, AnnotationOriginalMinReadySeconds, "7")
	assertAnnotation(t, annotations, AnnotationOriginalProgressDeadlineSeconds, "60")
	assertAnnotation(t, annotations, AnnotationOriginalMaxUnavailable, "25%")
	assertAnnotation(t, annotations, AnnotationOriginalMaxSurge, "1")
}

func TestMinReadyInitializeIsIdempotentAndDoesNotOverwriteAnnotations(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds:         "5",
		AnnotationOriginalProgressDeadlineSeconds: "30",
		AnnotationOriginalMaxUnavailable:          "10%",
		AnnotationOriginalMaxSurge:                "2",
	}
	inflateDeploymentStrategy(deployment)
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Initialize(releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	assertAnnotation(t, got.Annotations, AnnotationOriginalMinReadySeconds, "5")
	assertAnnotation(t, got.Annotations, AnnotationOriginalProgressDeadlineSeconds, "30")
	assertAnnotation(t, got.Annotations, AnnotationOriginalMaxUnavailable, "10%")
	assertAnnotation(t, got.Annotations, AnnotationOriginalMaxSurge, "2")
	assertMinReadyInflated(t, got)
}

func TestMinReadyInitializeRejectsGitOpsDrift(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds:         "5",
		AnnotationOriginalProgressDeadlineSeconds: "30",
		AnnotationOriginalMaxUnavailable:          "10%",
		AnnotationOriginalMaxSurge:                "2",
	}
	control := newBuiltMinReadyControl(t, deployment)

	err := control.Initialize(releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), EventDegradedDriftDetected) {
		t.Fatalf("Initialize error = %v, want drift detected", err)
	}
}

func TestMinReadyInitializeRejectsPartialOriginalAnnotations(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds: "5",
	}
	control := newBuiltMinReadyControl(t, deployment)

	err := control.Initialize(releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), AnnotationOriginalProgressDeadlineSeconds) {
		t.Fatalf("Initialize error = %v, want missing annotation error", err)
	}
}

func TestMinReadyInitializeSerializesKubernetesDefaults(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	deployment.Spec.ProgressDeadlineSeconds = nil
	deployment.Spec.Strategy.RollingUpdate = nil
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Initialize(releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	assertAnnotation(t, got.Annotations, AnnotationOriginalProgressDeadlineSeconds, AnnotationValueKubernetesDefault)
	assertAnnotation(t, got.Annotations, AnnotationOriginalMaxUnavailable, AnnotationValueKubernetesDefault)
	assertAnnotation(t, got.Annotations, AnnotationOriginalMaxSurge, AnnotationValueKubernetesDefault)
	assertMinReadyInflated(t, got)
}

func TestMinReadyInitializeRejectsFeatureGateDisabled(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=false")
	control := newBuiltMinReadyControl(t, newMinReadyDeployment())

	err := control.Initialize(releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), "feature gate is disabled") {
		t.Fatalf("Initialize error = %v, want feature gate disabled", err)
	}
}

func TestMinReadyInitializeRejectsCoveringPDB(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-pdb", Namespace: deployment.Namespace},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "busybox"}},
		},
	}
	control := newBuiltMinReadyControl(t, deployment, pdb)

	err := control.Initialize(releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), EventDegradedPDBIncompatible) {
		t.Fatalf("Initialize error = %v, want PDB incompatible", err)
	}
}

func TestMinReadyUpgradeBatchUpdatesMaxUnavailableOnly(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	control := newBuiltMinReadyControl(t, deployment)
	if err := control.Initialize(releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	control.object = fetchMinReadyDeployment(t, control)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           1,
		Replicas:               10,
		DesiredUpdatedReplicas: 5,
	}

	if err := control.UpgradeBatch(ctx); err != nil {
		t.Fatalf("UpgradeBatch failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	if unavailable := got.Spec.Strategy.RollingUpdate.MaxUnavailable; unavailable == nil || unavailable.IntVal != 5 {
		t.Fatalf("maxUnavailable = %v, want 5", unavailable)
	}
	if surge := got.Spec.Strategy.RollingUpdate.MaxSurge; surge == nil || surge.IntVal != InflatedMaxSurgeInt {
		t.Fatalf("maxSurge = %v, want %d", surge, InflatedMaxSurgeInt)
	}
	if got.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
		t.Fatalf("strategy.type = %q, want RollingUpdate", got.Spec.Strategy.Type)
	}
}

func TestMinReadyCalculateBatchContextUsesReadyReplicas(t *testing.T) {
	release := releaseDemo.DeepCopy()
	release.Status.CanaryStatus.CurrentBatch = 1
	release.Status.UpdateRevision = "version-2"
	deployment := newMinReadyDeployment()
	deployment.Status.Replicas = 10
	deployment.Status.UpdatedReplicas = 5
	deployment.Status.ReadyReplicas = 5
	control := newBuiltMinReadyControl(t, deployment)

	ctx, err := control.CalculateBatchContext(release)
	if err != nil {
		t.Fatalf("CalculateBatchContext failed: %v", err)
	}

	if ctx.DesiredUpdatedReplicas != 5 || ctx.PlannedUpdatedReplicas != 5 {
		t.Fatalf("desired/planned = %d/%d, want 5/5", ctx.DesiredUpdatedReplicas, ctx.PlannedUpdatedReplicas)
	}
	if ctx.UpdatedReadyReplicas != 5 {
		t.Fatalf("UpdatedReadyReplicas = %d, want ReadyReplicas 5", ctx.UpdatedReadyReplicas)
	}
	if err := ctx.IsBatchReady(); err != nil {
		t.Fatalf("IsBatchReady failed: %v", err)
	}
}

func TestMinReadyCalculateBatchContextNotReady(t *testing.T) {
	release := releaseDemo.DeepCopy()
	release.Status.CanaryStatus.CurrentBatch = 1
	deployment := newMinReadyDeployment()
	deployment.Status.Replicas = 10
	deployment.Status.UpdatedReplicas = 5
	deployment.Status.ReadyReplicas = 4
	control := newBuiltMinReadyControl(t, deployment)

	ctx, err := control.CalculateBatchContext(release)
	if err != nil {
		t.Fatalf("CalculateBatchContext failed: %v", err)
	}
	if err := ctx.IsBatchReady(); err == nil {
		t.Fatalf("IsBatchReady succeeded, want not ready error")
	}
}

func TestMinReadyCalculateBatchContextRecomputesAfterScaling(t *testing.T) {
	release := releaseDemo.DeepCopy()
	release.Status.CanaryStatus.CurrentBatch = 1
	deployment := newMinReadyDeployment()
	deployment.Spec.Replicas = pointer.Int32(20)
	deployment.Status.Replicas = 20
	deployment.Status.UpdatedReplicas = 10
	deployment.Status.ReadyReplicas = 10
	control := newBuiltMinReadyControl(t, deployment)

	ctx, err := control.CalculateBatchContext(release)
	if err != nil {
		t.Fatalf("CalculateBatchContext failed: %v", err)
	}
	if ctx.DesiredUpdatedReplicas != 10 {
		t.Fatalf("DesiredUpdatedReplicas = %d, want 10", ctx.DesiredUpdatedReplicas)
	}
}

func TestMinReadyCalculateBatchContextReplicasZero(t *testing.T) {
	release := releaseDemo.DeepCopy()
	release.Status.CanaryStatus.CurrentBatch = 1
	deployment := newMinReadyDeployment()
	deployment.Spec.Replicas = pointer.Int32(0)
	deployment.Status.Replicas = 0
	control := newBuiltMinReadyControl(t, deployment)

	ctx, err := control.CalculateBatchContext(release)
	if err != nil {
		t.Fatalf("CalculateBatchContext failed: %v", err)
	}
	if ctx.DesiredUpdatedReplicas != 0 {
		t.Fatalf("DesiredUpdatedReplicas = %d, want 0", ctx.DesiredUpdatedReplicas)
	}
}
