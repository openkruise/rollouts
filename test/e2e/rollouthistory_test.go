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
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = SIGDescribe("RolloutHistory", func() {
	var namespace string

	DumpAllResources := func() {
		rollout := &rolloutsv1alpha1.RolloutList{}
		k8sClient.List(context.TODO(), rollout, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(rollout))
		batch := &rolloutsv1alpha1.BatchReleaseList{}
		k8sClient.List(context.TODO(), batch, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(batch))
		deploy := &apps.DeploymentList{}
		k8sClient.List(context.TODO(), deploy, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(deploy))
		rs := &apps.ReplicaSetList{}
		k8sClient.List(context.TODO(), rs, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(rs))
		cloneSet := &appsv1alpha1.CloneSetList{}
		k8sClient.List(context.TODO(), cloneSet, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(cloneSet))
		sts := &apps.StatefulSetList{}
		k8sClient.List(context.TODO(), sts, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(sts))
	}

	CreateObject := func(object client.Object, options ...client.CreateOption) {
		object.SetNamespace(namespace)
		Expect(k8sClient.Create(context.TODO(), object)).NotTo(HaveOccurred())
	}

	GetObject := func(name string, object client.Object) error {
		key := types.NamespacedName{Namespace: namespace, Name: name}
		return k8sClient.Get(context.TODO(), key, object)
	}
	/*
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
				return k8sClient.Update(context.TODO(), clone)
			})).NotTo(HaveOccurred())

			return clone
		}
	*/
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

	UpdateAdvancedStatefulSet := func(object *appsv1beta1.StatefulSet) *appsv1beta1.StatefulSet {
		var clone *appsv1beta1.StatefulSet
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone = &appsv1beta1.StatefulSet{}
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

	UpdateNativeStatefulSet := func(object *apps.StatefulSet) *apps.StatefulSet {
		var clone *apps.StatefulSet
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone = &apps.StatefulSet{}
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

	ResumeRolloutCanary := func(name string) {
		Eventually(func() bool {
			clone := &rolloutsv1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStatus.CurrentStepState != rolloutsv1alpha1.CanaryStepStatePaused {
				fmt.Println("resume rollout success, and CurrentStepState", util.DumpJSON(clone.Status))
				return true
			}

			body := fmt.Sprintf(`{"status":{"canaryStatus":{"currentStepState":"%s"}}}`, rolloutsv1alpha1.CanaryStepStateReady)
			Expect(k8sClient.Status().Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))).NotTo(HaveOccurred())
			return false
		}, 10*time.Second, time.Second).Should(BeTrue())
	}
	/*
		WaitDeploymentAllPodsReady := func(deployment *apps.Deployment) {
			Eventually(func() bool {
				clone := &apps.Deployment{}
				Expect(GetObject(deployment.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.ObservedGeneration == clone.Generation && *clone.Spec.Replicas == clone.Status.UpdatedReplicas &&
					*clone.Spec.Replicas == clone.Status.ReadyReplicas && *clone.Spec.Replicas == clone.Status.Replicas
			}, 5*time.Minute, time.Second).Should(BeTrue())
		}
	*/
	WaitCloneSetAllPodsReady := func(cloneset *appsv1alpha1.CloneSet) {
		Eventually(func() bool {
			clone := &appsv1alpha1.CloneSet{}
			Expect(GetObject(cloneset.Name, clone)).NotTo(HaveOccurred())
			return clone.Status.ObservedGeneration == clone.Generation && *clone.Spec.Replicas == clone.Status.UpdatedReplicas &&
				*clone.Spec.Replicas == clone.Status.ReadyReplicas && *clone.Spec.Replicas == clone.Status.Replicas
		}, 5*time.Minute, time.Second).Should(BeTrue())
	}

	WaitNativeStatefulSetPodsReady := func(statefulset *apps.StatefulSet) {
		Eventually(func() bool {
			set := &apps.StatefulSet{}
			Expect(GetObject(statefulset.Name, set)).NotTo(HaveOccurred())
			return set.Status.ObservedGeneration == set.Generation && *set.Spec.Replicas == set.Status.UpdatedReplicas &&
				*set.Spec.Replicas == set.Status.ReadyReplicas && *set.Spec.Replicas == set.Status.Replicas
		}, 20*time.Minute, 3*time.Second).Should(BeTrue())
	}

	WaitAdvancedStatefulSetPodsReady := func(statefulset *appsv1beta1.StatefulSet) {
		Eventually(func() bool {
			set := &appsv1beta1.StatefulSet{}
			Expect(GetObject(statefulset.Name, set)).NotTo(HaveOccurred())
			return set.Status.ObservedGeneration == set.Generation && *set.Spec.Replicas == set.Status.UpdatedReplicas &&
				*set.Spec.Replicas == set.Status.ReadyReplicas && *set.Spec.Replicas == set.Status.Replicas
		}, 20*time.Minute, 3*time.Second).Should(BeTrue())
	}
	/*
		WaitDeploymentReplicas := func(deployment *apps.Deployment) {
			Eventually(func() bool {
				clone := &apps.Deployment{}
				Expect(GetObject(deployment.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.ObservedGeneration == clone.Generation &&
					*clone.Spec.Replicas == clone.Status.ReadyReplicas && *clone.Spec.Replicas == clone.Status.Replicas
			}, 10*time.Minute, time.Second).Should(BeTrue())
		}
	*/
	/*
		WaitRolloutCanaryStepPaused := func(name string, stepIndex int32) {
			start := time.Now()
			Eventually(func() bool {
				if start.Add(time.Minute * 5).Before(time.Now()) {
					DumpAllResources()
					Expect(true).Should(BeFalse())
				}
				clone := &rolloutsv1alpha1.Rollout{}
				Expect(GetObject(name, clone)).NotTo(HaveOccurred())
				if clone.Status.CanaryStatus == nil {
					return false
				}
				klog.Infof("current step:%v target step:%v current step state %v", clone.Status.CanaryStatus.CurrentStepIndex, stepIndex, clone.Status.CanaryStatus.CurrentStepState)
				return clone.Status.CanaryStatus.CurrentStepIndex == stepIndex && clone.Status.CanaryStatus.CurrentStepState == rolloutsv1alpha1.CanaryStepStatePaused
			}, 20*time.Minute, time.Second).Should(BeTrue())
		}
	*/
	WaitRolloutStatusPhase := func(name string, phase rolloutsv1alpha1.RolloutPhase) {
		Eventually(func() bool {
			clone := &rolloutsv1alpha1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			return clone.Status.Phase == phase
		}, 20*time.Minute, time.Second).Should(BeTrue())
	}

	ListPods := func(namespace string, selector labels.Selector) ([]*v1.Pod, error) {
		appList := &v1.PodList{}
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

	CheckPodsBatchLabel := func(namespace string, labelSelector *metav1.LabelSelector, rolloutID, batchID string, expected int) {
		selector, _ := metav1.LabelSelectorAsSelector(labelSelector)

		lableSelectorString := fmt.Sprintf("%v=%v,%v=%v,%v", util.RolloutBatchIDLabel, batchID, util.RolloutIDLabel, rolloutID, selector.String())
		extraSelector, err := labels.Parse(lableSelectorString)
		Expect(err).NotTo(HaveOccurred())

		pods, err := ListPods(namespace, extraSelector)
		Expect(err).NotTo(HaveOccurred())

		Expect(len(pods)).Should(BeNumerically("==", expected))
	}

	CheckRolloutHistoryPodsBatchLabel := func(pods *rolloutsv1alpha1.CanaryStepPods, rolloutID, batchID string) bool {
		for _, podInfo := range pods.Pods {
			pod := &v1.Pod{}
			Expect(GetObject(podInfo.Name, pod))
			if pod.Labels[util.RolloutIDLabel] != rolloutID &&
				pod.Labels[util.RolloutBatchIDLabel] != batchID {
				return false
			}
		}

		return true
	}

	GetRolloutHistory := func(rollout *rolloutsv1alpha1.Rollout) (RH *rolloutsv1alpha1.RolloutHistory, err error) {
		Eventually(func() bool {
			rhList := &rolloutsv1alpha1.RolloutHistoryList{}
			Expect(k8sClient.List(context.TODO(), rhList, &client.ListOptions{}, client.InNamespace(rollout.Namespace))).NotTo(HaveOccurred())

			result := rhList.Items

			for i := range result {
				RH = &result[i]
				if rollout.Spec.RolloutID == RH.Spec.RolloutWrapper.Rollout.Spec.RolloutID &&
					rollout.Name == RH.Spec.RolloutWrapper.Name {
					err = nil
					return true
				}
			}

			err = errors.New("rollout history not found")
			return false
		}, 30*time.Second, time.Second).Should(BeTrue())
		return
	}

	ListRolloutHistories := func(namespace string) ([]*rolloutsv1alpha1.RolloutHistory, error) {
		rhList := &rolloutsv1alpha1.RolloutHistoryList{}
		err := k8sClient.List(context.TODO(), rhList, &client.ListOptions{Namespace: namespace})
		if err != nil {
			return nil, err
		}
		rhs := make([]*rolloutsv1alpha1.RolloutHistory, 0)
		for i := range rhList.Items {
			rh := &rhList.Items[i]
			if rh.DeletionTimestamp.IsZero() {
				rhs = append(rhs, rh)
			}
		}
		return rhs, nil
	}

	WaitRolloutHistoryPhase := func(name string, status string) {
		start := time.Now()
		Eventually(func() bool {
			if start.Add(time.Minute * 5).Before(time.Now()) {
				DumpAllResources()
				Expect(true).Should(BeFalse())
			}
			clone := &rolloutsv1alpha1.RolloutHistory{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStepIndex == nil {
				return false
			}
			klog.Infof("rolloutID:%v current rhphase:%v current step: %v current status:%v target step:%v ", clone.Spec.RolloutWrapper.Rollout.Spec.RolloutID, clone.Status.CanaryStepState, clone.Status.CanaryStepIndex, clone.Status.Phase, status)
			return clone.Status.Phase == status
		}, 20*time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutHistoryStepPaused := func(name string, stepIndex int32) {
		start := time.Now()
		Eventually(func() bool {
			if start.Add(time.Minute * 5).Before(time.Now()) {
				DumpAllResources()
				Expect(true).Should(BeFalse())
			}
			clone := &rolloutsv1alpha1.RolloutHistory{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStepIndex == nil {
				return false
			}
			klog.Infof("current step:%v target step:%v current step state %v", *clone.Status.CanaryStepIndex, stepIndex, clone.Status.CanaryStepState)
			return *clone.Status.CanaryStepIndex == stepIndex && clone.Status.CanaryStepState == rolloutsv1alpha1.CanaryStateUpdated
		}, 20*time.Minute, time.Second).Should(BeTrue())
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
		k8sClient.DeleteAllOf(context.TODO(), &rolloutsv1alpha1.RolloutHistory{}, client.InNamespace(namespace))
		Expect(k8sClient.Delete(context.TODO(), &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
		time.Sleep(time.Second * 3)
	})

	KruiseDescribe("CloneSet canary rollout with RolloutHistory", func() {
		It("V1->V2: Percentage, 20%,60% Succeeded", func() {
			By("Creating Rollout...")
			rollout := &rolloutsv1alpha1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
				{
					Weight: utilpointer.Int32(20),
					Pause:  rolloutsv1alpha1.RolloutPause{Duration: utilpointer.Int32(10)},
				},
				{
					Weight: utilpointer.Int32(60),
					Pause:  rolloutsv1alpha1.RolloutPause{Duration: utilpointer.Int32(10)},
				},
			}
			rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
				APIVersion: "apps.kruise.io/v1alpha1",
				Kind:       "CloneSet",
				Name:       "echoserver",
			}
			rollout.Spec.RolloutID = "1"
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

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			rollout.Spec.RolloutID = "2"
			UpdateRollout(rollout)
			UpdateCloneSet(workload)
			By("Update rollouthistory rolloutID from(1) -> to(2), update cloneSet env NODE_NAME from(version1) -> to(version2)")

			// wait step 1 complete
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			rollouthistory, err := GetRolloutHistory(rollout)
			Expect(err).To(BeNil())
			WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)

			// check out the num of rollouthistory
			rhs, err := ListRolloutHistories(namespace)
			Expect(err).To(BeNil())
			Expect(len(rhs)).Should(BeNumerically("==", 1))

			// check rollout status & paused
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

			// check rollouthistory spec
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
			Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
			Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
			Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

			// check rollouthistory status & paused
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 1))
			Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
			Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
			By("check rollouthistory status & update success")

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("resume rollout, and wait next step(2)")
			WaitRolloutHistoryStepPaused(rollouthistory.Name, 2)

			// check rollout status & paused
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))

			// check rollouthistory status & paused
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 2))
			Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 2))
			Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
			Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 2)
			Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 2))
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
			By("check rollouthistory status & update success")

			// cloneset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")

			// resume rollout
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
			WaitCloneSetAllPodsReady(workload)
			By("rollout completed, and check")

			WaitRolloutHistoryPhase(rollouthistory.Name, rolloutsv1alpha1.PhaseCompleted)

			// check rollout status & paused
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateCompleted))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))

			// check rollouthistory status & paused
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 3))
			Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 3))
			Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateCompleted))
			Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 2))
			Expect(len(rollouthistory.Status.CanaryStepPods[2].Pods)).Should(BeNumerically("==", 0))
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 2)
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
			/// the last rollout
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "3", 0)
			/// Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.StepStatus[2].PodList, rollout.Spec.RolloutID, "3")).Should(BeTrue())
			fmt.Println(rollouthistory.Status.CanaryStepPods) /// dialog
			fmt.Println()
			fmt.Println()

			// check cloneset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			selector, _ := metav1.LabelSelectorAsSelector(workload.Spec.Selector)
			fmt.Println(ListPods(namespace, selector))
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			By("check rollouthistory status & update success")
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
			stableRevision := rollout.Status.StableRevision
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Image = "echoserver:failed"
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			rollout.Spec.RolloutID = "1"
			UpdateRollout(rollout)
			UpdateCloneSet(workload)
			By("Update cloneSet env NODE_NAME from(version1) -> to(version2), rolloutID from('') -> to(1)")
			// wait step 1 complete
			time.Sleep(time.Second * 20)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateUpgrade))

			// check rollouthistory
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			rollouthistory, err := GetRolloutHistory(rollout)
			Expect(err).To(BeNil())

			// check rollouthistory num
			rhs, err := ListRolloutHistories(namespace)
			Expect(err).To(BeNil())
			Expect(len(rhs)).Should(BeNumerically("==", 1))

			// check rollouthistory spec
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
			Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
			Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
			Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

			// check rollouthistory status
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 0))
			Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStatePending))
			Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			time.Sleep(time.Second * 15)

			// rollback -> v1
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version1"})
			workload.Spec.Template.Spec.Containers[0].Image = "cilium/echoserver:1.10.2"
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			rollout.Spec.RolloutID = "2"
			UpdateRollout(rollout)
			UpdateCloneSet(workload)
			By("Rollback deployment env NODE_NAME from(version2) -> to(version1), rolloutID from(1) -> to(2)")
			time.Sleep(time.Second * 5)

			// check rollouthistory
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			rollouthistory, err = GetRolloutHistory(rollout)
			Expect(err).To(BeNil())
			WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)
			ResumeRolloutCanary(rollout.Name)

			WaitRolloutHistoryPhase(rollouthistory.Name, rolloutsv1alpha1.PhaseCompleted)

			// check rollout status & paused
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateCompleted))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 5))

			// check rollouthistory num
			rhs, err = ListRolloutHistories(namespace)
			Expect(err).To(BeNil())
			Expect(len(rhs)).Should(BeNumerically("==", 2))

			// check rollouthistory spec
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
			Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
			Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
			Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

			// check rollouthistory status
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 5))
			Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 5))
			Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateCompleted))
			Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods[2].Pods)).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods[3].Pods)).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods[4].Pods)).Should(BeNumerically("==", 1))
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 1)
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "3", 1)
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "4", 1)
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "5", 1)
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[2], rollout.Spec.RolloutID, "3")).Should(BeTrue())
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[3], rollout.Spec.RolloutID, "4")).Should(BeTrue())
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[4], rollout.Spec.RolloutID, "5")).Should(BeTrue())

			By("rollout completed, and check")
			// check workload
			// cloneset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Spec.UpdateStrategy.Partition.IntVal).Should(BeNumerically("==", 0))

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
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			rollout.Spec.RolloutID = "1"
			UpdateRollout(rollout)
			UpdateCloneSet(workload)
			By("Update cloneSet env NODE_NAME from(version1) -> to(version2)")
			// wait step 1 complete
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			rollouthistory, err := GetRolloutHistory(rollout)
			Expect(err).To(BeNil())
			WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)

			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

			// check rollouthistory num
			rhs, err := ListRolloutHistories(namespace)
			Expect(err).To(BeNil())
			Expect(len(rhs)).Should(BeNumerically("==", 1))

			// check rollouthistory spec
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
			Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
			Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
			Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

			// check rollouthistory status
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 1))
			Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
			Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
			Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
			By("check rollouthistory[rollout-id:1 batch-id:1] status & update success")

			// resume rollouthistory canary
			ResumeRolloutCanary(rollout.Name)
			time.Sleep(time.Second * 15)

			// v1 -> v2 -> v3, continuous release
			newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version3"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			rollout.Spec.RolloutID = "2"
			UpdateRollout(rollout)
			UpdateCloneSet(workload)
			By("Update cloneSet env NODE_NAME from(version2) -> to(version3), rollout from(1) -> to(2)")
			time.Sleep(time.Second * 10)

			// wait step 1 complete
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			rollouthistory, err = GetRolloutHistory(rollout)
			Expect(err).To(BeNil())
			WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)

			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

			// check rollouthistory num
			rhs, err = ListRolloutHistories(namespace)
			Expect(err).To(BeNil())
			Expect(len(rhs)).Should(BeNumerically("==", 2))

			// check rollouthistory spec
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
			Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
			Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
			Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
			Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
			Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

			// check rollouthistory status
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 1))
			Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
			Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
			Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
			By("check rollouthistory[rollout-id:2 batch-id:1] status & update success")

			// resume rollout canary
			ResumeRolloutCanary(rollout.Name)
			By("check rollout canary status success, resume rollout, and wait rollout canary complete")
			WaitRolloutHistoryPhase(rollouthistory.Name, rolloutsv1alpha1.PhaseCompleted)
			By("rollout completed, and check")

			// cloneset
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 5))

			// check rollouthistory status
			Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
			Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 5))
			Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 5))
			Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateCompleted))
			Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods[2].Pods)).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods[3].Pods)).Should(BeNumerically("==", 1))
			Expect(len(rollouthistory.Status.CanaryStepPods[4].Pods)).Should(BeNumerically("==", 1))
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 1)
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "3", 1)
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "4", 1)
			CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "5", 1)
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[2], rollout.Spec.RolloutID, "3")).Should(BeTrue())
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[3], rollout.Spec.RolloutID, "4")).Should(BeTrue())
			Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[4], rollout.Spec.RolloutID, "5")).Should(BeTrue())

			// check progressing succeed
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
		})
	})

	KruiseDescribe("StatefulSet canary rollout with RolloutHistory", func() {
		KruiseDescribe("Native StatefulSet canary rollout with RolloutHistory", func() {
			It("V1->V2: Percentage, 20%,60% Succeeded", func() {
				By("Creating Rollout...")
				rollout := &rolloutsv1alpha1.Rollout{}
				Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
				rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
					{
						Weight: utilpointer.Int32(20),
						Pause:  rolloutsv1alpha1.RolloutPause{},
					},
					{
						Weight: utilpointer.Int32(60),
						Pause:  rolloutsv1alpha1.RolloutPause{},
					},
				}
				rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "StatefulSet",
					Name:       "echoserver",
				}
				rollout.Spec.RolloutID = "1"
				CreateObject(rollout)

				By("Creating workload and waiting for all pods ready...")
				// headless service
				headlessService := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/headless_service.yaml", headlessService)).ToNot(HaveOccurred())
				CreateObject(headlessService)
				// service
				service := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
				CreateObject(service)
				// ingress
				ingress := &netv1.Ingress{}
				Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
				CreateObject(ingress)
				// workload
				workload := &apps.StatefulSet{}
				Expect(ReadYamlToObject("./test_data/rollout/native_statefulset.yaml", workload)).ToNot(HaveOccurred())
				CreateObject(workload)
				WaitNativeStatefulSetPodsReady(workload)

				// v1 -> v2, start rollout action
				newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "2"
				UpdateRollout(rollout)
				UpdateNativeStatefulSet(workload)
				By("Update rollouthistory rolloutID from(1) -> to(2), Update NativeStatefulSet env NODE_NAME from(version1) -> to(version2)")

				// wait step 1 complete
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err := GetRolloutHistory(rollout)
				Expect(err).To(BeNil())
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)

				// check out the num of rollouthistory
				rhs, err := ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 1))

				// check rollout status & paused
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status & paused
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 1))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				By("check rollouthistory status & update success")

				// check workload status & paused
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
				Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
				By("check statefulset status & paused success")

				// resume rollout canary
				ResumeRolloutCanary(rollout.Name)
				By("resume rollout, and wait next step(2)")
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 2)

				// check rollout status & paused
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))

				// check rollouthistory status & paused
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 2))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 2))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 2)
				Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 2))
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
				By("check rollouthistory status & update success")

				// cloneset
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 3))
				Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
				By("check cloneSet status & paused success")

				// resume rollout
				ResumeRolloutCanary(rollout.Name)
				WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
				WaitNativeStatefulSetPodsReady(workload)
				By("rollout completed, and check")

				WaitRolloutHistoryPhase(rollouthistory.Name, rolloutsv1alpha1.PhaseCompleted)

				// check rollout status & paused
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateCompleted))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))

				// check rollouthistory status & paused
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 3))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 3))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateCompleted))
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 2))
				Expect(len(rollouthistory.Status.CanaryStepPods[2].Pods)).Should(BeNumerically("==", 0))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 2)
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
				/// the last rollout
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "3", 0)
				/// Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.StepStatus[2].PodList, rollout.Spec.RolloutID, "3")).Should(BeTrue())
				fmt.Println(rollouthistory.Status.CanaryStepPods) /// dialog
				fmt.Println()
				fmt.Println()

				// check cloneset
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				selector, _ := metav1.LabelSelectorAsSelector(workload.Spec.Selector)
				fmt.Println(ListPods(namespace, selector))
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))
				Expect(workload.Status.ReadyReplicas).Should(BeNumerically("==", *workload.Spec.Replicas))
				By("check cloneSet status & paused success")
				By("check rollouthistory status & update success")
			})

			It("V1->V2: Percentage, 20%, and rollback(v1)", func() {
				By("Creating Rollout...")
				rollout := &rolloutsv1alpha1.Rollout{}
				Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
				rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "StatefulSet",
					Name:       "echoserver",
				}
				CreateObject(rollout)

				By("Creating workload and waiting for all pods ready...")
				// headless service
				headlessService := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/headless_service.yaml", headlessService)).ToNot(HaveOccurred())
				CreateObject(headlessService)
				// service
				service := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
				CreateObject(service)
				// ingress
				ingress := &netv1.Ingress{}
				Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
				CreateObject(ingress)
				// workload
				workload := &apps.StatefulSet{}
				Expect(ReadYamlToObject("./test_data/rollout/native_statefulset.yaml", workload)).ToNot(HaveOccurred())
				CreateObject(workload)
				WaitNativeStatefulSetPodsReady(workload)

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
				stableRevision := rollout.Status.StableRevision
				By("check rollout status & paused success")

				// v1 -> v2, start rollout action
				newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
				workload.Spec.Template.Spec.Containers[0].Image = "echoserver:failed"
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "1"
				UpdateRollout(rollout)
				UpdateNativeStatefulSet(workload)
				By("Update cloneSet env NODE_NAME from(version1) -> to(version2), rolloutID from('') -> to(1)")

				// wait step 1 complete
				time.Sleep(time.Second * 20)

				// check workload status & paused
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
				By("check cloneSet status & paused success")

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateUpgrade))

				// check rollouthistory
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err := GetRolloutHistory(rollout)
				Expect(err).To(BeNil())

				// check rollouthistory num
				rhs, err := ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 1))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 0))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStatePending))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))

				// rollback -> v1
				newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version1"})
				workload.Spec.Template.Spec.Containers[0].Image = "cilium/echoserver:1.10.2"
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "2"
				UpdateRollout(rollout)
				UpdateNativeStatefulSet(workload)
				By("Rollback deployment env NODE_NAME from(version2) -> to(version1), rolloutID from(1) -> to(2)")
				time.Sleep(time.Second * 5)

				// StatefulSet will not remove the broken pod with failed image, we should delete it manually
				brokenPod := &v1.Pod{}
				Expect(GetObject(fmt.Sprintf("%v-%v", workload.Name, *workload.Spec.Replicas-1), brokenPod)).NotTo(HaveOccurred())
				Expect(k8sClient.Delete(context.TODO(), brokenPod)).NotTo(HaveOccurred())

				// check rollouthistory
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err = GetRolloutHistory(rollout)
				Expect(err).To(BeNil())
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)
				ResumeRolloutCanary(rollout.Name)

				WaitRolloutHistoryPhase(rollouthistory.Name, rolloutsv1alpha1.PhaseCompleted)

				// check rollout status & paused
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateCompleted))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 5))

				// check rollouthistory num
				rhs, err = ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 2))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 5))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 5))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateCompleted))
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[2].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[3].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[4].Pods)).Should(BeNumerically("==", 1))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "3", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "4", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "5", 1)
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[2], rollout.Spec.RolloutID, "3")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[3], rollout.Spec.RolloutID, "4")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[4], rollout.Spec.RolloutID, "5")).Should(BeTrue())

				By("rollout completed, and check")
				// check workload
				// cloneset
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))
				Expect(*workload.Spec.UpdateStrategy.RollingUpdate.Partition).Should(BeNumerically("==", 0))

			})

			It("V1->V2: Percentage, 20%,40% and continuous release v3", func() {
				By("Creating Rollout...")
				rollout := &rolloutsv1alpha1.Rollout{}
				Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
				rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "StatefulSet",
					Name:       "echoserver",
				}
				CreateObject(rollout)

				By("Creating workload and waiting for all pods ready...")
				// headless service
				headlessService := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/headless_service.yaml", headlessService)).ToNot(HaveOccurred())
				CreateObject(headlessService)
				// service
				service := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
				CreateObject(service)
				// ingress
				ingress := &netv1.Ingress{}
				Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
				CreateObject(ingress)
				// workload
				workload := &apps.StatefulSet{}
				Expect(ReadYamlToObject("./test_data/rollout/native_statefulset.yaml", workload)).ToNot(HaveOccurred())
				CreateObject(workload)
				WaitNativeStatefulSetPodsReady(workload)

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
				By("check rollout status & paused success")

				// v1 -> v2, start rollout action
				newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "1"
				UpdateRollout(rollout)
				UpdateNativeStatefulSet(workload)
				By("Update cloneSet env NODE_NAME from(version1) -> to(version2)")
				// wait step 1 complete
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err := GetRolloutHistory(rollout)
				Expect(err).To(BeNil())
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)

				// check workload status & paused
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
				By("check cloneSet status & paused success")

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

				// check rollouthistory num
				rhs, err := ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 1))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 1))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				By("check rollouthistory[rollout-id:1 batch-id:1] status & update success")

				// resume rollouthistory canary
				ResumeRolloutCanary(rollout.Name)
				time.Sleep(time.Second * 15)

				// v1 -> v2 -> v3, continuous release
				newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version3"})
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "2"
				UpdateRollout(rollout)
				UpdateNativeStatefulSet(workload)
				By("Update cloneSet env NODE_NAME from(version2) -> to(version3), rollout from(1) -> to(2)")
				time.Sleep(time.Second * 10)

				// wait step 1 complete
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err = GetRolloutHistory(rollout)
				Expect(err).To(BeNil())
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

				// check rollouthistory num
				rhs, err = ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 2))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 1))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				By("check rollouthistory[rollout-id:2 batch-id:1] status & update success")

				// resume rollout canary
				ResumeRolloutCanary(rollout.Name)
				By("check rollout canary status success, resume rollout, and wait rollout canary complete")
				WaitRolloutHistoryPhase(rollouthistory.Name, rolloutsv1alpha1.PhaseCompleted)
				By("rollout completed, and check")

				// cloneset
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 5))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 5))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateCompleted))
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[2].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[3].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[4].Pods)).Should(BeNumerically("==", 1))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "3", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "4", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "5", 1)
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[2], rollout.Spec.RolloutID, "3")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[3], rollout.Spec.RolloutID, "4")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[4], rollout.Spec.RolloutID, "5")).Should(BeTrue())

				// check progressing succeed
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
				Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
				Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			})
		})

		KruiseDescribe("Advanced StatefulSet canary rollout with RolloutHistory", func() {
			It("V1->V2: Percentage, 20%,60% Succeeded", func() {
				By("Creating Rollout...")
				rollout := &rolloutsv1alpha1.Rollout{}
				Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
				rollout.Spec.Strategy.Canary.Steps = []rolloutsv1alpha1.CanaryStep{
					{
						Weight: utilpointer.Int32(20),
						Pause:  rolloutsv1alpha1.RolloutPause{},
					},
					{
						Weight: utilpointer.Int32(60),
						Pause:  rolloutsv1alpha1.RolloutPause{},
					},
				}
				rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
					APIVersion: "apps.kruise.io/v1beta1",
					Kind:       "StatefulSet",
					Name:       "echoserver",
				}
				rollout.Spec.RolloutID = "1"
				CreateObject(rollout)

				By("Creating workload and waiting for all pods ready...")
				// headless service
				headlessService := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/headless_service.yaml", headlessService)).ToNot(HaveOccurred())
				CreateObject(headlessService)
				// service
				service := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
				CreateObject(service)
				// ingress
				ingress := &netv1.Ingress{}
				Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
				CreateObject(ingress)
				// workload
				workload := &appsv1beta1.StatefulSet{}
				Expect(ReadYamlToObject("./test_data/rollout/advanced_statefulset.yaml", workload)).ToNot(HaveOccurred())
				CreateObject(workload)
				WaitAdvancedStatefulSetPodsReady(workload)

				// v1 -> v2, start rollout action
				newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "2"
				UpdateRollout(rollout)
				UpdateAdvancedStatefulSet(workload)
				By("Update rollouthistory rolloutID from(1) -> to(2), update cloneSet env NODE_NAME from(version1) -> to(version2)")

				// wait step 1 complete
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err := GetRolloutHistory(rollout)
				Expect(err).To(BeNil())
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)

				// check out the num of rollouthistory
				rhs, err := ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 1))

				// check rollout status & paused
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status & paused
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 1))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				By("check rollouthistory status & update success")

				// check workload status & paused
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
				By("check cloneSet status & paused success")

				// resume rollout canary
				ResumeRolloutCanary(rollout.Name)
				By("resume rollout, and wait next step(2)")
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 2)

				// check rollout status & paused
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))

				// check rollouthistory status & paused
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 2))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 2))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 2)
				Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 2))
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
				By("check rollouthistory status & update success")

				// cloneset
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 3))
				By("check cloneSet status & paused success")

				// resume rollout
				ResumeRolloutCanary(rollout.Name)
				WaitRolloutStatusPhase(rollout.Name, rolloutsv1alpha1.RolloutPhaseHealthy)
				WaitAdvancedStatefulSetPodsReady(workload)
				By("rollout completed, and check")

				WaitRolloutHistoryPhase(rollouthistory.Name, rolloutsv1alpha1.PhaseCompleted)

				// check rollout status & paused
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateCompleted))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))

				// check rollouthistory status & paused
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 3))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 3))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateCompleted))
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 2))
				Expect(len(rollouthistory.Status.CanaryStepPods[2].Pods)).Should(BeNumerically("==", 0))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 2)
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
				/// the last rollout
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "3", 0)
				/// Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.StepStatus[2].PodList, rollout.Spec.RolloutID, "3")).Should(BeTrue())
				fmt.Println(rollouthistory.Status.CanaryStepPods) /// dialog
				fmt.Println()
				fmt.Println()

				// check cloneset
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				selector, _ := metav1.LabelSelectorAsSelector(workload.Spec.Selector)
				fmt.Println(ListPods(namespace, selector))
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))
				By("check cloneSet status & paused success")
				By("check rollouthistory status & update success")
			})

			It("V1->V2: Percentage, 20%, and rollback(v1)", func() {
				By("Creating Rollout...")
				rollout := &rolloutsv1alpha1.Rollout{}
				Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
				rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
					APIVersion: "apps.kruise.io/v1beta1",
					Kind:       "StatefulSet",
					Name:       "echoserver",
				}
				CreateObject(rollout)

				By("Creating workload and waiting for all pods ready...")
				// service
				headlessService := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/headless_service.yaml", headlessService)).ToNot(HaveOccurred())
				CreateObject(headlessService)
				// service
				service := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
				CreateObject(service)
				// ingress
				ingress := &netv1.Ingress{}
				Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
				CreateObject(ingress)
				// workload
				workload := &appsv1beta1.StatefulSet{}
				Expect(ReadYamlToObject("./test_data/rollout/advanced_statefulset.yaml", workload)).ToNot(HaveOccurred())
				CreateObject(workload)
				WaitAdvancedStatefulSetPodsReady(workload)

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
				stableRevision := rollout.Status.StableRevision
				By("check rollout status & paused success")

				// v1 -> v2, start rollout action
				newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
				workload.Spec.Template.Spec.Containers[0].Image = "echoserver:failed"
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "1"
				UpdateRollout(rollout)
				UpdateAdvancedStatefulSet(workload)
				By("Update cloneSet env NODE_NAME from(version1) -> to(version2), rolloutID from('') -> to(1)")

				// wait step 1 complete
				time.Sleep(time.Second * 20)

				// check workload status & paused
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
				By("check cloneSet status & paused success")

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.StableRevision).Should(Equal(stableRevision))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateUpgrade))

				// check rollouthistory
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err := GetRolloutHistory(rollout)
				Expect(err).To(BeNil())

				// check rollouthistory num
				rhs, err := ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 1))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 0))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStatePending))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))

				// rollback -> v1
				newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version1"})
				workload.Spec.Template.Spec.Containers[0].Image = "cilium/echoserver:1.10.2"
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "2"
				UpdateRollout(rollout)
				UpdateAdvancedStatefulSet(workload)
				By("Rollback deployment env NODE_NAME from(version2) -> to(version1), rolloutID from(1) -> to(2)")
				time.Sleep(time.Second * 5)

				// StatefulSet will not remove the broken pod with failed image, we should delete it manually
				brokenPod := &v1.Pod{}
				Expect(GetObject(fmt.Sprintf("%v-%v", workload.Name, *workload.Spec.Replicas-1), brokenPod)).NotTo(HaveOccurred())
				Expect(k8sClient.Delete(context.TODO(), brokenPod)).NotTo(HaveOccurred())

				// check rollouthistory
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err = GetRolloutHistory(rollout)
				Expect(err).To(BeNil())
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)
				ResumeRolloutCanary(rollout.Name)

				WaitRolloutHistoryPhase(rollouthistory.Name, rolloutsv1alpha1.PhaseCompleted)

				// check rollout status & paused
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStateCompleted))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 5))

				// check rollouthistory num
				rhs, err = ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 2))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 5))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 5))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateCompleted))
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[2].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[3].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[4].Pods)).Should(BeNumerically("==", 1))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "3", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "4", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "5", 1)
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[2], rollout.Spec.RolloutID, "3")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[3], rollout.Spec.RolloutID, "4")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[4], rollout.Spec.RolloutID, "5")).Should(BeTrue())

				By("rollout completed, and check")
				// check workload
				// cloneset
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))

			})

			It("V1->V2: Percentage, 20%,40% and continuous release v3", func() {
				By("Creating Rollout...")
				rollout := &rolloutsv1alpha1.Rollout{}
				Expect(ReadYamlToObject("./test_data/rollout/rollout_canary_base.yaml", rollout)).ToNot(HaveOccurred())
				rollout.Spec.ObjectRef.WorkloadRef = &rolloutsv1alpha1.WorkloadRef{
					APIVersion: "apps.kruise.io/v1beta1",
					Kind:       "StatefulSet",
					Name:       "echoserver",
				}
				CreateObject(rollout)

				By("Creating workload and waiting for all pods ready...")
				headlessService := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/headless_service.yaml", headlessService)).ToNot(HaveOccurred())
				CreateObject(headlessService)
				// service
				service := &v1.Service{}
				Expect(ReadYamlToObject("./test_data/rollout/service.yaml", service)).ToNot(HaveOccurred())
				CreateObject(service)
				// ingress
				ingress := &netv1.Ingress{}
				Expect(ReadYamlToObject("./test_data/rollout/nginx_ingress.yaml", ingress)).ToNot(HaveOccurred())
				CreateObject(ingress)
				// workload
				workload := &appsv1beta1.StatefulSet{}
				Expect(ReadYamlToObject("./test_data/rollout/advanced_statefulset.yaml", workload)).ToNot(HaveOccurred())
				CreateObject(workload)
				WaitAdvancedStatefulSetPodsReady(workload)

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseHealthy))
				By("check rollout status & paused success")

				// v1 -> v2, start rollout action
				newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "1"
				UpdateRollout(rollout)
				UpdateAdvancedStatefulSet(workload)
				By("Update cloneSet env NODE_NAME from(version1) -> to(version2)")
				// wait step 1 complete
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err := GetRolloutHistory(rollout)
				Expect(err).To(BeNil())
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)

				// check workload status & paused
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
				By("check cloneSet status & paused success")

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

				// check rollouthistory num
				rhs, err := ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 1))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 1))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				By("check rollouthistory[rollout-id:1 batch-id:1] status & update success")

				// resume rollouthistory canary
				ResumeRolloutCanary(rollout.Name)
				time.Sleep(time.Second * 15)

				// v1 -> v2 -> v3, continuous release
				newEnvs = mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version3"})
				workload.Spec.Template.Spec.Containers[0].Env = newEnvs
				rollout.Spec.RolloutID = "2"
				UpdateRollout(rollout)
				UpdateAdvancedStatefulSet(workload)
				By("Update cloneSet env NODE_NAME from(version2) -> to(version3), rollout from(1) -> to(2)")
				time.Sleep(time.Second * 10)

				// wait step 1 complete
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				rollouthistory, err = GetRolloutHistory(rollout)
				Expect(err).To(BeNil())
				WaitRolloutHistoryStepPaused(rollouthistory.Name, 1)

				// check rollout status
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(rollout.Status.Phase).Should(Equal(rolloutsv1alpha1.RolloutPhaseProgressing))
				Expect(rollout.Status.CanaryStatus.CurrentStepState).Should(Equal(rolloutsv1alpha1.CanaryStepStatePaused))
				Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))

				// check rollouthistory num
				rhs, err = ListRolloutHistories(namespace)
				Expect(err).To(BeNil())
				Expect(len(rhs)).Should(BeNumerically("==", 2))

				// check rollouthistory spec
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Name).Should(Equal(ingress.Name))
				Expect(rollouthistory.Spec.TrafficRoutingWrapper.IngressWrapper.Ingress).NotTo(BeNil())
				Expect(rollouthistory.Spec.ServiceWrapper.Name).Should(Equal(service.Name))
				Expect(rollouthistory.Spec.ServiceWrapper.Service).NotTo(BeNil())
				Expect(rollouthistory.Spec.Workload.Name).Should(Equal(workload.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Name).Should(Equal(rollout.Name))
				Expect(rollouthistory.Spec.RolloutWrapper.Rollout).NotTo(BeNil())

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 1))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateUpdated))
				Expect(rollouthistory.Status.Phase).Should(Equal(rolloutsv1alpha1.PhaseProgressing))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				By("check rollouthistory[rollout-id:2 batch-id:1] status & update success")

				// resume rollout canary
				ResumeRolloutCanary(rollout.Name)
				By("check rollout canary status success, resume rollout, and wait rollout canary complete")
				WaitRolloutHistoryPhase(rollouthistory.Name, rolloutsv1alpha1.PhaseCompleted)
				By("rollout completed, and check")

				// cloneset
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 5))

				// check rollouthistory status
				Expect(GetObject(rollouthistory.Name, rollouthistory)).NotTo(HaveOccurred())
				Expect(*rollouthistory.Status.CanaryStepIndex).Should(BeNumerically("==", 5))
				Expect(len(rollouthistory.Status.CanaryStepPods)).Should(BeNumerically("==", 5))
				Expect(rollouthistory.Status.CanaryStepState).Should(Equal(rolloutsv1alpha1.CanaryStateCompleted))
				Expect(len(rollouthistory.Status.CanaryStepPods[0].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[1].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[2].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[3].Pods)).Should(BeNumerically("==", 1))
				Expect(len(rollouthistory.Status.CanaryStepPods[4].Pods)).Should(BeNumerically("==", 1))
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "1", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "2", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "3", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "4", 1)
				CheckPodsBatchLabel(namespace, workload.Spec.Selector, rollout.Spec.RolloutID, "5", 1)
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[0], rollout.Spec.RolloutID, "1")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[1], rollout.Spec.RolloutID, "2")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[2], rollout.Spec.RolloutID, "3")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[3], rollout.Spec.RolloutID, "4")).Should(BeTrue())
				Expect(CheckRolloutHistoryPodsBatchLabel(&rollouthistory.Status.CanaryStepPods[4], rollout.Spec.RolloutID, "5")).Should(BeTrue())

				// check progressing succeed
				Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
				Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
				cond := util.GetRolloutCondition(rollout.Status, rolloutsv1alpha1.RolloutConditionProgressing)
				Expect(cond.Reason).Should(Equal(rolloutsv1alpha1.ProgressingReasonSucceeded))
				Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			})
		})
	})

})
