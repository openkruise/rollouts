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
	"context"
	"strings"
	"testing"

	apps "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/feature"
	"github.com/openkruise/rollouts/pkg/util"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
)

func init() {
	_ = policyv1.AddToScheme(scheme)
}

func TestMinReadyInitializeWritesOriginalAnnotationsAndInflatesFields(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Initialize(context.Background(), releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	assertMinReadyInflated(t, got)
	annotations := got.GetAnnotations()
	assertAnnotation(t, annotations, AnnotationOriginalMinReadySeconds, "7")
	assertAnnotation(t, annotations, AnnotationOriginalProgressDeadlineSeconds, "60")
	assertAnnotation(t, annotations, AnnotationOriginalMaxUnavailable, "25%")
	assertAnnotation(t, annotations, util.BatchReleaseControlAnnotation, getControlInfo(releaseDemo))
}

func TestMinReadyInitializeIsIdempotentAndDoesNotOverwriteAnnotations(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds:         "5",
		AnnotationOriginalProgressDeadlineSeconds: "30",
		AnnotationOriginalMaxUnavailable:          "10%",
	}
	inflateDeploymentStrategy(deployment)
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Initialize(context.Background(), releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	assertAnnotation(t, got.Annotations, AnnotationOriginalMinReadySeconds, "5")
	assertAnnotation(t, got.Annotations, AnnotationOriginalProgressDeadlineSeconds, "30")
	assertAnnotation(t, got.Annotations, AnnotationOriginalMaxUnavailable, "10%")
	assertMinReadyInflated(t, got)
}

func TestMinReadyInitializeRejectsGitOpsDrift(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds:         "5",
		AnnotationOriginalProgressDeadlineSeconds: "30",
		AnnotationOriginalMaxUnavailable:          "10%",
	}
	control := newBuiltMinReadyControl(t, deployment)

	err := control.Initialize(context.Background(), releaseDemo.DeepCopy())
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

	err := control.Initialize(context.Background(), releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), AnnotationOriginalProgressDeadlineSeconds) {
		t.Fatalf("Initialize error = %v, want missing annotation error", err)
	}
}

func TestMinReadyInitializeRejectsEmptyOriginalAnnotations(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds:         "",
		AnnotationOriginalProgressDeadlineSeconds: "30",
		AnnotationOriginalMaxUnavailable:          "10%",
	}
	control := newBuiltMinReadyControl(t, deployment)

	err := control.Initialize(context.Background(), releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), "present but empty") {
		t.Fatalf("Initialize error = %v, want empty annotation error", err)
	}
}

func TestMinReadyInitializeSerializesKubernetesDefaults(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	deployment.Spec.MinReadySeconds = 0
	deployment.Spec.ProgressDeadlineSeconds = nil
	deployment.Spec.Strategy.RollingUpdate = nil
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Initialize(context.Background(), releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	assertAnnotation(t, got.Annotations, AnnotationOriginalMinReadySeconds, "0")
	assertAnnotation(t, got.Annotations, AnnotationOriginalProgressDeadlineSeconds, "600")
	assertAnnotation(t, got.Annotations, AnnotationOriginalMaxUnavailable, "25%")
	assertMinReadyInflatedWithoutSurgeRequirement(t, got)
}

func TestMinReadyInitializeRejectsFeatureGateDisabled(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=false")
	control := newBuiltMinReadyControl(t, newMinReadyDeployment())

	err := control.Initialize(context.Background(), releaseDemo.DeepCopy())
	if err == nil || !strings.Contains(err.Error(), "feature gate is disabled") {
		t.Fatalf("Initialize error = %v, want feature gate disabled", err)
	}
}

func TestMinReadyInitializeAllowsCoveringPDB(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-pdb", Namespace: deployment.Namespace},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "busybox"}},
		},
	}
	control := newBuiltMinReadyControl(t, deployment, pdb)

	if err := control.Initialize(context.Background(), releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
}

