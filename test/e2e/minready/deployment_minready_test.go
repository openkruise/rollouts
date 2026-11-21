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

package minready

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openkruise/rollouts/api/v1beta1"
	partitiondeployment "github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle/deployment"
)

var _ = SIGDescribe("Deployment MinReadySeconds", func() {
	var namespace string

	BeforeEach(func() {
		namespace = randomNamespaceName("deployment-minready")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(context.TODO(), ns)).Should(Succeed())
	})

	AfterEach(func() {
		teardownMinReadyE2ENamespace(namespace)
	})

	KruiseDescribe("MinReadySeconds deployment rollout", func() {
		It("TC1 normal rollout keeps RollingUpdate and restores original fields", func() {
			rollout := startMinReadyE2ERollout(namespace)
			finishMinReadyE2ERollout(namespace, rollout.Name)
		})

		It("TC2 rollback returns to the stable template", func() {
			rollout := newMinReadyE2ERollout(namespace)
			deployment := newMinReadyE2EDeployment(namespace)
			createReadyMinReadyE2EDeployment(namespace, deployment)
			createHealthyMinReadyE2ERollout(namespace, rollout)

			updateMinReadyE2EDeploymentVersion(namespace, "version2")
			waitMinReadyE2EDeploymentInflated(namespace)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 1)
			updateMinReadyE2EDeploymentVersion(namespace, "version1")
			waitMinReadyE2ERolloutPhase(namespace, rollout.Name, v1beta1.RolloutPhaseHealthy)
			waitMinReadyE2EDeploymentRestored(namespace)

			expectMinReadyE2EDeploymentVersion(namespace, "version1")
		})

		It("TC3 continuous release rolls v1 to v2 to v3 and refreshes original availability fields", func() {
			rollout := startMinReadyE2ERollout(namespace)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 1)

			updateMinReadyE2EDeploymentContinuousRelease(namespace, "version3", 9, 90)
			waitMinReadyE2EDeploymentInflated(namespace)
			waitMinReadyE2EOriginalAvailabilityAnnotations(namespace, 9, 90)
			expectMinReadyE2EDeploymentVersion(namespace, "version3")

			finishMinReadyE2ERolloutWithAvailability(namespace, rollout.Name, 9, 90)
		})

		It("TC4 controller restart resumes from the persisted MinReadySeconds state", func() {
			rollout := startMinReadyE2ERollout(namespace)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 1)
			restartMinReadyE2EControllerManager()

			waitMinReadyE2EDeploymentInflated(namespace)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 1)
			finishMinReadyE2ERollout(namespace, rollout.Name)
		})

		It("TC5 scale changes remain safe while rollout is active", func() {
			rollout := makeMinReadyE2ERolloutWithReplicas(namespace, "25%", "50%", "100%")
			deployment := newMinReadyE2EDeployment(namespace)
			setMinReadyE2EInitialReplicas(deployment, 4)
			createReadyMinReadyE2EDeployment(namespace, deployment)
			createHealthyMinReadyE2ERollout(namespace, rollout)

			updateMinReadyE2EDeploymentVersion(namespace, "version2")
			waitMinReadyE2EDeploymentInflated(namespace)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 1)
			patchMinReadyE2EDeploymentReplicas(namespace, 8)
			waitMinReadyE2EDeploymentReplicas(namespace, 8)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 1)
			resumeMinReadyE2ERollout(namespace, rollout.Name)
			expectMinReadyE2EInflatedMaxUnavailable(namespace, 4)
			finishMinReadyE2ERollout(namespace, rollout.Name)
		})

		It("TC6 deleting Rollout restores annotations and lets native RollingUpdate continue", func() {
			rollout := startMinReadyE2ERollout(namespace)
			deleteMinReadyE2ERollout(namespace, rollout.Name)

			waitMinReadyE2ERolloutDeleted(namespace, rollout.Name)
			waitMinReadyE2EDeploymentRestored(namespace)
			expectMinReadyE2EOriginalAnnotationAbsent(namespace)
		})

		It("TC7 external maxUnavailable drift converges to the current batch target", func() {
			rollout := startMinReadyE2ERollout(namespace)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 1)
			patchMinReadyE2EMaxUnavailable(namespace, 5)
			// Heal drift to the paused step's batch target (20% on 5 replicas => 1).
			expectMinReadyE2EInflatedMaxUnavailable(namespace, 1)
			finishMinReadyE2ERollout(namespace, rollout.Name)
		})

		It("TC8 missing annotation blocks finalize until the operator restores it", func() {
			rollout := startMinReadyE2ERollout(namespace)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 1)
			deleteMinReadyE2EOriginalAnnotation(namespace, partitiondeployment.AnnotationOriginalMaxUnavailable)
			resumeMinReadyE2ERollout(namespace, rollout.Name)
			waitMinReadyE2EBatchCondition(namespace, rollout.Name, "MinReadyDegradedMissingAnnotations")
			waitMinReadyE2EEventReason(namespace, "MinReadyDegradedMissingAnnotations")

			restoreMinReadyE2EOriginalAnnotation(namespace, partitiondeployment.AnnotationOriginalMaxUnavailable, "25%")
			finishMinReadyE2ERollout(namespace, rollout.Name)
		})

		// TC9 verifies the native Kubernetes Deployment controller's pod-level
		// behavior during a MinReady rollout. Existing TCs only assert the
		// rollout controller's intended Spec.MaxUnavailable value; this TC
		// asserts the actual pod counts the native Deployment controller
		// produces (UpdatedReplicas, Replicas, ReadyReplicas), proving the
		// MaxSurge/MaxUnavailable boundaries are respected end-to-end.
		It("TC9 native Deployment controller respects MaxSurge and reaches batch targets during MinReady rollout", func() {
			rollout := startMinReadyE2ERollout(namespace)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 1)

			// Step 1 (20% of 5 replicas = 1): surge bounded by MaxSurge=1
			// and at least the batch target is updated and Ready. An extra
			// updated pod may exist as allowed surge capacity.
			expectMinReadyE2EDeploymentPodInvariants(namespace, rollout.Name, 5, 1, 1)

			resumeMinReadyE2ERollout(namespace, rollout.Name)
			waitMinReadyE2ERolloutStepPaused(namespace, rollout.Name, 2)

			// Step 2 (50% of 5 replicas = 3): surge bounded, with at least
			// the batch target updated and Ready.
			expectMinReadyE2EDeploymentPodInvariants(namespace, rollout.Name, 5, 1, 3)

			finishMinReadyE2ERollout(namespace, rollout.Name)

			// Final steady state: all 5 pods updated, ready, no surge.
			expectMinReadyE2EDeploymentFinalState(namespace, 5)
		})
	})
})
