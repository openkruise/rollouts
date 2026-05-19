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
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/gomega"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1beta1"
)

func finishMinReadyE2ERollout(namespace, name string) {
	resumeMinReadyE2ERollout(namespace, name)
	resumeMinReadyE2ERollout(namespace, name)
	waitMinReadyE2ERolloutPhase(namespace, name, v1beta1.RolloutPhaseHealthy)
	waitMinReadyE2EDeploymentRestored(namespace)
}

func waitMinReadyE2EDeploymentReady(namespace string) {
	Eventually(func() bool {
		deployment := &apps.Deployment{}
		key := types.NamespacedName{Namespace: namespace, Name: minReadyE2EDeploymentName}
		if err := k8sClient.Get(context.TODO(), key, deployment); err != nil {
			return false
		}
		return deployment.Status.ReadyReplicas == *deployment.Spec.Replicas
	}, 10*time.Minute, time.Second).Should(BeTrue())
}

func waitMinReadyE2ERolloutStepPaused(namespace, name string, step int32) {
	Eventually(func() bool {
		rollout := &v1beta1.Rollout{}
		key := types.NamespacedName{Namespace: namespace, Name: name}
		Expect(k8sClient.Get(context.TODO(), key, rollout)).NotTo(HaveOccurred())
		return rollout.Status.CanaryStatus != nil &&
			rollout.Status.CanaryStatus.CurrentStepIndex == step &&
			rollout.Status.CanaryStatus.CurrentStepState == v1beta1.CanaryStepStatePaused
	}, 10*time.Minute, time.Second).Should(BeTrue())
}

func patchMinReadyE2EDeploymentReplicas(namespace string, replicas int32) {
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployment := &apps.Deployment{}
		key := types.NamespacedName{Namespace: namespace, Name: minReadyE2EDeploymentName}
		if err := k8sClient.Get(context.TODO(), key, deployment); err != nil {
			return err
		}
		deployment.Spec.Replicas = pointer.Int32(replicas)
		return k8sClient.Update(context.TODO(), deployment)
	})).NotTo(HaveOccurred())
}

func patchMinReadyE2EMaxUnavailable(namespace string, value int) {
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployment := &apps.Deployment{}
		key := types.NamespacedName{Namespace: namespace, Name: minReadyE2EDeploymentName}
		if err := k8sClient.Get(context.TODO(), key, deployment); err != nil {
			return err
		}
		maxUnavailable := intstr.FromInt(value)
		deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
		return k8sClient.Update(context.TODO(), deployment)
	})).NotTo(HaveOccurred())
}

func deleteMinReadyE2EOriginalAnnotation(namespace, key string) {
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployment := &apps.Deployment{}
		namespacedName := types.NamespacedName{Namespace: namespace, Name: minReadyE2EDeploymentName}
		if err := k8sClient.Get(context.TODO(), namespacedName, deployment); err != nil {
			return err
		}
		delete(deployment.Annotations, key)
		return k8sClient.Update(context.TODO(), deployment)
	})).NotTo(HaveOccurred())
}

func restartMinReadyE2EControllerManager() {
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployment := &apps.Deployment{}
		key := types.NamespacedName{Namespace: "kruise-rollout", Name: "kruise-rollout-controller-manager"}
		if err := k8sClient.Get(context.TODO(), key, deployment); err != nil {
			return err
		}
		if deployment.Spec.Template.Annotations == nil {
			deployment.Spec.Template.Annotations = map[string]string{}
		}
		deployment.Spec.Template.Annotations["rollouts.kruise.io/minready-e2e-restart"] = strconv.FormatInt(time.Now().UnixNano(), 10)
		return k8sClient.Update(context.TODO(), deployment)
	})).NotTo(HaveOccurred())
	waitMinReadyE2EWebhookEndpointReady()
}

func expectMinReadyE2EDeploymentVersion(namespace, version string) {
	deployment := &apps.Deployment{}
	key := types.NamespacedName{Namespace: namespace, Name: minReadyE2EDeploymentName}
	Expect(k8sClient.Get(context.TODO(), key, deployment)).Should(Succeed())
	got := ""
	for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "NODE_NAME" {
			got = env.Value
		}
	}
	Expect(got).Should(Equal(version))
}

func expectMinReadyE2ENoVersion2Pods(namespace string) {
	pods := &corev1.PodList{}
	Expect(k8sClient.List(context.TODO(), pods, client.InNamespace(namespace))).Should(Succeed())
	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			Expect(strings.Contains(container.Image, "version2")).Should(BeFalse())
		}
	}
}