func TestMinReadyUpgradeBatchUpdatesMaxUnavailableOnly(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newMinReadyDeployment()
	control := newBuiltMinReadyControl(t, deployment)
	if err := control.Initialize(context.Background(), releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	control.object = fetchMinReadyDeployment(t, control)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           1,
		Replicas:               10,
		DesiredUpdatedReplicas: 5,
	}

	if err := control.UpgradeBatch(context.Background(), ctx); err != nil {
		t.Fatalf("UpgradeBatch failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	// Sliding window (P0-3): UpgradeBatch advances maxUnavailable one step
	// (original 25% of 10 = 3) toward the batch target 5, not straight to 5.
	if unavailable := got.Spec.Strategy.RollingUpdate.MaxUnavailable; unavailable == nil || unavailable.IntVal != 3 {
		t.Fatalf("maxUnavailable = %v, want 3 (first sliding-window step)", unavailable)
	}
	if got.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
		t.Fatalf("strategy.type = %q, want RollingUpdate", got.Spec.Strategy.Type)
	}
}

func TestMinReadyUpgradeBatchRejectsStrategyTypeDrift(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newInflatedMinReadyDeployment()
	addMinReadyOriginalAnnotations(deployment)
	deployment.Spec.Strategy.Type = apps.RecreateDeploymentStrategyType
	control := newBuiltMinReadyControl(t, deployment)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           1,
		Replicas:               10,
		DesiredUpdatedReplicas: 5,
	}

	err := control.UpgradeBatch(context.Background(), ctx)
	if err == nil || !strings.Contains(err.Error(), EventDegradedDriftDetected) {
		t.Fatalf("UpgradeBatch error = %v, want strategy type drift detected", err)
	}
}

func TestMinReadyUpgradeBatchHealsPausedDrift(t *testing.T) {
	// P0-2: a Deployment paused mid-rollout silently freezes the native
	// controller. validateInflatedDeploymentStrategy now treats paused as drift,
	// so ensureInflatedDeploymentStrategy re-inflates and clears spec.paused,
	// actively unfreezing the workload instead of leaving it stuck without signal.
	// (Recreate strategy-type drift is reported as degraded instead of healed,
	// because Recreate may have already deleted pods destructively.)
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newInflatedMinReadyDeployment()
	addMinReadyOriginalAnnotations(deployment)
	deployment.Spec.Paused = true
	control := newBuiltMinReadyControl(t, deployment)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           1,
		Replicas:               10,
		DesiredUpdatedReplicas: 5,
	}

	if err := control.UpgradeBatch(context.Background(), ctx); err != nil {
		t.Fatalf("UpgradeBatch failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	if got.Spec.Paused {
		t.Fatalf("deployment still paused, want spec.paused=false after self-heal")
	}
	if got.Spec.MinReadySeconds != InflatedMinReadySeconds {
		t.Fatalf("minReadySeconds = %d, want %d (re-inflated)", got.Spec.MinReadySeconds, InflatedMinReadySeconds)
	}
}

