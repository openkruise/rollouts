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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1beta1"
	partitiondeployment "github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle/deployment"
)

const minReadyE2EDeploymentName = "minready-demo"

func newMinReadyE2EDeployment(namespace string) *apps.Deployment {
	maxUnavailable := intstr.FromString("25%")
	maxSurge := intstr.FromInt(1)
	return &apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      minReadyE2EDeploymentName,
			Namespace: namespace,
			Labels:    map[string]string{"app": minReadyE2EDeploymentName},
		},
		Spec: apps.DeploymentSpec{
			Replicas: pointer.Int32(5),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": minReadyE2EDeploymentName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": minReadyE2EDeploymentName}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:            "echoserver",
					Image:           "cilium/echoserver:latest",
					ImagePullPolicy: corev1.PullIfNotPresent,
					Env:             []corev1.EnvVar{{Name: "NODE_NAME", Value: "version1"}},
				}}},
			},
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

func newMinReadyE2ERollout(namespace string) *v1beta1.Rollout {
	return &v1beta1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "minready-rollout", Namespace: namespace},
		Spec: v1beta1.RolloutSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: "apps/v1", Kind: "Deployment", Name: minReadyE2EDeploymentName},
			Strategy: v1beta1.RolloutStrategy{Canary: &v1beta1.CanaryStrategy{
				EnableExtraWorkloadForCanary: false,
				Steps: []v1beta1.CanaryStep{
					{Replicas: intstrPtr(intstr.FromString("20%")), Pause: v1beta1.RolloutPause{}},
					{Replicas: intstrPtr(intstr.FromString("50%")), Pause: v1beta1.RolloutPause{}},
					{Replicas: intstrPtr(intstr.FromString("100%")), Pause: v1beta1.RolloutPause{Duration: pointer.Int32(0)}},
				},
			}},
		},
	}
}

func newMinReadyE2EPDB(namespace string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "minready-pdb", Namespace: namespace},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector:     &metav1.LabelSelector{MatchLabels: map[string]string{"app": minReadyE2EDeploymentName}},
			MinAvailable: intstrPtr(intstr.FromInt(4)),
		},
	}
}

func intstrPtr(value intstr.IntOrString) *intstr.IntOrString {
	return &value
}

func createMinReadyE2EObject(object client.Object) {
	By(fmt.Sprintf("create %T %s/%s", object, object.GetNamespace(), object.GetName()))
	Expect(k8sClient.Create(context.TODO(), object)).NotTo(HaveOccurred())
}

func updateMinReadyE2EDeploymentVersion(namespace, version string) {
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		deployment := &apps.Deployment{}
		key := types.NamespacedName{Namespace: namespace, Name: minReadyE2EDeploymentName}
		if err := k8sClient.Get(context.TODO(), key, deployment); err != nil {
			return err
		}
		deployment.Spec.Template.Spec.Containers[0].Env = mergeEnvVar(
			deployment.Spec.Template.Spec.Containers[0].Env,
			corev1.EnvVar{Name: "NODE_NAME", Value: version},
		)
		return k8sClient.Update(context.TODO(), deployment)
	})).NotTo(HaveOccurred())
}

func resumeMinReadyE2ERollout(namespace, name string) {
	resumedStep := int32(-1)
	Eventually(func() bool {
		rollout := &v1beta1.Rollout{}
		key := types.NamespacedName{Namespace: namespace, Name: name}
		Expect(k8sClient.Get(context.TODO(), key, rollout)).NotTo(HaveOccurred())
		if rollout.Status.Phase == v1beta1.RolloutPhaseHealthy {
			return true
		}
		if rollout.Status.CanaryStatus == nil || rollout.Status.CanaryStatus.CurrentStepState != v1beta1.CanaryStepStatePaused {
			return false
		}
		resumedStep = rollout.Status.CanaryStatus.CurrentStepIndex
		body := fmt.Sprintf(`{"status":{"canaryStatus":{"currentStepState":"%s"}}}`, v1beta1.CanaryStepStateReady)
		return k8sClient.Status().Patch(context.TODO(), rollout, client.RawPatch(types.MergePatchType, []byte(body))) == nil
	}, 2*time.Minute, time.Second).Should(BeTrue())
	if resumedStep < 0 {
		return
	}
	Eventually(func() bool {
		rollout := &v1beta1.Rollout{}
		key := types.NamespacedName{Namespace: namespace, Name: name}
		Expect(k8sClient.Get(context.TODO(), key, rollout)).NotTo(HaveOccurred())
		if rollout.Status.Phase == v1beta1.RolloutPhaseHealthy {
			return true
		}
		return rollout.Status.CanaryStatus != nil &&
			(rollout.Status.CanaryStatus.CurrentStepIndex != resumedStep ||
				rollout.Status.CanaryStatus.CurrentStepState != v1beta1.CanaryStepStatePaused)
	}, 2*time.Minute, time.Second).Should(BeTrue())
}

