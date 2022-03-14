/*
Copyright 2022 The Kruise Authors.

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
	"sort"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	nginxIngressAnnotationDefaultPrefix = "nginx.ingress.kubernetes.io"
)

var _ = SIGDescribe("Rollout", func() {
	var namespace string

	CreateObject := func(object client.Object, options ...client.CreateOption) {
		object.SetNamespace(namespace)
		Expect(k8sClient.Create(context.TODO(), object)).NotTo(HaveOccurred())
	}

	GetObject := func(name string, object client.Object) error {
		key := types.NamespacedName{Namespace: namespace, Name: name}
		return k8sClient.Get(context.TODO(), key, object)
	}

	UpdateDeployment := func(object *apps.Deployment) *apps.Deployment {
		var clone *apps.Deployment
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone = &apps.Deployment{}
			err := GetObject(object.Name, clone)
			if err != nil {
				return err
			}
			clone.Spec = *object.Spec.DeepCopy()
			return k8sClient.Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())

		return clone
	}

	UpdateCloneSet := func(object *appsv1alpha1.CloneSet) *appsv1alpha1.CloneSet {
		var clone *appsv1alpha1.CloneSet
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone = &appsv1alpha1.CloneSet{}
			err := GetObject(object.Name, clone)
			if err != nil {
				return err
			}
			clone.Spec = *object.Spec.DeepCopy()
			return k8sClient.Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())

		return clone
	}

	UpdateRollout := func(object *rolloutsv1alpha1.Rollout) *rolloutsv1alpha1.Rollout {
		var clone *rolloutsv1alpha1.Rollout
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone = &rolloutsv1alpha1.Rollout{}
			err := GetObject(object.Name, clone)
			if err != nil {
				return err
			}
			clone.Spec = *object.Spec.DeepCopy()
			return k8sClient.Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())

		return clone
	}

	ResumeRolloutCanary := func(name string) {
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone := &rolloutsv1alpha1.Rollout{}
			err := GetObject(name, clone)
			if err != nil {
				return err
			}
			clone.Status.CanaryStatus.CurrentStepState = rolloutsv1alpha1.CanaryStepStateReady
			return k8sClient.Status().Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())
	}

	WaitDeploymentAllPodsReady := func(deployment *apps.Deployment) {
		Eventually(func() bool {
			clone := &apps.Deployment{}
			Expect(GetObject(deployment.Name, clone)).NotTo(HaveOccurred())
			return clone.Status.ObservedGeneration == clone.Generation && *clone.Spec.Replicas == clone.Status.UpdatedReplicas &&
				*clone.Spec.Replicas == clone.Status.ReadyReplicas && *clone.Spec.Replicas == clone.Status.Replicas
		}, 5*time.Minute, time.Second).Should(BeTrue())
	}

	WaitCloneSetAllPodsReady := func(cloneset *appsv1alpha1.CloneSet) {
		Eventually(func() bool {
			clone := &appsv1alpha1.CloneSet{}
			Expect(GetObject(cloneset.Name, clone)).NotTo(HaveOccurred())
			return clone.Status.ObservedGeneration == clone.Generation && *clone.Spec.Replicas == clone.Status.UpdatedReplicas &&
				*clone.Spec.Replicas == clone.Status.ReadyReplicas && *clone.Spec.Replicas == clone.Status.Replicas
		}, 5*time.Minute, time.Second).Should(BeTrue())
	}

	WaitDeploymentReplicas := func(deployment *apps.Deployment) {
		Eventually(func() bool {
			clone := &apps.Deployment{}
			Expect(GetObject(deployment.Name, clone)).NotTo(HaveOccurred())
			return clone.Status.ObservedGeneration == clone.Generation &&
				*clone.Spec.Replicas == clone.Status.ReadyReplicas && *clone.Spec.Replicas == clone.Status.Replicas
		}, 5*time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutCanaryStepPaused := func(name string, stepIndex int32) {
		Eventually(func() bool {
			clone := &rolloutsv1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStatus == nil {
				return false
			}
			return clone.Status.CanaryStatus.CurrentStepIndex == stepIndex && clone.Status.CanaryStatus.CurrentStepState == rolloutsv1alpha1.CanaryStepStatePaused
		}, 5*time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutStatusPhase := func(name string, phase rolloutsv1alpha1.RolloutPhase) {
		Eventually(func() bool {
			clone := &rolloutsv1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			return clone.Status.Phase == phase
		}, 5*time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutNotFound := func(name string) {
		Eventually(func() bool {
			clone := &rolloutsv1alpha1.Rollout{}
			err := GetObject(name, clone)
			if err == nil {
				return false
			} else if errors.IsNotFound(err) {
				return true
			} else {
				Expect(err).NotTo(HaveOccurred())
				return false
			}
		}, 5*time.Minute, time.Second).Should(BeTrue())
	}

	GetCanaryDeployment := func(stable *apps.Deployment) (*apps.Deployment, error) {
		canaryList := &apps.DeploymentList{}
		selector, _ := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{util.CanaryDeploymentLabel: stable.Name}})
		err := k8sClient.List(context.TODO(), canaryList, &client.ListOptions{Namespace: stable.Namespace, LabelSelector: selector})
		if err != nil {
			return nil, err
		} else if len(canaryList.Items) == 0 {
			return nil, nil
		}
		sort.Slice(canaryList.Items, func(i, j int) bool {
			return canaryList.Items[j].CreationTimestamp.Before(&canaryList.Items[i].CreationTimestamp)
		})
		return &canaryList.Items[0], nil
	}

	GetPodsOfDeployment := func(obj *apps.Deployment) ([]*v1.Pod, error) {
		appList := &v1.PodList{}
		selector, _ := metav1.LabelSelectorAsSelector(obj.Spec.Selector)
		err := k8sClient.List(context.TODO(), appList, &client.ListOptions{Namespace: obj.Namespace, LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		apps := make([]*v1.Pod, 0)
		for i := range appList.Items {
			pod := &appList.Items[i]
			if pod.DeletionTimestamp.IsZero() {
				apps = append(apps, pod)
			}
		}
		return apps, nil
	}

	BeforeEach(func() {
		namespace = randomNamespaceName("rollout")
		ns := v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		Expect(k8sClient.Create(context.TODO(), &ns)).Should(SatisfyAny(BeNil()))
	})

	AfterEach(func() {
		By("[TEST] Clean up resources after an integration test")
		k8sClient.DeleteAllOf(context.TODO(), &apps.Deployment{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &appsv1alpha1.CloneSet{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &rolloutsv1alpha1.BatchRelease{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &rolloutsv1alpha1.Rollout{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &v1.Service{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &netv1.Ingress{}, client.InNamespace(namespace))
		Expect(k8sClient.Delete(context.TODO(), &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
		time.Sleep(time.Second * 3)
	})

	KruiseDescribe("Deployment rollout canary nginx", func() {
		It("V1->V2: Percentage 20%,40%,60%,80%,90%, and replicas=3", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight: 20,
				},
				{
					Weight: 40,
				},
				{
					Weight: 60,
				},
				{
					Weight: 80,
				},
				{
					Weight: 90,
				},
			}
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			workload.Spec.Replicas = utilpointer.Int32(3)
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 2)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 1))
			cIngress := &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("20"))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("40"))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("60"))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 4)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 3))
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("80"))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 5)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 3))
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("90"))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")
			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService := &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})

		It("V1->V2: Percentage 20%,60% Succeeded", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			replicas := intstr.FromInt(2)
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight:   20,
					Replicas: &replicas,
					Pause:    rolloutsv1alpha1.RolloutPause{},
				},
				{
					Weight: 60,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
			}
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).ShouldNot(Equal(""))
			stableRevision := rollout.Status.StableRevision
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 2)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			By("check deployment status & paused success")

			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(""))
			canaryRevision := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))

			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.Canary.Steps[0].Weight)))
			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 2))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 2))
			for _, env := range cWorkload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutCanaryStepPaused(rollout.Name, 2)

			// check stable, canary service & ingress
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.Canary.Steps[1].Weight)))
			// canary deployment
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 3))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 3))
			// stable deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")
			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService = &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.StableRevision).Should(Equal(canaryRevision))
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))

			// scale up replicas 5 -> 6
			workload.Spec.Replicas = utilpointer.Int32(6)
			UpdateDeployment(workload)
			By("Update deployment replicas from(5) -> to(6)")
			time.Sleep(time.Second * 2)

			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})

		It("V1->V2: Percentage 20%, and rollback(v1)", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)
			apps, err := GetPodsOfDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			appNames := make(map[string]struct{})
			for _, app := range apps {
				appNames[app.Name] = struct{}{}
			}

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).ShouldNot(Equal(""))
			stableRevision := rollout.Status.StableRevision
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 2)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			By("check deployment status & paused success")
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// rollback -> v1
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version1"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Rollback deployment env NODE_NAME from(version2) -> to(version1)")
			time.Sleep(time.Second * 2)

			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService := &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version1"))
				}
			}
			// deployment pods not changed
			capps, err := GetPodsOfDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			cappNames := make(map[string]struct{})
			for _, app := range capps {
				cappNames[app.Name] = struct{}{}
			}
			Expect(cappNames).Should(Equal(appNames))
			// check progressing canceled
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonCanceled))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(string(cond.Status)).Should(Equal("False"))
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})

		It("V1->V2: Percentage 20%,40% and continuous release v3", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).ShouldNot(Equal(""))
			stableRevision := rollout.Status.StableRevision
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 2)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			By("check deployment status & paused success")
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(""))
			canaryRevisionV1 := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			time.Sleep(time.Second * 15)

			// v1 -> v2 -> v3, continuous release
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version3"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version2) -> to(version3)")
			time.Sleep(time.Second * 2)

			// wait step 0 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(""))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(canaryRevisionV1))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 1))
			canaryRevisionV2 := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(canaryRevisionV2))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.Canary.Steps[0].Weight)))
			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 1))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 1))
			for _, env := range cWorkload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version3"))
				}
			}

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService = &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version3"))
				}
			}
			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.StableRevision).Should(Equal(canaryRevisionV2))
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})

		It("V1->V2: Percentage 20%,40% and scale up replicas from(5) -> to(10)", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight: 20,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					Weight: 40,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
				{
					Weight: 60,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					Weight: 100,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
			}
			CreateObject(rollout)
			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 2)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			By("check deployment status & paused success")

			// wait step 2 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			canaryRevision := rollout.Status.CanaryStatus.CanaryRevision

			// scale up replicas, 5 -> 10
			workload.Spec.Replicas = utilpointer.Int32(10)
			UpdateDeployment(workload)
			time.Sleep(time.Second * 3)
			cWorkload, _ := GetCanaryDeployment(workload)
			WaitDeploymentAllPodsReady(cWorkload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(canaryRevision))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService := &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.StableRevision).Should(Equal(canaryRevision))
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})

		It("V1->V2: Percentage 20%,40%, and scale down replicas from(10) -> to(5)", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight: 20,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					Weight: 40,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					Weight: 60,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
				{
					Weight: 100,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
			}
			CreateObject(rollout)
			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			workload.Spec.Replicas = utilpointer.Int32(10)
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2)")
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			By("check deployment status & paused success")

			// wait step 3 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			canaryRevision := rollout.Status.CanaryStatus.CanaryRevision

			// scale up replicas, 10 -> 5
			workload.Spec.Replicas = utilpointer.Int32(5)
			UpdateDeployment(workload)
			time.Sleep(time.Second * 3)
			WaitDeploymentReplicas(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 6))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 6))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(canaryRevision))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService := &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.StableRevision).Should(Equal(canaryRevision))
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})

		It("V1->V2: Percentage 20%,40%, paused and resume", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight: 20,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					Weight: 40,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					Weight: 60,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					Weight: 80,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					Weight: 100,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
			}
			CreateObject(rollout)
			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).ShouldNot(Equal(""))
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 3)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			By("check deployment status & paused success")

			// paused rollout
			time.Sleep(time.Second * 10)
			rollout.Spec.Strategy.Paused = true
			UpdateRollout(rollout)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			cIndex := rollout.Status.CanaryStatus.CurrentStepIndex
			time.Sleep(time.Second * 15)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonPaused))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", cIndex))

			// resume rollout
			rollout.Spec.Strategy.Paused = false
			UpdateRollout(rollout)
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService := &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond = util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})

		It("V1->V2: Percentage 20%,40%, but delete rollout crd", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight: 20,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					Weight: 60,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					Weight: 100,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
			}
			CreateObject(rollout)
			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).ShouldNot(Equal(""))
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 3)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			By("check deployment status & paused success")

			// delete rollout
			Expect(k8sClient.DeleteAllOf(context.TODO(), &rolloutsv1alpha1.Rollout{}, client.InNamespace(namespace), client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
			WaitRolloutNotFound(rollout.Name)
			WaitDeploymentAllPodsReady(workload)
			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService := &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
		})

		It("V1->V2: Percentage 20% v2 failed image, and v3 succeed image", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight: 20,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					Weight: 60,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					Weight: 100,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
			}
			CreateObject(rollout)
			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).ShouldNot(Equal(""))
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Image = "echoserver:failed"
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment image from(v1.0.0) -> to(failed)")
			time.Sleep(time.Second * 3)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			By("check deployment status & paused success")
			time.Sleep(time.Second * 10)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 0))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateUpgrade))

			// update success image, v3
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version3"})
			workload.Spec.Template.Spec.Containers[0].Image = "cilium/echoserver:latest"
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment image from(v2) -> to(v3)")
			// wait rollout complete
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService := &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version3"))
				}
			}
			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})

		It("V1->V2: Percentage, 20%,40%,60%,80%,100%, steps changed v1", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			CreateObject(rollout)
			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			workload.Spec.Replicas = utilpointer.Int32(10)
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			stableRevision := rollout.Status.StableRevision

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment image from(version1) -> to(version2)")
			time.Sleep(time.Second * 3)
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// update rollout step configuration
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight: 10,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
				{
					Weight: 30,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
				{
					Weight: 100,
					Pause: rolloutsv1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
			}
			rollout = UpdateRollout(rollout)
			time.Sleep(time.Second * 3)
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(""))
			canaryRevision := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.Canary.Steps[1].Weight)))
			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 3))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 3))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			// wait rollout complete
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService = &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})
	})

	KruiseDescribe("CloneSet rollout canary nginx", func() {

		It("V1->V2: Percentage, 20%,40%,60%,80%,100% Succeeded", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight: 20,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
				{
					Weight: 60,
					Pause:  rolloutsv1alpha1.RolloutPause{},
				},
			}
			rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
				APIVersion: "apps.kruise.io/v1alpha1",
				Kind:       "CloneSet",
				Name:       "echoserver",
			}
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &appsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/rollout/cloneset.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitCloneSetAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).Should(Equal(workload.Status.CurrentRevision))
			stableRevision := rollout.Status.StableRevision
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateCloneSet(workload)
			By("Update cloneSet env NODE_NAME from(version1) -> to(version2)")
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Spec.UpdateStrategy.Partition.IntVal).Should(BeNumerically("==", 4))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).Should(Equal(workload.Status.UpdateRevision))
			canaryRevision := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.Canary.Steps[0].Weight)))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutCanaryStepPaused(rollout.Name, 2)

			// check stable, canary service & ingress
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.Canary.Steps[1].Weight)))
			// cloneset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Spec.UpdateStrategy.Partition.IntVal).Should(BeNumerically("==", 2))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			WaitCloneSetAllPodsReady(workload)
			By("rollout completed, and check")

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService = &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// cloneset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Spec.UpdateStrategy.Partition.IntVal).Should(BeNumerically("==", 0))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			Expect(workload.Status.CurrentRevision).Should(ContainSubstring(canaryRevision))
			Expect(workload.Status.UpdateRevision).Should(ContainSubstring(canaryRevision))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			time.Sleep(time.Second * 3)

			// check progressing succeed
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
			Expect(rollout.Status.StableRevision).Should(Equal(canaryRevision))

			// scale up replicas 5 -> 6
			workload.Spec.Replicas = utilpointer.Int32(6)
			UpdateCloneSet(workload)
			By("Update cloneSet replicas from(5) -> to(6)")
			time.Sleep(time.Second * 2)

			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
		})

		It("V1->V2: Percentage, 20%, and rollback(v1)", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
				APIVersion: "apps.kruise.io/v1alpha1",
				Kind:       "CloneSet",
				Name:       "echoserver",
			}
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &appsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/rollout/cloneset.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitCloneSetAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).Should(Equal(workload.Status.CurrentRevision))
			stableRevision := rollout.Status.StableRevision
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Image = "echoserver:failed"
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateCloneSet(workload)
			By("Update cloneSet env NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 10)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Spec.UpdateStrategy.Partition.IntVal).Should(BeNumerically("==", 4))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).Should(Equal(workload.Status.UpdateRevision))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateUpgrade))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))

			// rollback -> v1
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version1"})
			workload.Spec.Template.Spec.Containers[0].Image = "cilium/echoserver:latest"
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateCloneSet(workload)
			By("Rollback deployment env NODE_NAME from(version2) -> to(version1)")
			time.Sleep(time.Second * 2)

			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			WaitCloneSetAllPodsReady(workload)
			By("rollout completed, and check")
			// check progressing canceled
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonCanceled))
			Expect(string(cond.Status)).Should(Equal("False"))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService := &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// cloneset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Spec.UpdateStrategy.Partition.IntVal).Should(BeNumerically("==", 0))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			Expect(workload.Status.CurrentRevision).Should(ContainSubstring(stableRevision))
			Expect(workload.Status.UpdateRevision).Should(ContainSubstring(stableRevision))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version1"))
				}
			}
		})

		It("V1->V2: Percentage, 20%,40% and continuous release v3", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
				APIVersion: "apps.kruise.io/v1alpha1",
				Kind:       "CloneSet",
				Name:       "echoserver",
			}
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			CreateObject(ingress)
			// workload
			workload := &appsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/rollout/cloneset.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitCloneSetAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).Should(Equal(workload.Status.CurrentRevision))
			stableRevision := rollout.Status.StableRevision
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateCloneSet(workload)
			By("Update cloneSet env NODE_NAME from(version1) -> to(version2)")
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Spec.UpdateStrategy.Partition.IntVal).Should(BeNumerically("==", 4))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).Should(Equal(workload.Status.UpdateRevision))
			canaryRevisionV1 := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			time.Sleep(time.Second * 15)

			// v1 -> v2 -> v3, continuous release
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version3"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateCloneSet(workload)
			By("Update cloneSet env NODE_NAME from(version2) -> to(version3)")
			time.Sleep(time.Second * 10)

			// wait step 0 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(canaryRevisionV1))
			Expect(rollout.Status.CanaryRevision).Should(Equal(workload.Status.UpdateRevision))
			canaryRevisionV2 := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(canaryRevisionV2))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.Canary.Steps[0].Weight)))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(""))
			cService = &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// cloneset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Spec.UpdateStrategy.Partition.IntVal).Should(BeNumerically("==", 0))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			Expect(workload.Status.CurrentRevision).Should(ContainSubstring(canaryRevisionV2))
			Expect(workload.Status.UpdateRevision).Should(ContainSubstring(canaryRevisionV2))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version3"))
				}
			}
			time.Sleep(time.Second * 3)

			// check progressing succeed
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(rollout.Status.CanaryStatus.ObservedWorkloadGeneration).Should(BeNumerically("==", workload.Generation))
			Expect(rollout.Status.StableRevision).Should(Equal(canaryRevisionV2))
		})
	})
})

func mergeEnvVar(original []v1.EnvVar, add v1.EnvVar) []v1.EnvVar {
	newEnvs := make([]v1.EnvVar, 0)
	for _, env := range original {
		if add.Name == env.Name {
			continue
		}
		newEnvs = append(newEnvs, env)
	}
	newEnvs = append(newEnvs, add)
	return newEnvs
}