func TestMinReadyUpgradeBatchRestoresInflatedStrategyFields(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newInflatedMinReadyDeployment()
	addMinReadyOriginalAnnotations(deployment)
	deployment.Spec.MinReadySeconds = 7
	deployment.Spec.ProgressDeadlineSeconds = pointer.Int32(60)
	deployment.Spec.Strategy.RollingUpdate = nil
	control := newBuiltMinReadyControl(t, deployment)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           1,
		Replicas:               10,
		DesiredUpdatedReplicas: 5,
	}

	if err := control.UpgradeBatch(context.Background(), ctx); err != nil {
		t.Fatalf("UpgradeBatch failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	if got.Spec.MinReadySeconds != InflatedMinReadySeconds {
		t.Fatalf("minReadySeconds = %d, want %d", got.Spec.MinReadySeconds, InflatedMinReadySeconds)
	}
	if got.Spec.ProgressDeadlineSeconds == nil || *got.Spec.ProgressDeadlineSeconds != InflatedProgressDeadlineSeconds {
		t.Fatalf("progressDeadlineSeconds = %v, want %d", got.Spec.ProgressDeadlineSeconds, InflatedProgressDeadlineSeconds)
	}
	if got.Spec.Strategy.RollingUpdate == nil {
		t.Fatalf("rollingUpdate is nil, want restored strategy")
	}
	// Sliding window (P0-3): after re-inflation maxUnavailable starts at 0, so
	// UpgradeBatch advances it one step (25% of 10 = 3) toward target 5.
	if unavailable := got.Spec.Strategy.RollingUpdate.MaxUnavailable; unavailable == nil || unavailable.IntVal != 3 {
		t.Fatalf("maxUnavailable = %v, want 3 (first sliding-window step)", unavailable)
	}
}

func TestMinReadyReconcileMaxUnavailableDriftConvergesExternalTampering(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newInflatedMinReadyDeployment()
	addMinReadyOriginalAnnotations(deployment)
	maxUnavailable := intstr.FromInt(5)
	deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	control := newBuiltMinReadyControl(t, deployment)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           0,
		Replicas:               5,
		DesiredUpdatedReplicas: 1,
	}

	if err := control.ReconcileMaxUnavailableDrift(context.Background(), ctx); err != nil {
		t.Fatalf("ReconcileMaxUnavailableDrift failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	if value := minReadyMaxUnavailableValue(t, got, 5); value != 1 {
		t.Fatalf("maxUnavailable = %d, want 1 (converged while batch is ready)", value)
	}
}

func TestMinReadyUpgradeBatchConvergesMaxUnavailableOnScaleDown(t *testing.T) {
	// P1-2: after a scale-down (HPA or manual) the previously-set integer
	// maxUnavailable can exceed the new batch target. This is a legal state, not
	// external tampering, so UpgradeBatch must converge it back to the target
	// instead of reporting degraded drift.
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newInflatedMinReadyDeployment()
	addMinReadyOriginalAnnotations(deployment)
	maxUnavailable := intstr.FromInt(8)
	deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	control := newBuiltMinReadyControl(t, deployment)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           1,
		Replicas:               10,
		DesiredUpdatedReplicas: 5,
	}

	if err := control.UpgradeBatch(context.Background(), ctx); err != nil {
		t.Fatalf("UpgradeBatch failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	if value := minReadyMaxUnavailableValue(t, got, 10); value != 5 {
		t.Fatalf("maxUnavailable = %d, want 5 (converged to target)", value)
	}
}

func TestMinReadyCalculateBatchContextUsesUpdatedReadyReplicas(t *testing.T) {
	release := releaseDemo.DeepCopy()
	release.Status.CanaryStatus.CurrentBatch = 1
	updateRevision := util.ComputeHash(&deploymentDemo.Spec.Template, nil)
	release.Status.UpdateRevision = updateRevision
	deployment := newMinReadyDeployment()
	deployment.Status.Replicas = 10
	deployment.Status.UpdatedReplicas = 5
	deployment.Status.ReadyReplicas = 5
	addMinReadyOriginalAnnotations(deployment)
	rs := newMinReadyReplicaSet(deployment, updateRevision, 5, 5)
	pods := newMinReadyUpdatedPods(deployment, rs, updateRevision, "", 5, 5)
	control := newBuiltMinReadyControl(t, deployment, appendPodObjects([]interface{}{rs}, pods)...)

	ctx, err := control.CalculateBatchContext(release)
	if err != nil {
		t.Fatalf("CalculateBatchContext failed: %v", err)
	}

	if ctx.DesiredUpdatedReplicas != 5 || ctx.PlannedUpdatedReplicas != 5 {
		t.Fatalf("desired/planned = %d/%d, want 5/5", ctx.DesiredUpdatedReplicas, ctx.PlannedUpdatedReplicas)
	}
	if ctx.UpdatedReadyReplicas != 5 {
		t.Fatalf("UpdatedReadyReplicas = %d, want updated available pods 5", ctx.UpdatedReadyReplicas)
	}
	if err := ctx.IsBatchReady(); err != nil {
		t.Fatalf("IsBatchReady failed: %v", err)
	}
}

