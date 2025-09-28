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

//READ - instead of adding test cases or modifying existing test cases in this file,
// we recommend to create new test cases or rewrite existing test cases in test/e2e/rollout_v1beta1_test.go,
// which use v1beta1 Rollout instead of v1alpha1.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

const (
	nginxIngressAnnotationDefaultPrefix = "nginx.ingress.kubernetes.io"
	OriginalSpecAnnotation              = "rollouts.kruise.io/original-spec-configuration"
)

func getRolloutCondition(status v1alpha1.RolloutStatus, condType v1alpha1.RolloutConditionType) *v1alpha1.RolloutCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

func getRolloutConditionV1beta1(status v1beta1.RolloutStatus, condType v1beta1.RolloutConditionType) *v1beta1.RolloutCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

var _ = SIGDescribe("Rollout", func() {
	var namespace string

	DumpAllResources := func() {
		rollout := &v1alpha1.RolloutList{}
		_ = k8sClient.List(context.TODO(), rollout, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(rollout))
		batch := &v1alpha1.BatchReleaseList{}
		_ = k8sClient.List(context.TODO(), batch, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(batch))
		deploy := &apps.DeploymentList{}
		_ = k8sClient.List(context.TODO(), deploy, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(deploy))
		rs := &apps.ReplicaSetList{}
		_ = k8sClient.List(context.TODO(), rs, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(rs))
		cloneSet := &appsv1alpha1.CloneSetList{}
		_ = k8sClient.List(context.TODO(), cloneSet, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(cloneSet))
		sts := &apps.StatefulSetList{}
		_ = k8sClient.List(context.TODO(), sts, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(sts))
		asts := &appsv1beta1.StatefulSetList{}
		_ = k8sClient.List(context.TODO(), asts, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(asts))
	}

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
			clone.Spec.Replicas = utilpointer.Int32(*object.Spec.Replicas)
			clone.Spec.Template = *object.Spec.Template.DeepCopy()
			clone.Labels = mergeMap(clone.Labels, object.Labels)
			clone.Annotations = mergeMap(clone.Annotations, object.Annotations)
			clone.Spec.Paused = object.Spec.Paused
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
			clone.Spec.Replicas = utilpointer.Int32(*object.Spec.Replicas)
			clone.Spec.Template = *object.Spec.Template.DeepCopy()
			clone.Labels = mergeMap(clone.Labels, object.Labels)
			clone.Annotations = mergeMap(clone.Annotations, object.Annotations)
			return k8sClient.Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())

		return clone
	}

	UpdateDaemonSet := func(object *appsv1alpha1.DaemonSet) *appsv1alpha1.DaemonSet {
		var daemon *appsv1alpha1.DaemonSet
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			daemon = &appsv1alpha1.DaemonSet{}
			err := GetObject(object.Name, daemon)
			if err != nil {
				return err
			}
			// daemon.Spec.Replicas = utilpointer.Int32(*object.Spec.Replicas)
			daemon.Spec.Template = *object.Spec.Template.DeepCopy()
			daemon.Spec.UpdateStrategy = *object.Spec.UpdateStrategy.DeepCopy()
			daemon.Labels = mergeMap(daemon.Labels, object.Labels)
			daemon.Annotations = mergeMap(daemon.Annotations, object.Annotations)
			return k8sClient.Update(context.TODO(), daemon)
		})).NotTo(HaveOccurred())

		return daemon
	}

	UpdateRollout := func(object *v1alpha1.Rollout) *v1alpha1.Rollout {
		var clone *v1alpha1.Rollout
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone = &v1alpha1.Rollout{}
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
		clone := &v1alpha1.Rollout{}
		Expect(GetObject(name, clone)).NotTo(HaveOccurred())
		currentIndex := clone.Status.CanaryStatus.CurrentStepIndex
		Eventually(func() bool {
			clone := &v1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStatus.CurrentStepIndex == currentIndex && clone.Status.CanaryStatus.CurrentStepState == v1alpha1.CanaryStepStatePaused {
				klog.Info("patch to stepReady")
				body := fmt.Sprintf(`{"status":{"canaryStatus":{"currentStepState":"%s"}}}`, v1alpha1.CanaryStepStateReady)
				Expect(k8sClient.Status().Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))).NotTo(HaveOccurred())
				return false
			} else {
				fmt.Println("resume rollout success, and CurrentStepState", util.DumpJSON(clone.Status))
				return true
			}
			// interval was critical before:
			// too small: StepReady could be overidden by StepPaused
			// too big: StepReady could progress to StepPaused of next Step
		}, 10*time.Second, 2*time.Second).Should(BeTrue())
	}

	ResumeRolloutCanaryV1beta1 := func(name string) {
		Eventually(func() bool {
			clone := &v1beta1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStatus.CurrentStepState != v1beta1.CanaryStepStatePaused {
				fmt.Println("resume rollout success, and CurrentStepState", util.DumpJSON(clone.Status))
				return true
			}

			body := fmt.Sprintf(`{"status":{"canaryStatus":{"currentStepState":"%s"}}}`, v1beta1.CanaryStepStateReady)
			Expect(k8sClient.Status().Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))).NotTo(HaveOccurred())
			return false
		}, 10*time.Second, time.Second).Should(BeTrue())
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

	WaitDaemonSetAllPodsReady := func(daemonset *appsv1alpha1.DaemonSet) {
		Eventually(func() bool {
			daemon := &appsv1alpha1.DaemonSet{}
			Expect(GetObject(daemonset.Name, daemon)).NotTo(HaveOccurred())
			klog.Infof("DaemonSet updateStrategy(%s) Generation(%d) ObservedGeneration(%d) DesiredNumberScheduled(%d) UpdatedNumberScheduled(%d) NumberReady(%d)",
				util.DumpJSON(daemon.Spec.UpdateStrategy), daemon.Generation, daemon.Status.ObservedGeneration, daemon.Status.DesiredNumberScheduled, daemon.Status.UpdatedNumberScheduled, daemon.Status.NumberReady)
			return daemon.Status.ObservedGeneration == daemon.Generation && daemon.Status.DesiredNumberScheduled == daemon.Status.UpdatedNumberScheduled && daemon.Status.DesiredNumberScheduled == daemon.Status.NumberReady
		}, 5*time.Minute, time.Second).Should(BeTrue())
	}

	WaitDeploymentReplicas := func(deployment *apps.Deployment) {
		Eventually(func() bool {
			clone := &apps.Deployment{}
			Expect(GetObject(deployment.Name, clone)).NotTo(HaveOccurred())
			return clone.Status.ObservedGeneration == clone.Generation &&
				*clone.Spec.Replicas == clone.Status.ReadyReplicas && *clone.Spec.Replicas == clone.Status.Replicas
		}, 10*time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutCanaryStepPaused := func(name string, stepIndex int32) {
		start := time.Now()
		Eventually(func() bool {
			if start.Add(time.Minute * 5).Before(time.Now()) {
				DumpAllResources()
				Expect(true).Should(BeFalse())
			}
			clone := &v1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStatus == nil {
				return false
			}
			klog.Infof("current step:%v target step:%v current step state %v", clone.Status.CanaryStatus.CurrentStepIndex, stepIndex, clone.Status.CanaryStatus.CurrentStepState)
			return clone.Status.CanaryStatus.CurrentStepIndex == stepIndex && clone.Status.CanaryStatus.CurrentStepState == v1alpha1.CanaryStepStatePaused
		}, 20*time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutStatusPhase := func(name string, phase v1alpha1.RolloutPhase) {
		Eventually(func() bool {
			clone := &v1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			return clone.Status.Phase == phase
		}, 20*time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutWorkloadGeneration := func(name string, generation int64) {
		Eventually(func() bool {
			clone := &v1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			return clone.Status.CanaryStatus.ObservedWorkloadGeneration == generation
		}, time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutNotFound := func(name string) {
		Eventually(func() bool {
			clone := &v1alpha1.Rollout{}
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

	ListPods := func(namespace string, labelSelector *metav1.LabelSelector) ([]*v1.Pod, error) {
		appList := &v1.PodList{}
		selector, _ := metav1.LabelSelectorAsSelector(labelSelector)
		err := k8sClient.List(context.TODO(), appList, &client.ListOptions{Namespace: namespace, LabelSelector: selector})
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
		_ = k8sClient.DeleteAllOf(context.TODO(), &apps.Deployment{}, client.InNamespace(namespace))
		_ = k8sClient.DeleteAllOf(context.TODO(), &appsv1alpha1.CloneSet{}, client.InNamespace(namespace))
		_ = k8sClient.DeleteAllOf(context.TODO(), &v1alpha1.BatchRelease{}, client.InNamespace(namespace))
		_ = k8sClient.DeleteAllOf(context.TODO(), &v1alpha1.Rollout{}, client.InNamespace(namespace))
		_ = k8sClient.DeleteAllOf(context.TODO(), &v1.Service{}, client.InNamespace(namespace))
		_ = k8sClient.DeleteAllOf(context.TODO(), &netv1.Ingress{}, client.InNamespace(namespace))
		Expect(k8sClient.Delete(context.TODO(), &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
		time.Sleep(time.Second * 3)
	})

	KruiseDescribe("Deployment canary rollout with Ingress", func() {
		It("Deployment V1->V2: Percentage 20%,40%,60%,80%,90%, and replicas=3", func() {
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(40),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(60),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(80),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(90),
					},
				},
			}
			rollout.Spec.Strategy.Canary.PatchPodTemplateMetadata = &v1alpha1.PatchPodTemplateMetadata{
				Labels:      map[string]string{"pod": "canary"},
				Annotations: map[string]string{"pod": "canary"},
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
			workload.Spec.Template.Labels["pod"] = "stable"
			workload.Spec.Template.Annotations = map[string]string{
				"pod": "stable",
			}
			workload.Spec.Template.Labels[apps.DefaultDeploymentUniqueLabelKey] = "abcdefg"
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
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
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("20"))
			canaryWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(canaryWorkload.Spec.Template.Annotations["pod"]).Should(Equal("canary"))
			Expect(canaryWorkload.Spec.Template.Labels["pod"]).Should(Equal("canary"))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(2)")
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("40"))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(3)")
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("60"))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(4)")
			WaitRolloutCanaryStepPaused(rollout.Name, 4)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 3))
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("80"))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(5)")
			WaitRolloutCanaryStepPaused(rollout.Name, 5)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 3))
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("90"))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")
			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Percentage 20%,60% Succeeded", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			replicas := intstr.FromInt(2)
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
					Replicas: &replicas,
					Pause:    v1alpha1.RolloutPause{},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(60),
					},
					Pause: v1alpha1.RolloutPause{},
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
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
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

			// check stable, canary service & ingress
			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err := finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			canaryRevision := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 2))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 2))
			for _, env := range cWorkload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			// stable service
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", *rollout.Spec.Strategy.Canary.Steps[0].Weight)))

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevision))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(2)")
			WaitRolloutCanaryStepPaused(rollout.Name, 2)

			// check stable, canary service & ingress
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", *rollout.Spec.Strategy.Canary.Steps[1].Weight)))
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
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")
			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			rss, err = finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			Expect(rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))

			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			//Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(canaryRevision))
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)

			// scale up replicas 5 -> 6
			workload.Spec.Replicas = utilpointer.Int32(6)
			UpdateDeployment(workload)
			By("Update deployment replicas from(5) -> to(6)")
			time.Sleep(time.Second * 2)

			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Percentage 20%, and rollback-v1", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
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
			pods, err := ListPods(workload.Name, workload.Spec.Selector)
			Expect(err).NotTo(HaveOccurred())
			appNames := make(map[string]struct{})
			for _, app := range pods {
				appNames[app.Name] = struct{}{}
			}
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
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

			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)
			time.Sleep(time.Second * 10)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cpods, err := ListPods(workload.Name, workload.Spec.Selector)
			Expect(err).NotTo(HaveOccurred())
			cappNames := make(map[string]struct{})
			for _, pod := range cpods {
				cappNames[pod.Name] = struct{}{}
			}
			Expect(cappNames).Should(Equal(appNames))
			// check progressing canceled
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Percentage 20%,40% and continuous release v3", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(40),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(60),
					},
					Pause: v1alpha1.RolloutPause{Duration: utilpointer.Int32(10)},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(80),
					},
					Pause: v1alpha1.RolloutPause{Duration: utilpointer.Int32(10)},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(100),
					},
					Pause: v1alpha1.RolloutPause{Duration: utilpointer.Int32(1)},
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
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
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

			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err := finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			canaryRevisionV1 := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevisionV1))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 2)

			// v1 -> v2 -> v3, continuous release
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version3"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version2) -> to(version3)")
			time.Sleep(time.Second * 2)
			// wait step 0 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err = finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			canaryRevisionV2 := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevisionV2))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevisionV2))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", *rollout.Spec.Strategy.Canary.Steps[0].Weight)))
			// canary deployment
			cWorkload, err = GetCanaryDeployment(workload)
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
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Percentage 20%,40% and scale up replicas from(5) -> to(10)", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(40),
					},
					Pause: v1alpha1.RolloutPause{},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(60),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(100),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(0),
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
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
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

			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err := finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			canaryRevision := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevision))

			// scale up replicas, 5 -> 10
			workload.Spec.Replicas = utilpointer.Int32(10)
			UpdateDeployment(workload)
			time.Sleep(time.Second * 3)
			cWorkload, _ = GetCanaryDeployment(workload)
			WaitDeploymentAllPodsReady(cWorkload)
			time.Sleep(time.Second * 5)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(v1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevision))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			//Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(canaryRevision))
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Percentage 20%,40%, and scale down replicas from(10) -> to(5)", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(40),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(60),
					},
					Pause: v1alpha1.RolloutPause{},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(100),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(0),
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
			workload.Spec.Replicas = utilpointer.Int32(10)
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
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

			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err := finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			canaryRevision := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))

			// scale up replicas, 10 -> 5
			workload.Spec.Replicas = utilpointer.Int32(5)
			UpdateDeployment(workload)
			time.Sleep(time.Second * 3)
			WaitDeploymentReplicas(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 6))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 6))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(v1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevision))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			//Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(canaryRevision))
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Percentage 20%,40%, paused and resume", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(40),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(60),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(80),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(100),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(0),
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
			// deployment
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
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
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			cIndex := rollout.Status.CanaryStatus.CurrentStepIndex
			time.Sleep(time.Second * 15)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonPaused))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", cIndex))

			// resume rollout
			rollout.Spec.Strategy.Paused = false
			UpdateRollout(rollout)
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Percentage 20%,40%, but delete rollout crd", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(60),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(100),
					},
					Pause: v1alpha1.RolloutPause{
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
			// deployment
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
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
			Expect(k8sClient.DeleteAllOf(context.TODO(), &v1alpha1.Rollout{}, client.InNamespace(namespace), client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
			WaitRolloutNotFound(rollout.Name)
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			workload.Spec.Paused = false
			UpdateDeployment(workload)
			By("Update deployment paused=false")
			WaitDeploymentAllPodsReady(workload)
			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(60),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(100),
					},
					Pause: v1alpha1.RolloutPause{
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
			// deployment
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
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
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 0))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(v1alpha1.CanaryStepStateUpgrade))

			// update success image, v3
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version3"})
			workload.Spec.Template.Spec.Containers[0].Image = "cilium/echoserver:latest"
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			workload.Spec.Template.Spec.Containers[0].ImagePullPolicy = v1.PullIfNotPresent
			UpdateDeployment(workload)
			By("Update deployment image from(v2) -> to(v3)")
			// wait rollout complete
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress := &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Percentage, 20%,40%,60%,80%,100%, steps changed v1", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
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
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment image from(version1) -> to(version2)")
			time.Sleep(time.Second * 3)
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// update rollout step configuration
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(10),
					},
					Pause: v1alpha1.RolloutPause{},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(30),
					},
					Pause: v1alpha1.RolloutPause{},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(100),
					},
					Pause: v1alpha1.RolloutPause{
						Duration: utilpointer.Int32(5),
					},
				},
			}
			rollout = UpdateRollout(rollout)
			By("update rollout configuration, and wait rollout next step(2)")
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			batch := &v1alpha1.BatchRelease{}
			Expect(GetObject(rollout.Name, batch)).NotTo(HaveOccurred())

			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err := finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			canaryRevision := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevision))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", *rollout.Spec.Strategy.Canary.Steps[1].Weight)))
			// canary deployment
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 3))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 3))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			// wait rollout complete
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)

			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: A/B testing, header & cookies", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			headerType := gatewayv1beta1.HeaderMatchRegularExpression
			replica1 := intstr.FromInt(1)
			replica2 := intstr.FromInt(2)
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Matches: []v1alpha1.HttpRouteMatch{
							{
								Headers: []gatewayv1beta1.HTTPHeaderMatch{
									{
										Type:  &headerType,
										Name:  "user_id",
										Value: "123456",
									},
								},
							},
							{
								Headers: []gatewayv1beta1.HTTPHeaderMatch{
									{
										Name:  "canary-by-cookie",
										Value: "demo",
									},
								},
							},
						},
					},
					Pause:    v1alpha1.RolloutPause{},
					Replicas: &replica1,
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(30),
					},
					Replicas: &replica2,
					Pause:    v1alpha1.RolloutPause{},
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
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment image from(version1) -> to(version2)")
			time.Sleep(time.Second * 3)
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err := finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			canaryRevision := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevision))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-by-header", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("user_id"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-by-header-pattern", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("123456"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-by-cookie", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("demo"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(""))
			// canary deployment
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 1))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 1))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			// wait step 2 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-by-header", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(""))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-by-header-pattern", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(""))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-by-cookie", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(""))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("30"))
			// canary deployment
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 2))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 2))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			// wait rollout complete
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)
			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: A/B testing, aliyun-alb, header & cookies", func() {
			finder := util.NewControllerFinder(k8sClient)
			configmap := &v1.ConfigMap{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout-configuration.yaml", configmap)).ToNot(HaveOccurred())
			Expect(k8sClient.Create(context.TODO(), configmap)).NotTo(HaveOccurred())
			defer func(k8sClient client.Client, ctx context.Context, obj client.Object) {
				_ = k8sClient.Delete(ctx, obj)
			}(k8sClient, context.TODO(), configmap)

			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			replica1 := intstr.FromInt(1)
			replica2 := intstr.FromInt(2)
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Matches: []v1alpha1.HttpRouteMatch{
							{
								Headers: []gatewayv1beta1.HTTPHeaderMatch{
									{
										Name:  "Cookie",
										Value: "demo1=value1;demo2=value2",
									},
									{
										Name:  "SourceIp",
										Value: "192.168.0.0/16;172.16.0.0/16",
									},
									{
										Name:  "headername",
										Value: "headervalue1;headervalue2",
									},
								},
							},
						},
					},
					Pause:    v1alpha1.RolloutPause{},
					Replicas: &replica1,
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(30),
					},
					Replicas: &replica2,
					Pause:    v1alpha1.RolloutPause{},
				},
			}
			rollout.Spec.Strategy.Canary.TrafficRoutings[0].Ingress.ClassType = "aliyun-alb"
			CreateObject(rollout)
			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			ingress.Annotations = map[string]string{}
			ingress.Spec.IngressClassName = utilpointer.String("alb")
			CreateObject(ingress)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			workload.Spec.Replicas = utilpointer.Int32(3)
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment image from(version1) -> to(version2)")
			time.Sleep(time.Second * 3)
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err := finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			canaryRevision := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevision))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			labIngressAnnotationDefaultPrefix := "alb.ingress.kubernetes.io"
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", labIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", labIngressAnnotationDefaultPrefix)]).Should(Equal(""))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/conditions.echoserver-canary", labIngressAnnotationDefaultPrefix)]).Should(Equal(`[{"cookieConfig":{"values":[{"key":"demo1","value":"value1"},{"key":"demo2","value":"value2"}]},"type":"Cookie"},{"sourceIpConfig":{"values":["192.168.0.0/16","172.16.0.0/16"]},"type":"SourceIp"},{"headerConfig":{"key":"headername","values":["headervalue1","headervalue2"]},"type":"Header"}]`))
			// canary deployment
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 1))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 1))
			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			// wait step 2 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", labIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/conditions.echoserver-canary", labIngressAnnotationDefaultPrefix)]).Should(Equal(""))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", labIngressAnnotationDefaultPrefix)]).Should(Equal("30"))
			// canary deployment
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 2))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 2))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			// wait rollout complete
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)
			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: A/B testing, aliyun-alb, header & cookies. cloneSet workload", func() {
			configmap := &v1.ConfigMap{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout-configuration.yaml", configmap)).ToNot(HaveOccurred())
			if err := k8sClient.Create(context.TODO(), configmap); err != nil {
				if !errors.IsAlreadyExists(err) {
					Expect(err).Should(BeNil())
				}
			}
			defer func(k8sClient client.Client, ctx context.Context, obj client.Object) {
				_ = k8sClient.Delete(ctx, obj)
			}(k8sClient, context.TODO(), configmap)

			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			replica1 := intstr.FromInt(1)
			replica2 := intstr.FromInt(3)
			rollout.Spec.ObjectRef.WorkloadRef = &v1alpha1.WorkloadRef{
				APIVersion: "apps.kruise.io/v1alpha1",
				Kind:       "CloneSet",
				Name:       "echoserver",
			}
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Matches: []v1alpha1.HttpRouteMatch{
							{
								Headers: []gatewayv1beta1.HTTPHeaderMatch{
									{
										Name:  "Cookie",
										Value: "demo1=value1;demo2=value2",
									},
									{
										Name:  "SourceIp",
										Value: "192.168.0.0/16;172.16.0.0/16",
									},
									{
										Name:  "headername",
										Value: "headervalue1;headervalue2",
									},
								},
							},
						},
					},
					Pause:    v1alpha1.RolloutPause{},
					Replicas: &replica1,
				},
				{
					Replicas: &replica2,
					Pause:    v1alpha1.RolloutPause{},
				},
			}
			rollout.Spec.Strategy.Canary.TrafficRoutings[0].Ingress.ClassType = "aliyun-alb"
			CreateObject(rollout)
			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// ingress
			ingress := &netv1.Ingress{}
			Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
			ingress.Annotations = map[string]string{}
			ingress.Spec.IngressClassName = utilpointer.String("alb")
			CreateObject(ingress)
			// workload
			workload := &appsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/rollout/cloneset.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitCloneSetAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(workload.Status.CurrentRevision[strings.LastIndex(workload.Status.CurrentRevision, "-")+1:]))
			stableRevision := rollout.Status.CanaryStatus.StableRevision
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateCloneSet(workload)
			By("Update cloneset EnvVar: NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 3)
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			canaryRevision := rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))

			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			labIngressAnnotationDefaultPrefix := "alb.ingress.kubernetes.io"
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/conditions.echoserver-canary", labIngressAnnotationDefaultPrefix)]).Should(Equal(`[{"cookieConfig":{"values":[{"key":"demo1","value":"value1"},{"key":"demo2","value":"value2"}]},"type":"Cookie"},{"sourceIpConfig":{"values":["192.168.0.0/16","172.16.0.0/16"]},"type":"SourceIp"},{"headerConfig":{"key":"headername","values":["headervalue1","headervalue2"]},"type":"Header"}]`))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			// wait step 2 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 2)

			// canary ingress and canary service should be deleted
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).To(HaveOccurred())
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).To(HaveOccurred())

			// check service update
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))

			// check cloneSet
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())

			// resume rollout to complete
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			WaitCloneSetAllPodsReady(workload)
			By("rollout completed, and check")

			// check service & ingress & cloneSet
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			// cloneSet
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

			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Percentage 20%, Succeeded with NodePort-type service", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			replicas := intstr.FromInt(2)
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
					Replicas: &replicas,
					Pause:    v1alpha1.RolloutPause{},
				},
			}
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			service.Spec.Type = "NodePort"
			service.Spec.Ports[0].NodePort = 30000
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
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			stableRevision := rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
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

			// check stable, canary service & ingress
			// canary deployment
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err := finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			canaryRevision := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			Expect(*cWorkload.Spec.Replicas).Should(BeNumerically("==", 2))
			Expect(cWorkload.Status.ReadyReplicas).Should(BeNumerically("==", 2))
			for _, env := range cWorkload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			// stable service
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService := &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(fmt.Sprintf("%d", *rollout.Spec.Strategy.Canary.Steps[0].Weight)))

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(canaryRevision))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")
			// check service & ingress & deployment
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			rss, err = finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))
			Expect(rss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))

			// check progressing succeed
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			//Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(canaryRevision))
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})
	})

	KruiseDescribe("Canary rollout with Gateway API", func() {
		It("V1->V2: Percentage 20%,40%,60%,80%,90%, and replicas=3", func() {
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/gateway/rollout-test.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(20),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(40),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(60),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(80),
					},
				},
				{
					TrafficRoutingStrategy: v1alpha1.TrafficRoutingStrategy{
						Weight: utilpointer.Int32(90),
					},
				},
			}
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// route
			route := &gatewayv1beta1.HTTPRoute{}
			Expect(ReadYamlToObject("./test_data/gateway/httproute-test.yaml", route)).ToNot(HaveOccurred())
			CreateObject(route)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			workload.Spec.Replicas = utilpointer.Int32(3)
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
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
			routeGet := &gatewayv1beta1.HTTPRoute{}
			Expect(GetObject(route.Name, routeGet)).NotTo(HaveOccurred())
			stable, canary := getHTTPRouteWeight(*routeGet)
			Expect(stable).Should(Equal(int32(80)))
			Expect(canary).Should(Equal(int32(20)))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(2)")
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))

			Expect(GetObject(route.Name, routeGet)).NotTo(HaveOccurred())
			stable, canary = getHTTPRouteWeight(*routeGet)
			Expect(stable).Should(Equal(int32(60)))
			Expect(canary).Should(Equal(int32(40)))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(3)")
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))
			Expect(GetObject(route.Name, routeGet)).NotTo(HaveOccurred())
			stable, canary = getHTTPRouteWeight(*routeGet)
			Expect(stable).Should(Equal(int32(40)))
			Expect(canary).Should(Equal(int32(60)))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(4)")
			WaitRolloutCanaryStepPaused(rollout.Name, 4)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 3))
			Expect(GetObject(route.Name, routeGet)).NotTo(HaveOccurred())
			stable, canary = getHTTPRouteWeight(*routeGet)
			Expect(stable).Should(Equal(int32(20)))
			Expect(canary).Should(Equal(int32(80)))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(5)")
			WaitRolloutCanaryStepPaused(rollout.Name, 5)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 3))
			Expect(GetObject(route.Name, routeGet)).NotTo(HaveOccurred())
			stable, canary = getHTTPRouteWeight(*routeGet)
			Expect(stable).Should(Equal(int32(10)))
			Expect(canary).Should(Equal(int32(90)))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")
			// check service & httproute & deployment
			// httproute
			Expect(GetObject(routeGet.Name, routeGet)).NotTo(HaveOccurred())
			stable, canary = getHTTPRouteWeight(*routeGet)
			Expect(stable).Should(Equal(int32(1)))
			Expect(canary).Should(Equal(int32(-1)))
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})
	})

	KruiseDescribe("Canary rollout with custom network provider", func() {
		It("V1->V2: Route traffic with header/queryParams/path matches and weight using rollout for VirtualService", func() {
			By("Creating Rollout...")
			rollout := &v1beta1.Rollout{}
			Expect(ReadYamlToObject("./test_data/customNetworkProvider/rollout_with_trafficrouting.yaml", rollout)).ToNot(HaveOccurred())
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// istio api
			vs := &unstructured.Unstructured{}
			Expect(ReadYamlToObject("./test_data/customNetworkProvider/virtualservice_without_destinationrule.yaml", vs)).ToNot(HaveOccurred())
			vs.SetAPIVersion("networking.istio.io/v1alpha3")
			vs.SetKind("VirtualService")
			CreateObject(vs)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			workload.Spec.Replicas = utilpointer.Int32(4)
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseHealthy))
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2), routing traffic with header agent:pc to new version pods")
			time.Sleep(time.Second * 2)

			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 1))
			// check virtualservice spec
			Expect(GetObject(vs.GetName(), vs)).NotTo(HaveOccurred())
			expectedSpec := `{"gateways":["nginx-gateway"],"hosts":["*"],"http":[{"match":[{"uri":{"prefix":"/pc"}}],"route":[{"destination":{"host":"echoserver-canary"}}]},{"match":[{"queryParams":{"user-agent":{"exact":"pc"}}}],"route":[{"destination":{"host":"echoserver-canary"}}]},{"match":[{"headers":{"user-agent":{"exact":"pc"}}}],"route":[{"destination":{"host":"echoserver-canary"}}]},{"route":[{"destination":{"host":"echoserver"}}]}]}`
			Expect(util.DumpJSON(vs.Object["spec"])).Should(Equal(expectedSpec))
			// check original spec annotation
			expectedAnno := `{"spec":{"gateways":["nginx-gateway"],"hosts":["*"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}}`
			Expect(vs.GetAnnotations()[OriginalSpecAnnotation]).Should(Equal(expectedAnno))

			// resume rollout canary
			ResumeRolloutCanaryV1beta1(rollout.Name)
			By("Resume rollout, and wait next step(2), routing 50% traffic to new version pods")
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))
			// check virtualservice spec
			Expect(GetObject(vs.GetName(), vs)).NotTo(HaveOccurred())
			expectedSpec = `{"gateways":["nginx-gateway"],"hosts":["*"],"http":[{"route":[{"destination":{"host":"echoserver"},"weight":50},{"destination":{"host":"echoserver-canary"},"weight":50}]}]}`
			Expect(util.DumpJSON(vs.Object["spec"])).Should(Equal(expectedSpec))
			// check original spec annotation
			Expect(vs.GetAnnotations()[OriginalSpecAnnotation]).Should(Equal(expectedAnno))

			// resume rollout
			ResumeRolloutCanaryV1beta1(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			By("rollout completed, and check")
			// check service & virtualservice & deployment
			// virtualservice
			Expect(GetObject(vs.GetName(), vs)).NotTo(HaveOccurred())
			expectedSpec = `{"gateways":["nginx-gateway"],"hosts":["*"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`
			Expect(util.DumpJSON(vs.Object["spec"])).Should(Equal(expectedSpec))
			Expect(vs.GetAnnotations()[OriginalSpecAnnotation]).Should(Equal(""))
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutConditionV1beta1(rollout.Status, v1beta1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1beta1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutConditionV1beta1(rollout.Status, v1beta1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: Route traffic with header matches and weight for VirtualService and DestinationRule", func() {
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/customNetworkProvider/rollout_without_trafficrouting.yaml", rollout)).ToNot(HaveOccurred())
			CreateObject(rollout)

			By("Creating TrafficRouting...")
			traffic := &v1alpha1.TrafficRouting{}
			Expect(ReadYamlToObject("./test_data/customNetworkProvider/trafficrouting.yaml", traffic)).ToNot(HaveOccurred())
			CreateObject(traffic)

			By("Creating workload and waiting for all pods ready...")
			// service
			service := &v1.Service{}
			Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
			CreateObject(service)
			// istio api
			vs := &unstructured.Unstructured{}
			Expect(ReadYamlToObject("./test_data/customNetworkProvider/virtualservice_without_destinationrule.yaml", vs)).ToNot(HaveOccurred())
			vs.SetAPIVersion("networking.istio.io/v1alpha3")
			vs.SetKind("VirtualService")
			CreateObject(vs)
			dr := &unstructured.Unstructured{}
			Expect(ReadYamlToObject("./test_data/customNetworkProvider/destinationrule.yaml", dr)).ToNot(HaveOccurred())
			dr.SetAPIVersion("networking.istio.io/v1alpha3")
			dr.SetKind("DestinationRule")
			CreateObject(dr)
			// workload
			workload := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment.yaml", workload)).ToNot(HaveOccurred())
			workload.Spec.Replicas = utilpointer.Int32(4)
			CreateObject(workload)
			WaitDeploymentAllPodsReady(workload)

			// check rollout and trafficrouting status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			Expect(GetObject(traffic.Name, traffic)).NotTo(HaveOccurred())
			Expect(traffic.Status.Phase).Should(Equal(v1alpha1.TrafficRoutingPhaseHealthy))
			By("check rollout and trafficrouting status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment env NODE_NAME from(version1) -> to(version2), routing traffic with header agent:pc to new version pods")
			time.Sleep(time.Second * 2)

			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Spec.Paused).Should(BeTrue())
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// check rollout and trafficrouting status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 1))
			Expect(GetObject(traffic.Name, traffic)).NotTo(HaveOccurred())
			Expect(traffic.Status.Phase).Should(Equal(v1alpha1.TrafficRoutingPhaseProgressing))
			// check virtualservice and destinationrule spec
			Expect(GetObject(vs.GetName(), vs)).NotTo(HaveOccurred())
			expectedVSSpec := `{"gateways":["nginx-gateway"],"hosts":["*"],"http":[{"match":[{"headers":{"user-agent":{"exact":"pc"}}}],"route":[{"destination":{"host":"echoserver","subset":"canary"}}]},{"route":[{"destination":{"host":"echoserver"}}]}]}`
			Expect(util.DumpJSON(vs.Object["spec"])).Should(Equal(expectedVSSpec))
			Expect(GetObject(dr.GetName(), dr)).NotTo(HaveOccurred())
			expectedDRSpec := `{"host":"svc-demo","subsets":[{"labels":{"version":"base"},"name":"echoserver"},{"labels":{"istio.service.tag":"gray"},"name":"canary"}],"trafficPolicy":{"loadBalancer":{"simple":"ROUND_ROBIN"}}}`
			Expect(util.DumpJSON(dr.Object["spec"])).Should(Equal(expectedDRSpec))
			// check original spec annotation
			expectedVSAnno := `{"spec":{"gateways":["nginx-gateway"],"hosts":["*"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}}`
			Expect(vs.GetAnnotations()[OriginalSpecAnnotation]).Should(Equal(expectedVSAnno))
			expectedDRAnno := `{"spec":{"host":"svc-demo","subsets":[{"labels":{"version":"base"},"name":"echoserver"}],"trafficPolicy":{"loadBalancer":{"simple":"ROUND_ROBIN"}}}}`
			Expect(dr.GetAnnotations()[OriginalSpecAnnotation]).Should(Equal(expectedDRAnno))

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			Expect(GetObject(traffic.Name, traffic)).NotTo(HaveOccurred())
			Expect(traffic.Status.Phase).Should(Equal(v1alpha1.TrafficRoutingPhaseHealthy))
			By("rollout completed, and check")
			// check service & virtualservice & destinationrule & deployment
			// virtualservice and destinationrule
			Expect(GetObject(vs.GetName(), vs)).NotTo(HaveOccurred())
			expectedVSSpec = `{"gateways":["nginx-gateway"],"hosts":["*"],"http":[{"route":[{"destination":{"host":"echoserver"}}]}]}`
			Expect(util.DumpJSON(vs.Object["spec"])).Should(Equal(expectedVSSpec))
			Expect(vs.GetAnnotations()[OriginalSpecAnnotation]).Should(Equal(""))

			Expect(GetObject(dr.GetName(), dr)).NotTo(HaveOccurred())
			expectedDRSpec = `{"host":"svc-demo","subsets":[{"labels":{"version":"base"},"name":"echoserver"}],"trafficPolicy":{"loadBalancer":{"simple":"ROUND_ROBIN"}}}`
			Expect(util.DumpJSON(dr.Object["spec"])).Should(Equal(expectedDRSpec))
			Expect(dr.GetAnnotations()[OriginalSpecAnnotation]).Should(Equal(""))
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
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
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})
	})

	KruiseDescribe("DaemonSet canary rollout", func() {
		It("DaemonSet V1->V2: 1,100% Succeeded", func() {
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_daemonset_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					Replicas: &intstr.IntOrString{IntVal: *utilpointer.Int32(1), Type: intstr.Int},
					Pause:    v1alpha1.RolloutPause{},
				},
				{
					Replicas: &intstr.IntOrString{IntVal: *utilpointer.Int32(100), Type: intstr.Int},
					Pause:    v1alpha1.RolloutPause{},
				},
			}
			rollout.Spec.ObjectRef.WorkloadRef = &v1alpha1.WorkloadRef{
				APIVersion: "apps.kruise.io/v1alpha1",
				Kind:       "DaemonSet",
				Name:       "fluentd-elasticsearch",
			}
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// workload
			workload := &appsv1alpha1.DaemonSet{}
			Expect(ReadYamlToObject("./test_data/rollout/daemonset.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDaemonSetAllPodsReady(workload)

			// check rollout status
			By("check rollout status & paused success")
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))

			// v1 -> v2, start rollout action
			By("Update daemonset env NODE_NAME from(version1) -> to(version2)")
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDaemonSet(workload)
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedNumberScheduled).Should(BeNumerically("==", 1))
			Expect(workload.Status.NumberReady).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			Expect(*workload.Spec.UpdateStrategy.RollingUpdate.Paused).Should(BeFalse())
			By("check daemonset status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(2)")
			WaitRolloutCanaryStepPaused(rollout.Name, 2)

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			WaitDaemonSetAllPodsReady(workload)
			By("rollout completed, and check")

			// check daemonset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedNumberScheduled).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			Expect(workload.Status.NumberReady).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
			time.Sleep(time.Second * 3)

			// check progressing succeed
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)

		})

		It("V1->V2: Percentage, 1, 2 and continuous release v3", func() {
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_daemonset_interrupt.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					Replicas: &intstr.IntOrString{IntVal: *utilpointer.Int32(1), Type: intstr.Int},
					Pause:    v1alpha1.RolloutPause{},
				},
				{
					Replicas: &intstr.IntOrString{IntVal: *utilpointer.Int32(2), Type: intstr.Int},
					Pause:    v1alpha1.RolloutPause{},
				},
				{
					Replicas: &intstr.IntOrString{IntVal: *utilpointer.Int32(100), Type: intstr.Int},
					Pause:    v1alpha1.RolloutPause{},
				},
			}
			rollout.Spec.ObjectRef.WorkloadRef = &v1alpha1.WorkloadRef{
				APIVersion: "apps.kruise.io/v1alpha1",
				Kind:       "DaemonSet",
				Name:       "fluentd-elasticsearch",
			}
			CreateObject(rollout)

			By("Creating workload and waiting for all pods ready...")
			// workload
			workload := &appsv1alpha1.DaemonSet{}
			Expect(ReadYamlToObject("./test_data/rollout/daemonset.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDaemonSetAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDaemonSet(workload)
			By("Update daemonSet env NODE_NAME from(version1) -> to(version2)")
			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedNumberScheduled).Should(BeNumerically("==", 1))
			Expect(workload.Status.NumberReady).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			Expect(*workload.Spec.UpdateStrategy.RollingUpdate.Paused).Should(BeFalse())
			By("check daemonSet status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(2)")
			WaitRolloutCanaryStepPaused(rollout.Name, 2)

			// v1 -> v2 -> v3, continuous release
			By("Update daemonSet env NODE_NAME from(version2) -> to(version3)")
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version3"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDaemonSet(workload)

			// wait step 1 complete
			WaitRolloutCanaryStepPaused(rollout.Name, 1)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedNumberScheduled).Should(BeNumerically("==", 1))
			Expect(workload.Status.NumberReady).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			Expect(*workload.Spec.UpdateStrategy.RollingUpdate.Paused).Should(BeFalse())
			By("check daemonSet status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(2)")
			WaitRolloutCanaryStepPaused(rollout.Name, 2)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedNumberScheduled).Should(BeNumerically("==", 2))
			Expect(workload.Status.NumberReady).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			Expect(*workload.Spec.UpdateStrategy.RollingUpdate.Paused).Should(BeFalse())
			By("check daemonSet status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(3)")
			WaitRolloutCanaryStepPaused(rollout.Name, 3)

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, v1alpha1.RolloutPhaseHealthy)
			WaitDaemonSetAllPodsReady(workload)
			By("rollout completed, and check")

			// check daemonset
			// daemonset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedNumberScheduled).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			Expect(workload.Status.NumberReady).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version3"))
				}
			}
			time.Sleep(time.Second * 3)

			// check progressing succeed
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1alpha1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1alpha1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		It("V1->V2: 1,100%, but delete rollout crd", func() {
			// finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_daemonset_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
				{
					Replicas: &intstr.IntOrString{IntVal: *utilpointer.Int32(1), Type: intstr.Int},
					Pause:    v1alpha1.RolloutPause{},
				},
				{
					Replicas: &intstr.IntOrString{IntVal: *utilpointer.Int32(100), Type: intstr.Int},
					Pause:    v1alpha1.RolloutPause{},
				},
			}
			rollout.Spec.ObjectRef.WorkloadRef = &v1alpha1.WorkloadRef{
				APIVersion: "apps.kruise.io/v1alpha1",
				Kind:       "DaemonSet",
				Name:       "fluentd-elasticsearch",
			}
			CreateObject(rollout)
			By("Creating workload and waiting for all pods ready...")

			// workload
			workload := &appsv1alpha1.DaemonSet{}
			Expect(ReadYamlToObject("./test_data/rollout/daemonset.yaml", workload)).ToNot(HaveOccurred())
			CreateObject(workload)
			WaitDaemonSetAllPodsReady(workload)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			By("Update DaemonSet env NODE_NAME from(version1) -> to(version2)")
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDaemonSet(workload)
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			time.Sleep(time.Second * 3)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedNumberScheduled).Should(BeNumerically("==", 1))
			Expect(workload.Status.NumberReady).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			Expect(*workload.Spec.UpdateStrategy.RollingUpdate.Paused).Should(BeFalse())
			By("check DaemonSet status & paused success")
			// delete rollout
			By("Delete rollout crd, and wait DaemonSet ready")
			Expect(k8sClient.DeleteAllOf(context.TODO(), &v1alpha1.Rollout{}, client.InNamespace(namespace), client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
			WaitRolloutNotFound(rollout.Name)
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			workload.Spec.UpdateStrategy.RollingUpdate.Partition = utilpointer.Int32(0)
			UpdateDaemonSet(workload)
			WaitDaemonSetAllPodsReady(workload)

			// check daemonset
			// daemonset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(*workload.Spec.UpdateStrategy.RollingUpdate.Paused).Should(BeFalse())
			Expect(workload.Status.UpdatedNumberScheduled).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			Expect(workload.Status.NumberReady).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			Expect(workload.Status.CurrentNumberScheduled).Should(BeNumerically("==", workload.Status.DesiredNumberScheduled))
			for _, env := range workload.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "NODE_NAME" {
					Expect(env.Value).Should(Equal("version2"))
				}
			}
		})

	})

	KruiseDescribe("Disabled rollout tests", func() {
		rollout := &v1alpha1.Rollout{}
		Expect(ReadYamlToObject("./test_data/rollout/rollout_disabled.yaml", rollout)).ToNot(HaveOccurred())
		It("Rollout status tests", func() {
			By("Create an enabled rollout")
			rollout1 := rollout.DeepCopy()
			rollout1.Name = "rollout-demo1"
			rollout1.Spec.Disabled = false
			CreateObject(rollout1)
			time.Sleep(1 * time.Second)

			By("Create another enabled rollout")
			rollout2 := rollout.DeepCopy()
			rollout2.Name = "rollout-demo2"
			rollout2.Spec.Disabled = false
			rollout2.SetNamespace(namespace)
			Expect(k8sClient.Create(context.TODO(), rollout2)).Should(HaveOccurred())

			By("Creating a disabled rollout")
			rollout3 := rollout.DeepCopy()
			rollout3.Name = "rollout-demo3"
			rollout3.Spec.Disabled = true
			rollout2.SetNamespace(namespace)
			Expect(k8sClient.Create(context.TODO(), rollout2)).Should(HaveOccurred())
			// wait for reconciling
			time.Sleep(3 * time.Second)
			Expect(GetObject(rollout1.Name, rollout1)).NotTo(HaveOccurred())
			Expect(rollout1.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseInitial))

			By("Create workload")
			deploy := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/rollout/deployment_disabled.yaml", deploy)).ToNot(HaveOccurred())
			CreateObject(deploy)
			WaitDeploymentAllPodsReady(deploy)
			Expect(GetObject(rollout1.Name, rollout1)).NotTo(HaveOccurred())
			Expect(rollout1.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))

			By("Updating deployment version-1 to version-2")
			Expect(GetObject(deploy.Name, deploy)).NotTo(HaveOccurred())
			newEnvs := mergeEnvVar(deploy.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "VERSION", Value: "version-2"})
			deploy.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(deploy)
			WaitRolloutCanaryStepPaused(rollout1.Name, 1)
			Expect(GetObject(rollout1.Name, rollout1)).NotTo(HaveOccurred())
			Expect(rollout1.Status.CanaryStatus.CanaryReplicas).Should(BeNumerically("==", 2))
			Expect(rollout1.Status.CanaryStatus.CanaryReadyReplicas).Should(BeNumerically("==", 2))
			Expect(GetObject(deploy.Name, deploy)).NotTo(HaveOccurred())
			Expect(deploy.Spec.Paused).Should(BeTrue())

			By("Disable a rolling rollout")
			rollout1.Spec.Disabled = true
			UpdateRollout(rollout1)
			time.Sleep(5 * time.Second)

			By("Rolling should be resumed")
			Expect(GetObject(deploy.Name, deploy)).NotTo(HaveOccurred())
			Expect(deploy.Spec.Paused).Should(BeFalse())

			By("Batchrelease should be deleted")
			key := types.NamespacedName{Namespace: namespace, Name: rollout1.Name}
			Expect(k8sClient.Get(context.TODO(), key, &v1alpha1.BatchRelease{})).Should(HaveOccurred())
			Expect(GetObject(rollout1.Name, rollout1)).NotTo(HaveOccurred())
			Expect(rollout1.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseDisabled))

			By("Updating deployment version-2 to version-3")
			Expect(GetObject(deploy.Name, deploy)).NotTo(HaveOccurred())
			newEnvs = mergeEnvVar(deploy.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "VERSION", Value: "version-3"})
			deploy.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(deploy)
			time.Sleep(3 * time.Second)
			Expect(GetObject(deploy.Name, deploy)).NotTo(HaveOccurred())
			Expect(deploy.Spec.Paused).Should(BeFalse())

			By("Enable a disabled rollout")
			rollout1.Spec.Disabled = false
			UpdateRollout(rollout1)
			time.Sleep(3 * time.Second)
			Expect(GetObject(rollout1.Name, rollout1)).NotTo(HaveOccurred())
			Expect(rollout1.Status.Phase).Should(Equal(v1alpha1.RolloutPhaseHealthy))
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

func mergeMap(dst, patch map[string]string) map[string]string {
	for key, value := range patch {
		dst[key] = value
	}
	return dst
}

func getHTTPRouteWeight(route gatewayv1beta1.HTTPRoute) (int32, int32) {
	var stable, canary int32
	for i := range route.Spec.Rules {
		rules := route.Spec.Rules[i]
		for j := range rules.BackendRefs {
			if strings.HasSuffix(string(rules.BackendRefs[j].Name), "-canary") {
				canary = *rules.BackendRefs[j].Weight
			} else {
				stable = *rules.BackendRefs[j].Weight
			}
		}
	}
	if canary == 0 {
		canary = -1
	}
	return stable, canary
}
