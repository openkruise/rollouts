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

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"

	"github.com/openkruise/rollouts/api/v1beta1"
	partitiondeployment "github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle/deployment"
	"github.com/openkruise/rollouts/pkg/feature"
	"github.com/openkruise/rollouts/pkg/util"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
)

func TestDeploymentMinReadyConcurrentScaleUsesLatestReplicas(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := newIntegrationMinReadyRelease()
	release.Status.CanaryStatus.CurrentBatch = 1
	deployment := newInflatedIntegrationDeployment()
	deployment.Spec.Replicas = pointer.Int32(20)
	deployment.Status.Replicas = 20
	deployment.Status.UpdatedReplicas = 10
	deployment.Status.ReadyReplicas = 10
	recorder := record.NewFakeRecorder(20)
	cli := newIntegrationClient(release, deployment)
	status := release.Status.DeepCopy()
	control := newIntegrationMinReadyControl(cli, recorder, release, status, deployment.Name)

	if err := control.UpgradeBatch(); err != nil {
		t.Fatalf("UpgradeBatch failed: %v", err)
	}

	got := fetchIntegrationDeployment(t, cli, deployment)
	if unavailable := got.Spec.Strategy.RollingUpdate.MaxUnavailable; unavailable == nil || unavailable.IntVal != 10 {
		t.Fatalf("maxUnavailable = %v, want 10 after scale to 20 replicas", unavailable)
	}
	assertIntegrationCondition(t, status, v1beta1.RolloutConditionMinReadyBatching, corev1.ConditionTrue, "MinReadyBatching")
}

func TestDeploymentMinReadyConcurrentMaxUnavailableAboveTargetSelfHeals(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := newIntegrationMinReadyRelease()
	release.Status.CanaryStatus.CurrentBatch = 1
	deployment := newInflatedIntegrationDeployment()
	driftedMaxUnavailable := intstr.FromInt(6)
	deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &driftedMaxUnavailable
	deployment.Status.Replicas = 10
	deployment.Status.UpdatedReplicas = 5
	deployment.Status.ReadyReplicas = 5
	recorder := record.NewFakeRecorder(20)
	cli := newIntegrationClient(release, deployment)
	status := release.Status.DeepCopy()
	control := newIntegrationMinReadyControl(cli, recorder, release, status, deployment.Name)

	err := control.UpgradeBatch()
	if err != nil {
		t.Fatalf("UpgradeBatch failed: %v", err)
	}

	got := fetchIntegrationDeployment(t, cli, deployment)
	if unavailable := got.Spec.Strategy.RollingUpdate.MaxUnavailable; unavailable == nil || unavailable.IntVal != 5 {
		t.Fatalf("maxUnavailable = %v, want target value 5", unavailable)
	}
	if degraded := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyDegraded); degraded != nil {
		t.Fatalf("degraded condition = %v, want nil", degraded)
	}
}

func TestDeploymentMinReadyConcurrentAnnotationDeletionBlocksFinalize(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := newIntegrationMinReadyRelease()
	deployment := newInflatedIntegrationDeployment()
	delete(deployment.Annotations, partitiondeployment.AnnotationOriginalMaxUnavailable)
	recorder := record.NewFakeRecorder(20)
	cli := newIntegrationClient(release, deployment)
	status := release.Status.DeepCopy()
	control := newIntegrationMinReadyControl(cli, recorder, release, status, deployment.Name)

	err := control.Finalize()
	if err == nil || !strings.Contains(err.Error(), partitiondeployment.AnnotationOriginalMaxUnavailable) {
		t.Fatalf("Finalize error = %v, want missing annotation", err)
	}

	got := fetchIntegrationDeployment(t, cli, deployment)
	if got.Spec.MinReadySeconds != partitiondeployment.InflatedMinReadySeconds {
		t.Fatalf("minReadySeconds = %d, want inflated value preserved", got.Spec.MinReadySeconds)
	}
	assertIntegrationCondition(t, status, v1beta1.RolloutConditionMinReadyDegraded, corev1.ConditionTrue, "MinReadyDegradedMissingAnnotations")
	assertIntegrationEvent(t, recorder, "MinReadyDegradedMissingAnnotations")
}

var _ = apps.RollingUpdateDeploymentStrategyType