func TestMinReadyCalculateBatchContextIgnoresOldReadyPods(t *testing.T) {
	release := releaseDemo.DeepCopy()
	release.Status.CanaryStatus.CurrentBatch = 1
	updateRevision := util.ComputeHash(&deploymentDemo.Spec.Template, nil)
	release.Status.UpdateRevision = updateRevision
	deployment := newMinReadyDeployment()
	deployment.Status.Replicas = 10
	deployment.Status.UpdatedReplicas = 5
	deployment.Status.ReadyReplicas = 10
	addMinReadyOriginalAnnotations(deployment)
	rs := newMinReadyReplicaSet(deployment, updateRevision, 5, 1)
	pods := newMinReadyUpdatedPods(deployment, rs, updateRevision, "", 5, 1)
	control := newBuiltMinReadyControl(t, deployment, appendPodObjects([]interface{}{rs}, pods)...)

	ctx, err := control.CalculateBatchContext(release)
	if err != nil {
		t.Fatalf("CalculateBatchContext failed: %v", err)
	}
	if ctx.UpdatedReadyReplicas != 1 {
		t.Fatalf("UpdatedReadyReplicas = %d, want 1 from updated available pods only", ctx.UpdatedReadyReplicas)
	}
	if err := ctx.IsBatchReady(); err == nil {
		t.Fatalf("IsBatchReady succeeded, want not ready error")
	}
}

func TestMinReadyCalculateBatchContextRequiresPodListingForRolloutID(t *testing.T) {
	release := releaseDemo.DeepCopy()
	release.Status.CanaryStatus.CurrentBatch = 1
	release.Spec.ReleasePlan.RolloutID = "rollout-1"
	updateRevision := util.ComputeHash(&deploymentDemo.Spec.Template, nil)
	release.Status.UpdateRevision = updateRevision
	deployment := newMinReadyDeployment()
	deployment.Status.Replicas = 10
	deployment.Status.UpdatedReplicas = 5
	deployment.Status.ReadyReplicas = 5
	addMinReadyOriginalAnnotations(deployment)
	rs := newMinReadyReplicaSet(deployment, updateRevision, 5, 5)
	control := newBuiltMinReadyControl(t, deployment, rs)

	ctx, err := control.CalculateBatchContext(release)
	if err != nil {
		t.Fatalf("CalculateBatchContext failed: %v", err)
	}
	if len(ctx.Pods) != 0 {
		t.Fatalf("Pods = %d, want 0 when no pods exist in cluster", len(ctx.Pods))
	}
	if err := ctx.IsBatchReady(); err == nil {
		t.Fatalf("IsBatchReady succeeded, want batch label not satisfied")
	}
}

