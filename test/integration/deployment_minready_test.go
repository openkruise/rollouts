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

package integration

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	"github.com/openkruise/rollouts/api/v1beta1"
	partitiondeployment "github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle/deployment"
	"github.com/openkruise/rollouts/pkg/feature"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
)

func TestDeploymentMinReadyControlPlaneInitialize(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := newIntegrationMinReadyRelease()
	deployment := newIntegrationDeployment()
	recorder := record.NewFakeRecorder(20)
	cli := newIntegrationClient(release, deployment)
	status := release.Status.DeepCopy()
	control := newIntegrationMinReadyControl(cli, recorder, release, status, deployment.Name)

	if err := control.Initialize(); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	got := fetchIntegrationDeployment(t, cli, deployment)
	assertInflatedDeployment(t, got)
	assertOriginalAnnotation(t, got, partitiondeployment.AnnotationOriginalMinReadySeconds, "5")
	assertOriginalAnnotation(t, got, partitiondeployment.AnnotationOriginalProgressDeadlineSeconds, "60")
	assertOriginalAnnotation(t, got, partitiondeployment.AnnotationOriginalMaxUnavailable, "25%")
	assertOriginalAnnotation(t, got, partitiondeployment.AnnotationOriginalMaxSurge, "1")
	assertIntegrationCondition(t, status, v1beta1.RolloutConditionMinReadyInitialized, corev1.ConditionTrue, "MinReadyInitialized")
	assertIntegrationCondition(t, status, v1beta1.RolloutConditionMinReadyDegraded, corev1.ConditionFalse, "MinReadyHealthy")
	assertIntegrationEvent(t, recorder, "MinReadyInitialized")
}

func TestDeploymentMinReadyControlPlaneRejectsFeatureGateDisabled(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=false")
	release := newIntegrationMinReadyRelease()
	deployment := newIntegrationDeployment()
	recorder := record.NewFakeRecorder(20)
	cli := newIntegrationClient(release, deployment)
	status := release.Status.DeepCopy()
	control := newIntegrationMinReadyControl(cli, recorder, release, status, deployment.Name)

	err := control.Initialize()
	if err == nil || !strings.Contains(err.Error(), "feature gate is disabled") {
		t.Fatalf("Initialize error = %v, want feature gate disabled", err)
	}

	got := fetchIntegrationDeployment(t, cli, deployment)
	if got.Spec.MinReadySeconds != deployment.Spec.MinReadySeconds {
		t.Fatalf("minReadySeconds = %d, want unchanged %d", got.Spec.MinReadySeconds, deployment.Spec.MinReadySeconds)
	}
	assertIntegrationCondition(t, status, v1beta1.RolloutConditionMinReadyDegraded, corev1.ConditionTrue, "MinReadyFeatureGateDisabled")
	assertIntegrationEvent(t, recorder, "MinReadyFeatureGateDisabled")
}

func TestDeploymentMinReadyControlPlaneRejectsCoveringPDB(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := newIntegrationMinReadyRelease()
	deployment := newIntegrationDeployment()
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-pdb", Namespace: deployment.Namespace},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
		},
	}
	recorder := record.NewFakeRecorder(20)
	cli := newIntegrationClient(release, deployment, pdb)
	status := release.Status.DeepCopy()
	control := newIntegrationMinReadyControl(cli, recorder, release, status, deployment.Name)

	err := control.Initialize()
	if err == nil || !strings.Contains(err.Error(), partitiondeployment.EventDegradedPDBIncompatible) {
		t.Fatalf("Initialize error = %v, want PDB incompatible", err)
	}

	got := fetchIntegrationDeployment(t, cli, deployment)
	if got.Spec.MinReadySeconds != deployment.Spec.MinReadySeconds {
		t.Fatalf("minReadySeconds = %d, want unchanged %d", got.Spec.MinReadySeconds, deployment.Spec.MinReadySeconds)
	}
	assertIntegrationCondition(t, status, v1beta1.RolloutConditionMinReadyDegraded, corev1.ConditionTrue, "MinReadyDegradedPDBIncompatible")
	assertIntegrationEvent(t, recorder, "MinReadyDegradedPDBIncompatible")
}

func TestDeploymentMinReadyControlPlaneUpgradeBatchUsesReadyReplicas(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := newIntegrationMinReadyRelease()
	release.Status.CanaryStatus.CurrentBatch = 1
	deployment := newInflatedIntegrationDeployment()
	deployment.Status.Replicas = 10
	deployment.Status.UpdatedReplicas = 5
	deployment.Status.ReadyReplicas = 5
	recorder := record.NewFakeRecorder(20)
	cli := newIntegrationClient(release, deployment)
	status := release.Status.DeepCopy()
	control := newIntegrationMinReadyControl(cli, recorder, release, status, deployment.Name)

	if err := control.UpgradeBatch(); err != nil {
		t.Fatalf("UpgradeBatch failed: %v", err)
	}
	if err := control.EnsureBatchPodsReadyAndLabeled(); err != nil {
		t.Fatalf("EnsureBatchPodsReadyAndLabeled failed: %v", err)
	}

	got := fetchIntegrationDeployment(t, cli, deployment)
	if unavailable := got.Spec.Strategy.RollingUpdate.MaxUnavailable; unavailable == nil || unavailable.IntVal != 5 {
		t.Fatalf("maxUnavailable = %v, want 5", unavailable)
	}
	assertIntegrationCondition(t, status, v1beta1.RolloutConditionMinReadyBatching, corev1.ConditionTrue, "MinReadyBatchReady")
	assertIntegrationEvent(t, recorder, "MinReadyBatchReady")
}

func TestDeploymentMinReadyControlPlaneFinalizeRestoresOriginalFields(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := newIntegrationMinReadyRelease()
	deployment := newInflatedIntegrationDeployment()
	recorder := record.NewFakeRecorder(20)
	cli := newIntegrationClient(release, deployment)
	status := release.Status.DeepCopy()
	control := newIntegrationMinReadyControl(cli, recorder, release, status, deployment.Name)

	if err := control.Finalize(); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}

	got := fetchIntegrationDeployment(t, cli, deployment)
	if got.Spec.MinReadySeconds != 5 {
		t.Fatalf("minReadySeconds = %d, want 5", got.Spec.MinReadySeconds)
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
	for _, key := range partitiondeployment.AllOriginalAnnotations {
		if _, ok := got.Annotations[key]; ok {
			t.Fatalf("annotation %s still exists", key)
		}
	}
	assertIntegrationCondition(t, status, v1beta1.RolloutConditionMinReadyFinalized, corev1.ConditionTrue, "MinReadyFinalized")
	assertIntegrationEvent(t, recorder, "MinReadyFinalized")
}
