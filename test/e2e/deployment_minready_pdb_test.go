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

package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1beta1"
	partitiondeployment "github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle/deployment"
)

var _ = SIGDescribe("Deployment MinReadySeconds PDB", func() {
	var namespace string

	BeforeEach(func() {
		namespace = randomNamespaceName("deployment-minready-pdb")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(context.TODO(), ns)).Should(Succeed())
	})

	AfterEach(func() {
		_ = k8sClient.DeleteAllOf(context.TODO(), &v1beta1.BatchRelease{}, client.InNamespace(namespace))
		_ = k8sClient.DeleteAllOf(context.TODO(), &v1beta1.Rollout{}, client.InNamespace(namespace))
		_ = k8sClient.DeleteAllOf(context.TODO(), &policyv1.PodDisruptionBudget{}, client.InNamespace(namespace))
		_ = k8sClient.DeleteAllOf(context.TODO(), &apps.Deployment{}, client.InNamespace(namespace))
		Expect(k8sClient.Delete(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).Should(Succeed())
		time.Sleep(3 * time.Second)
	})

	KruiseDescribe("MinReadySeconds PDB coexistence", func() {
		It("continues rollout initialization and leaves Deployment strategy untouched", func() {
			rollout := newMinReadyE2ERollout(namespace)
			deployment := newMinReadyE2EDeployment(namespace)
			pdb := newMinReadyE2EPDB(namespace)
			createMinReadyE2EObject(rollout)
			createMinReadyE2EObject(deployment)
			createMinReadyE2EObject(pdb)

			waitMinReadyE2EDeploymentReady(namespace)
			waitMinReadyE2ERolloutPhase(namespace, rollout.Name, v1beta1.RolloutPhaseHealthy)
			updateMinReadyE2EDeploymentVersion(namespace, "version2")
			waitMinReadyE2EBatchCondition(namespace, rollout.Name, "MinReadyInitialized")

			got := &apps.Deployment{}
			Expect(k8sClient.Get(context.TODO(), client.ObjectKeyFromObject(deployment), got)).Should(Succeed())
			Expect(got.Spec.Strategy.Type).Should(Equal(apps.RollingUpdateDeploymentStrategyType))
			Expect(got.Spec.MinReadySeconds).Should(Equal(partitiondeployment.InflatedMinReadySeconds))
		})
	})
})