func TestMinReadyCalculateBatchContextCountsReadyPodsWhenListed(t *testing.T) {
	release := releaseDemo.DeepCopy()
	release.Status.CanaryStatus.CurrentBatch = 1
	release.Spec.ReleasePlan.RolloutID = "rollout-1"
	updateRevision := util.ComputeHash(&deploymentDemo.Spec.Template, nil)
	release.Status.UpdateRevision = updateRevision
	deployment := newMinReadyDeployment()
	deployment.Status.Replicas = 10
	deployment.Status.UpdatedReplicas = 5
	deployment.Status.ReadyReplicas = 10
	addMinReadyOriginalAnnotations(deployment)
	rs := newMinReadyReplicaSet(deployment, updateRevision, 5, 1)
	pods := newMinReadyUpdatedPods(deployment, rs, updateRevision, "rollout-1", 5, 3)
	control := newBuiltMinReadyControl(t, deployment, rs, pods[0], pods[1], pods[2], pods[3], pods[4])

	ctx, err := control.CalculateBatchContext(release)
	if err != nil {
		t.Fatalf("CalculateBatchContext failed: %v", err)
	}
	if len(ctx.Pods) != 5 {
		t.Fatalf("Pods = %d, want 5 listed pods", len(ctx.Pods))
	}
	if ctx.UpdatedReadyReplicas != 3 {
		t.Fatalf("UpdatedReadyReplicas = %d, want 3 ready updated pods", ctx.UpdatedReadyReplicas)
	}
	if err := ctx.IsBatchReady(); err == nil {
		t.Fatalf("IsBatchReady succeeded, want not ready error")
	}
}

func TestMinReadyCalculateBatchContextNotReady(t *testing.T) {
	release := releaseDemo.DeepCopy()
	release.Status.CanaryStatus.CurrentBatch = 1
	updateRevision := util.ComputeHash(&deploymentDemo.Spec.Template, nil)
	release.Status.UpdateRevision = updateRevision
	deployment := newMinReadyDeployment()
	deployment.Status.Replicas = 10
	deployment.Status.UpdatedReplicas = 5
	deployment.Status.ReadyReplicas = 4
	addMinReadyOriginalAnnotations(deployment)
	rs := newMinReadyReplicaSet(deployment, updateRevision, 5, 4)
	pods := newMinReadyUpdatedPods(deployment, rs, updateRevision, "", 5, 4)
	control := newBuiltMinReadyControl(t, deployment, appendPodObjects([]interface{}{rs}, pods)...)

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
	updateRevision := util.ComputeHash(&deploymentDemo.Spec.Template, nil)
	release.Status.UpdateRevision = updateRevision
	deployment := newMinReadyDeployment()
	deployment.Spec.Replicas = pointer.Int32(20)
	deployment.Status.Replicas = 20
	deployment.Status.UpdatedReplicas = 10
	deployment.Status.ReadyReplicas = 10
	addMinReadyOriginalAnnotations(deployment)
	rs := newMinReadyReplicaSet(deployment, updateRevision, 10, 10)
	pods := newMinReadyUpdatedPods(deployment, rs, updateRevision, "", 10, 10)
	control := newBuiltMinReadyControl(t, deployment, appendPodObjects([]interface{}{rs}, pods)...)

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
	addMinReadyOriginalAnnotations(deployment)
	control := newBuiltMinReadyControl(t, deployment)

	ctx, err := control.CalculateBatchContext(release)
	if err != nil {
		t.Fatalf("CalculateBatchContext failed: %v", err)
	}
	if ctx.DesiredUpdatedReplicas != 0 {
		t.Fatalf("DesiredUpdatedReplicas = %d, want 0", ctx.DesiredUpdatedReplicas)
	}
}

func TestMinReadyFinalizeRestoresAfterGateDisabled(t *testing.T) {
	// P1-4: even with the feature gate disabled, a Deployment carrying MinReady
	// original annotations must finalize cleanly and restore the original fields.
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=false")
	deployment := newInflatedMinReadyDeployment()
	addMinReadyOriginalAnnotations(deployment)
	control := newBuiltMinReadyControl(t, deployment)

	if err := control.Finalize(context.Background(), releaseDemo.DeepCopy()); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}

	got := fetchMinReadyDeployment(t, control)
	if got.Spec.MinReadySeconds != 7 {
		t.Fatalf("minReadySeconds = %d, want 7 (restored)", got.Spec.MinReadySeconds)
	}
	if got.Spec.ProgressDeadlineSeconds == nil || *got.Spec.ProgressDeadlineSeconds != 60 {
		t.Fatalf("progressDeadlineSeconds = %v, want 60 (restored)", got.Spec.ProgressDeadlineSeconds)
	}
	if hasAnyOriginalAnnotation(got.Annotations) {
		t.Fatalf("original annotations not cleaned up: %v", got.Annotations)
	}
}

