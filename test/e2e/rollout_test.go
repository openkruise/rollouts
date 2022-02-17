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
	"k8s.io/apimachinery/pkg/api/errors"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
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
			if clone.Status.CanaryStatus.CurrentStepState != rolloutsv1alpha1.CanaryStepStatePaused {
				return nil
			}
			clone.Status.CanaryStatus.CurrentStepState = rolloutsv1alpha1.CanaryStepStateCompleted
			return k8sClient.Status().Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())
	}

	WaitDeploymentAllPodsReady := func(deployment *apps.Deployment) {
		Eventually(func() bool {
			clone := &apps.Deployment{}
			Expect(GetObject(deployment.Name, clone)).NotTo(HaveOccurred())
			return clone.Status.ObservedGeneration == clone.Generation && clone.Status.Replicas == clone.Status.ReadyReplicas
		}, time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutCanaryStepPaused := func(name string, stepIndex int32) {
		Eventually(func() bool {
			clone := &rolloutsv1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStatus == nil {
				return false
			}
			return clone.Status.CanaryStatus.CurrentStepIndex == stepIndex && clone.Status.CanaryStatus.CurrentStepState == rolloutsv1alpha1.CanaryStepStatePaused
		}, time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutStatusPhase := func(name string, phase rolloutsv1alpha1.RolloutPhase) {
		Eventually(func() bool {
			clone := &rolloutsv1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			return clone.Status.Phase == phase
		}, time.Minute, time.Second).Should(BeTrue())
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
		}, time.Minute, time.Second).Should(BeTrue())
	}

	GetCanaryDeployment := func(stable *apps.Deployment) (*apps.Deployment, error) {
		canaryList := &apps.DeploymentList{}
		selector, _ := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{util.CanaryDeploymentLabel: stable.Name}})
		err := k8sClient.List(context.TODO(), canaryList, &client.ListOptions{LabelSelector: selector})
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
		err := k8sClient.List(context.TODO(), appList, &client.ListOptions{LabelSelector: selector})
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
		k8sClient.DeleteAllOf(context.TODO(), &kruiseappsv1alpha1.CloneSet{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &rolloutsv1alpha1.BatchRelease{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &rolloutsv1alpha1.Rollout{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &v1.Service{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &netv1.Ingress{}, client.InNamespace(namespace))
		Expect(k8sClient.Delete(context.TODO(), &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
		time.Sleep(time.Second * 3)
	})

	KruiseDescribe("Deployment rollout canary nginx", func() {

		It("V1->V2: Percentage, 20%,40%,60%,80%,100% Succeeded", func() {
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
			time.Sleep(time.Second * 5)

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

			// wait step 0 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 0)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(""))
			canaryRevision := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 0))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			sRevision := stableRevision[strings.LastIndex(stableRevision, "-")+1:]
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(sRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cService)).NotTo(HaveOccurred())
			cRevision := canaryRevision[strings.LastIndex(canaryRevision, "-")+1:]
			Expect(cService.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(cRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.CanaryPlan.Steps[0].Weight)))
			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 1))
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 1))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutCanaryStepPaused(rollout.Name, 4)

			// check stable, canary service & ingress
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.CanaryPlan.Steps[4].Weight)))
			// canary deployment
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 5))
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 5))
			// stable deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)

			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))

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
		})

		It("V1->V2: Percentage, 20%, and rollback(v1)", func() {
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
			time.Sleep(time.Second * 3)
			apps, err := GetPodsOfDeployment(workload)
			appNames := make(map[string]struct{})
			for _, app := range apps {
				appNames[app.Name] = struct{}{}
			}
			Expect(err).NotTo(HaveOccurred())

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).ShouldNot(Equal(""))
			//stableRevision := rollout.Status.StableRevision
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
			// wait step 0 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 0)

			// rollback -> v1
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version1"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Rollback deployment env NODE_NAME from(version2) -> to(version1)")
			time.Sleep(time.Second * 2)

			By("wait rollout rollback complete, and healthy")
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			// check progressing canceled
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonCanceled))
			Expect(string(cond.Status)).Should(Equal("False"))

			// deployment pods not changed
			capps, err := GetPodsOfDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			cappNames := make(map[string]struct{})
			for _, app := range capps {
				cappNames[app.Name] = struct{}{}
			}
			Expect(cappNames).Should(Equal(appNames))
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
		})

		It("V1->V2: Percentage, 20%,40%,60% and continuous release v3", func() {
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
			time.Sleep(time.Second * 3)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.StableRevision).ShouldNot(Equal(""))
			stableRevision := rollout.Status.StableRevision
			//stableRevision := rollout.Status.StableRevision
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
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			By("check deployment status & paused success")
			// wait step 0 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 0)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(""))
			canaryRevisionV1 := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 0))

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
			WaitRolloutCanaryStepPaused(rollout.Name, 0)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(""))
			Expect(rollout.Status.CanaryRevision).ShouldNot(Equal(canaryRevisionV1))
			canaryRevisionV2 := rollout.Status.CanaryRevision
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 0))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			sRevision := stableRevision[strings.LastIndex(stableRevision, "-")+1:]
			Expect(service.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(sRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cService)).NotTo(HaveOccurred())
			cRevision := canaryRevisionV2[strings.LastIndex(canaryRevisionV2, "-")+1:]
			Expect(cService.Spec.Selector[util.RsPodRevisionLabelKey]).Should(Equal(cRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.CanaryPlan.Steps[0].Weight)))
			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 1))
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 1))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutCanaryStepPaused(rollout.Name, 4)

			// check stable, canary service & ingress
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(rollout.Status.CanaryStatus.CanaryService, cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", rollout.Spec.Strategy.CanaryPlan.Steps[4].Weight)))
			// canary deployment
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 5))
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 5))
			// stable deployment
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.Replicas).Should(BeNumerically("==", *workload.Spec.Replicas))
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			time.Sleep(time.Second * 3)

			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))

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
		})

		It("V1->V2: Percentage, 20%,40%,60%,80%,100% Succeeded, and scale up replicas from(5) -> to(10)", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.CanaryPlan.Steps = []rolloutsv1alpha1.CanaryStep{
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
			time.Sleep(time.Second * 5)

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

			// wait step 0 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			time.Sleep(time.Second * 3)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			canaryRevision := rollout.Status.CanaryStatus.CanaryRevision

			// scale up replicas, 5 -> 10
			workload.Spec.Replicas = utilpointer.Int32(10)
			UpdateDeployment(workload)
			time.Sleep(time.Second * 5)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(canaryRevision))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 10))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 10))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(canaryRevision))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			time.Sleep(time.Second * 3)

			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))

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
		})

		It("V1->V2: Percentage, 20%,40%,60%,80%,100% Succeeded, and scale down replicas from(10) -> to(5)", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.CanaryPlan.Steps = []rolloutsv1alpha1.CanaryStep{
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
			time.Sleep(time.Second * 5)

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
			time.Sleep(time.Second * 3)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			canaryRevision := rollout.Status.CanaryStatus.CanaryRevision

			// scale up replicas, 10 -> 5
			workload.Spec.Replicas = utilpointer.Int32(5)
			UpdateDeployment(workload)
			time.Sleep(time.Second * 5)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 6))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 6))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(canaryRevision))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 6))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 6))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(canaryRevision))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			time.Sleep(time.Second * 3)

			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))

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
		})

		It("V1->V2: Percentage, 20%,40%,60%,80%,100% Succeeded, paused and resume", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.CanaryPlan.Steps = []rolloutsv1alpha1.CanaryStep{
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
			time.Sleep(time.Second * 5)

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
			time.Sleep(time.Second * 2)

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
			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond = util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))

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
		})

		It("V1->V2: Percentage, 20%,40%,60%,80%,100%, progressing, but delete rollout crd", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.CanaryPlan.Steps = []rolloutsv1alpha1.CanaryStep{
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
			time.Sleep(time.Second * 5)

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
			time.Sleep(time.Second * 2)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			By("check deployment status & paused success")

			// paused rollout
			time.Sleep(time.Second * 10)
			Expect(k8sClient.DeleteAllOf(context.TODO(), &rolloutsv1alpha1.Rollout{}, client.InNamespace(namespace), client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
			WaitRolloutNotFound(rollout.Name)
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

func skip() bool {
	return true
}