func waitMinReadyE2ERolloutPhase(namespace, name string, phase v1beta1.RolloutPhase) {
	Eventually(func() bool {
		rollout := &v1beta1.Rollout{}
		key := types.NamespacedName{Namespace: namespace, Name: name}
		Expect(k8sClient.Get(context.TODO(), key, rollout)).NotTo(HaveOccurred())
		return rollout.Status.Phase == phase
	}, 10*time.Minute, time.Second).Should(BeTrue())
}

func waitMinReadyE2EDeploymentInflated(namespace string) {
	Eventually(func() bool {
		deployment := &apps.Deployment{}
		key := types.NamespacedName{Namespace: namespace, Name: minReadyE2EDeploymentName}
		Expect(k8sClient.Get(context.TODO(), key, deployment)).NotTo(HaveOccurred())
		return deployment.Spec.Strategy.Type == apps.RollingUpdateDeploymentStrategyType &&
			deployment.Spec.MinReadySeconds == partitiondeployment.InflatedMinReadySeconds &&
			deployment.Spec.Strategy.RollingUpdate != nil
	}, 5*time.Minute, time.Second).Should(BeTrue())
}

func waitMinReadyE2EDeploymentRestored(namespace string) {
	Eventually(func() bool {
		deployment := &apps.Deployment{}
		key := types.NamespacedName{Namespace: namespace, Name: minReadyE2EDeploymentName}
		Expect(k8sClient.Get(context.TODO(), key, deployment)).NotTo(HaveOccurred())
		return deployment.Spec.MinReadySeconds == 0 &&
			deployment.Spec.Strategy.Type == apps.RollingUpdateDeploymentStrategyType &&
			deployment.Annotations[partitiondeployment.AnnotationOriginalMinReadySeconds] == ""
	}, 10*time.Minute, time.Second).Should(BeTrue())
}

func waitMinReadyE2EBatchCondition(namespace, name, reason string) {
	Eventually(func() bool {
		release := &v1beta1.BatchRelease{}
		key := types.NamespacedName{Namespace: namespace, Name: name}
		if err := k8sClient.Get(context.TODO(), key, release); err != nil {
			if apierrors.IsNotFound(err) {
				return false
			}
			Expect(err).NotTo(HaveOccurred())
		}
		for _, condition := range release.Status.Conditions {
			if condition.Reason == reason {
				return true
			}
		}
		return false
	}, 5*time.Minute, time.Second).Should(BeTrue())
}

func startMinReadyE2ERollout(namespace string) *v1beta1.Rollout {
	rollout := newMinReadyE2ERollout(namespace)
	deployment := newMinReadyE2EDeployment(namespace)
	createMinReadyE2EObject(rollout)
	createMinReadyE2EObject(deployment)
	waitMinReadyE2EDeploymentReady(namespace)
	waitMinReadyE2ERolloutPhase(namespace, rollout.Name, v1beta1.RolloutPhaseHealthy)
	updateMinReadyE2EDeploymentVersion(namespace, "version2")
	waitMinReadyE2EDeploymentInflated(namespace)
	waitMinReadyE2EBatchCondition(namespace, rollout.Name, "MinReadyInitialized")
	return rollout
}