func TestMinReadySlidingWindowAdvancesStepByStep(t *testing.T) {
	// P0-3: a large batch target must not be written to maxUnavailable in a
	// single patch. reconcileMaxUnavailable keeps at most the user's original
	// maxUnavailable (25% of 10 = 3) worth of updated-but-not-ready pods in
	// flight, topping up the window as individual pods become ready.
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newInflatedMinReadyDeployment()
	addMinReadyOriginalAnnotations(deployment)
	control := newBuiltMinReadyControl(t, deployment)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           1,
		Replicas:               10,
		DesiredUpdatedReplicas: 9,
	}

	steps := []struct {
		ready   int32
		wantMU  int
		comment string
	}{
		{0, 3, "empty window advances to first step"},
		{1, 4, "one ready pod tops up one slot"},
		{2, 5, "partial readiness keeps topping up"},
		{4, 7, "does not wait for the whole current window"},
		{6, 9, "advance caps at target"},
		{9, 9, "at target holds"},
	}
	for i, s := range steps {
		ctx.UpdatedReadyReplicas = s.ready
		if err := control.ReconcileMaxUnavailableDrift(context.Background(), ctx); err != nil {
			t.Fatalf("step %d (%s): %v", i, s.comment, err)
		}
		if v := minReadyMaxUnavailableValue(t, fetchMinReadyDeployment(t, control), 10); v != s.wantMU {
			t.Fatalf("step %d (%s): maxUnavailable = %d, want %d", i, s.comment, v, s.wantMU)
		}
	}
}

func TestMinReadySlidingWindowReachesSmallTargetInOneStep(t *testing.T) {
	// P0-3: when the batch target is within one step (target <= step), the
	// first advance is capped at the target, so small batches complete in one
	// reconcile instead of overshooting.
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newInflatedMinReadyDeployment()
	addMinReadyOriginalAnnotations(deployment)
	control := newBuiltMinReadyControl(t, deployment)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           1,
		Replicas:               10,
		DesiredUpdatedReplicas: 2,
		UpdatedReadyReplicas:   0,
	}
	if err := control.ReconcileMaxUnavailableDrift(context.Background(), ctx); err != nil {
		t.Fatalf("drift reconcile failed: %v", err)
	}
	if v := minReadyMaxUnavailableValue(t, fetchMinReadyDeployment(t, control), 10); v != 2 {
		t.Fatalf("maxUnavailable = %d, want 2 (small target reached in one step)", v)
	}
}

func TestMinReadySlidingWindowStepZeroDrivesBatchDirectly(t *testing.T) {
	// P0-3: original maxUnavailable=0 means the user relies on maxSurge for
	// concurrency control, so there is no budget to slide; the batch target is
	// driven directly to preserve the existing surge-gated behavior.
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	deployment := newInflatedMinReadyDeployment()
	deployment.Annotations = map[string]string{
		AnnotationOriginalMinReadySeconds:         "7",
		AnnotationOriginalProgressDeadlineSeconds: "60",
		AnnotationOriginalMaxUnavailable:          "0",
	}
	control := newBuiltMinReadyControl(t, deployment)
	ctx := &batchcontext.BatchContext{
		CurrentBatch:           1,
		Replicas:               10,
		DesiredUpdatedReplicas: 5,
		UpdatedReadyReplicas:   0,
	}
	if err := control.ReconcileMaxUnavailableDrift(context.Background(), ctx); err != nil {
		t.Fatalf("drift reconcile failed: %v", err)
	}
	if v := minReadyMaxUnavailableValue(t, fetchMinReadyDeployment(t, control), 10); v != 5 {
		t.Fatalf("maxUnavailable = %d, want 5 (step=0 drives batch directly)", v)
	}
}
