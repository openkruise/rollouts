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
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	// "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	// "k8s.io/apimachinery/pkg/util/intstr"
	// gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
	// "github.com/openkruise/rollouts/api/v1alpha1"
	// "k8s.io/apimachinery/pkg/api/errors"
)

var _ = SIGDescribe("Rollout v1beta1", func() {
	var namespace string

	DumpAllResources := func() {
		rollout := &v1beta1.RolloutList{}
		k8sClient.List(context.TODO(), rollout, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(rollout))
		batch := &v1beta1.BatchReleaseList{}
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
		asts := &appsv1beta1.StatefulSetList{}
		k8sClient.List(context.TODO(), asts, client.InNamespace(namespace))
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

	getRolloutCondition := func(status v1beta1.RolloutStatus, condType v1beta1.RolloutConditionType) *v1beta1.RolloutCondition {
		for i := range status.Conditions {
			c := status.Conditions[i]
			if c.Type == condType {
				return &c
			}
		}
		return nil
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

	// UpdateDaemonSet := func(object *appsv1alpha1.DaemonSet) *appsv1alpha1.DaemonSet {
	// 	var daemon *appsv1alpha1.DaemonSet
	// 	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
	// 		daemon = &appsv1alpha1.DaemonSet{}
	// 		err := GetObject(object.Name, daemon)
	// 		if err != nil {
	// 			return err
	// 		}
	// 		// daemon.Spec.Replicas = utilpointer.Int32(*object.Spec.Replicas)
	// 		daemon.Spec.Template = *object.Spec.Template.DeepCopy()
	// 		daemon.Spec.UpdateStrategy = *object.Spec.UpdateStrategy.DeepCopy()
	// 		daemon.Labels = mergeMap(daemon.Labels, object.Labels)
	// 		daemon.Annotations = mergeMap(daemon.Annotations, object.Annotations)
	// 		return k8sClient.Update(context.TODO(), daemon)
	// 	})).NotTo(HaveOccurred())

	// 	return daemon
	// }

	// UpdateNativeStatefulSet := func(object *apps.StatefulSet) *apps.StatefulSet {
	// 	var clone *apps.StatefulSet
	// 	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
	// 		clone = &apps.StatefulSet{}
	// 		err := GetObject(object.Name, clone)
	// 		if err != nil {
	// 			return err
	// 		}
	// 		clone.Spec.Replicas = utilpointer.Int32(*object.Spec.Replicas)
	// 		clone.Spec.Template = *object.Spec.Template.DeepCopy()
	// 		clone.Labels = mergeMap(clone.Labels, object.Labels)
	// 		clone.Annotations = mergeMap(clone.Annotations, object.Annotations)
	// 		return k8sClient.Update(context.TODO(), clone)
	// 	})).NotTo(HaveOccurred())

	// 	return clone
	// }

	// UpdateAdvancedStatefulSet := func(object *appsv1beta1.StatefulSet) *appsv1beta1.StatefulSet {
	// 	var clone *appsv1beta1.StatefulSet
	// 	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
	// 		clone = &appsv1beta1.StatefulSet{}
	// 		err := GetObject(object.Name, clone)
	// 		if err != nil {
	// 			return err
	// 		}
	// 		clone.Spec.Replicas = utilpointer.Int32(*object.Spec.Replicas)
	// 		clone.Spec.Template = *object.Spec.Template.DeepCopy()
	// 		clone.Labels = mergeMap(clone.Labels, object.Labels)
	// 		clone.Annotations = mergeMap(clone.Annotations, object.Annotations)
	// 		return k8sClient.Update(context.TODO(), clone)
	// 	})).NotTo(HaveOccurred())

	// 	return clone
	// }

	UpdateRollout := func(object *v1beta1.Rollout) *v1beta1.Rollout {
		var clone *v1beta1.Rollout
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone = &v1beta1.Rollout{}
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

	RolloutJumpCanaryStep := func(name string, target int) {
		Eventually(func() bool {
			clone := &v1beta1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStatus.CurrentStepState != v1beta1.CanaryStepStatePaused {
				fmt.Println("Jump successfully, and current status ", util.DumpJSON(clone.Status))
				return true
			}

			body := fmt.Sprintf(`{"status":{"canaryStatus":{"nextStepIndex":%d}}}`, target)
			Expect(k8sClient.Status().Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))).NotTo(HaveOccurred())
			return false
		}, 10*time.Second, time.Second).Should(BeTrue())
	}

	// RolloutJumpBlueGreenStep := func(name string, target int) {
	// 	Eventually(func() bool {
	// 		clone := &v1alpha1.Rollout{}
	// 		Expect(GetObject(name, clone)).NotTo(HaveOccurred())
	// 		if clone.Status.CanaryStatus.CurrentStepState !=v1beta1.CanaryStepStatePaused {
	// 			fmt.Println("Jump successfully, and current status ", util.DumpJSON(clone.Status))
	// 			return true
	// 		}

	// 		body := fmt.Sprintf(`{"status":{"blueGreenStatus":{"nextStepIndex":"%d"}}}`, target)
	// 		Expect(k8sClient.Status().Patch(context.TODO(), clone, client.RawPatch(types.MergePatchType, []byte(body)))).NotTo(HaveOccurred())
	// 		return false
	// 	}, 10*time.Second, time.Second).Should(BeTrue())
	// }

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

	// WaitNativeStatefulSetPodsReady := func(statefulset *apps.StatefulSet) {
	// 	Eventually(func() bool {
	// 		set := &apps.StatefulSet{}
	// 		Expect(GetObject(statefulset.Name, set)).NotTo(HaveOccurred())
	// 		return set.Status.ObservedGeneration == set.Generation && *set.Spec.Replicas == set.Status.UpdatedReplicas &&
	// 			*set.Spec.Replicas == set.Status.ReadyReplicas && *set.Spec.Replicas == set.Status.Replicas
	// 	}, 20*time.Minute, 3*time.Second).Should(BeTrue())
	// }

	// WaitAdvancedStatefulSetPodsReady := func(statefulset *appsv1beta1.StatefulSet) {
	// 	Eventually(func() bool {
	// 		set := &appsv1beta1.StatefulSet{}
	// 		Expect(GetObject(statefulset.Name, set)).NotTo(HaveOccurred())
	// 		return set.Status.ObservedGeneration == set.Generation && *set.Spec.Replicas == set.Status.UpdatedReplicas &&
	// 			*set.Spec.Replicas == set.Status.ReadyReplicas && *set.Spec.Replicas == set.Status.Replicas
	// 	}, 20*time.Minute, 3*time.Second).Should(BeTrue())
	// }

	// WaitDaemonSetAllPodsReady := func(daemonset *appsv1alpha1.DaemonSet) {
	// 	Eventually(func() bool {
	// 		daemon := &appsv1alpha1.DaemonSet{}
	// 		Expect(GetObject(daemonset.Name, daemon)).NotTo(HaveOccurred())
	// 		klog.Infof("DaemonSet updateStrategy(%s) Generation(%d) ObservedGeneration(%d) DesiredNumberScheduled(%d) UpdatedNumberScheduled(%d) NumberReady(%d)",
	// 			util.DumpJSON(daemon.Spec.UpdateStrategy), daemon.Generation, daemon.Status.ObservedGeneration, daemon.Status.DesiredNumberScheduled, daemon.Status.UpdatedNumberScheduled, daemon.Status.NumberReady)
	// 		return daemon.Status.ObservedGeneration == daemon.Generation && daemon.Status.DesiredNumberScheduled == daemon.Status.UpdatedNumberScheduled && daemon.Status.DesiredNumberScheduled == daemon.Status.NumberReady
	// 	}, 5*time.Minute, time.Second).Should(BeTrue())
	// }

	// WaitDeploymentReplicas := func(deployment *apps.Deployment) {
	// 	Eventually(func() bool {
	// 		clone := &apps.Deployment{}
	// 		Expect(GetObject(deployment.Name, clone)).NotTo(HaveOccurred())
	// 		return clone.Status.ObservedGeneration == clone.Generation &&
	// 			*clone.Spec.Replicas == clone.Status.ReadyReplicas && *clone.Spec.Replicas == clone.Status.Replicas
	// 	}, 10*time.Minute, time.Second).Should(BeTrue())
	// }

	WaitRolloutCanaryStepPaused := func(name string, stepIndex int32) {
		start := time.Now()
		Eventually(func() bool {
			if start.Add(time.Minute * 5).Before(time.Now()) {
				DumpAllResources()
				Expect(true).Should(BeFalse())
			}
			clone := &v1beta1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			if clone.Status.CanaryStatus == nil {
				return false
			}
			klog.Infof("current step:%v target step:%v current step state %v", clone.Status.CanaryStatus.CurrentStepIndex, stepIndex, clone.Status.CanaryStatus.CurrentStepState)
			return clone.Status.CanaryStatus.CurrentStepIndex == stepIndex && clone.Status.CanaryStatus.CurrentStepState == v1beta1.CanaryStepStatePaused
		}, 20*time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutStatusPhase := func(name string, phase v1beta1.RolloutPhase) {
		Eventually(func() bool {
			clone := &v1beta1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			return clone.Status.Phase == phase
		}, 20*time.Minute, time.Second).Should(BeTrue())
	}

	WaitRolloutWorkloadGeneration := func(name string, generation int64) {
		Eventually(func() bool {
			clone := &v1beta1.Rollout{}
			Expect(GetObject(name, clone)).NotTo(HaveOccurred())
			return clone.Status.CanaryStatus.ObservedWorkloadGeneration == generation
		}, time.Minute, time.Second).Should(BeTrue())
	}

	// WaitRolloutNotFound := func(name string) {
	// 	Eventually(func() bool {
	// 		clone := &v1beta1.Rollout{}
	// 		err := GetObject(name, clone)
	// 		if err == nil {
	// 			return false
	// 		} else if errors.IsNotFound(err) {
	// 			return true
	// 		} else {
	// 			Expect(err).NotTo(HaveOccurred())
	// 			return false
	// 		}
	// 	}, 5*time.Minute, time.Second).Should(BeTrue())
	// }

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

	// ListPods := func(namespace string, labelSelector *metav1.LabelSelector) ([]*v1.Pod, error) {
	// 	appList := &v1.PodList{}
	// 	selector, _ := metav1.LabelSelectorAsSelector(labelSelector)
	// 	err := k8sClient.List(context.TODO(), appList, &client.ListOptions{Namespace: namespace, LabelSelector: selector})
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	apps := make([]*v1.Pod, 0)
	// 	for i := range appList.Items {
	// 		pod := &appList.Items[i]
	// 		if pod.DeletionTimestamp.IsZero() {
	// 			apps = append(apps, pod)
	// 		}
	// 	}
	// 	return apps, nil
	// }

	// CheckPodBatchLabel := func(namespace string, labelSelector *metav1.LabelSelector, rolloutID, batchID string, expected int) {
	// 	pods, err := ListPods(namespace, labelSelector)
	// 	Expect(err).NotTo(HaveOccurred())

	// 	count := 0
	// 	for _, pod := range pods {
	// 		if pod.Labels[v1beta1.RolloutIDLabel] == rolloutID &&
	// 			pod.Labels[v1beta1.RolloutBatchIDLabel] == batchID {
	// 			count++
	// 		}
	// 	}
	// 	Expect(count).Should(BeNumerically("==", expected))
	// }

	ListReplicaSet := func(d *apps.Deployment) []*apps.ReplicaSet {
		var rss []*apps.ReplicaSet
		rsLister := &apps.ReplicaSetList{}
		selectorOpt, _ := metav1.LabelSelectorAsSelector(d.Spec.Selector)
		err := k8sClient.List(context.TODO(), rsLister, &client.ListOptions{LabelSelector: selectorOpt, Namespace: d.Namespace})
		Expect(err).NotTo(HaveOccurred())
		for i := range rsLister.Items {
			rs := &rsLister.Items[i]
			if !rs.DeletionTimestamp.IsZero() {
				continue
			}
			rss = append(rss, rs)
		}
		return rss
	}

	GetStableRSRevision := func(d *apps.Deployment) string {
		rss := ListReplicaSet(d)
		_, stable := util.FindCanaryAndStableReplicaSet(rss, d)
		if stable != nil {
			return stable.Labels[apps.DefaultDeploymentUniqueLabelKey]
		}
		return ""
	}

	GetCanaryRSRevision := func(d *apps.Deployment) string {
		rss := ListReplicaSet(d)
		canary, _ := util.FindCanaryAndStableReplicaSet(rss, d)
		if canary != nil {
			return canary.Labels[apps.DefaultDeploymentUniqueLabelKey]
		}
		return ""
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
		k8sClient.DeleteAllOf(context.TODO(), &v1beta1.BatchRelease{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &v1beta1.Rollout{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &v1.Service{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &netv1.Ingress{}, client.InNamespace(namespace))
		Expect(k8sClient.Delete(context.TODO(), &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
		time.Sleep(time.Second * 3)
	})

	KruiseDescribe("Step Jump", func() {
		// step1-> 2-> 3-> 4-> 3-(TrafficChange)-> 3-> 2-> 1-> 5
		It("V1->V2: Deployment, Canary, patch nextStepIndex to jump", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1beta1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_v1beta1_canary_base.yaml", rollout)).ToNot(HaveOccurred())
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
			By("wait step(1) pause")
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 2))
			// canary workload
			cWorkload, err := GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			crss, err := finder.GetReplicaSetsForDeployment(cWorkload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(crss)).Should(BeNumerically("==", 1))
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 1))
			// workload
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", 5))

			// wait step 2 complete
			By("wait step(2) pause")
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 3))
			// canary workload
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 2))
			// workload
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", 5))

			// wait step 3 complete
			By("wait step(3) pause")
			ResumeRolloutCanary(rollout.Name)
			// rollout
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 4))
			// canary workload
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 3))
			// workload
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", 5))

			// wait step 4 complete
			By("wait step(4) pause")
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 4)
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 5))
			// canary workload
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			canaryRevision := crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 4))
			// workload
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", 5))
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			// canary service
			cService := &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress := &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[3].Traffic)))

			// Jump to step 3
			By("Jump to step 3")
			RolloutJumpCanaryStep(rollout.Name, 3)
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 4))
			// canary workload (won't scale down indeed)
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			canaryRevision = crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 4))
			// workload
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", 5))
			// canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[2].Traffic)))

			// Change traffic of current step, which shouldn't cause jump
			By("Change traffic of step 3")
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// update rollout step configuration
			rollout.Spec.Strategy.Canary.Steps = []v1beta1.CanaryStep{
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("21%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "20%"},
					Pause:    v1beta1.RolloutPause{},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("41%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "40%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("61%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "60%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("81%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "80%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("100%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
			}
			rollout = UpdateRollout(rollout)
			By("update rollout configuration, and wait rollout re-run current step(3)")
			time.Sleep(time.Second * 3)
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// batch release
			batch := &v1beta1.BatchRelease{}
			Expect(GetObject(rollout.Name, batch)).NotTo(HaveOccurred())
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 4))
			// canary workload (won't scale down indeed)
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			canaryRevision = crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 4))
			// workload
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", 5))
			// canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[2].Traffic)))

			// Jump to step 2
			By("Jump to step 2")
			RolloutJumpCanaryStep(rollout.Name, 2)
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 3))
			// canary workload (won't scale down indeed)
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			canaryRevision = crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 4))
			// workload
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", 5))
			// canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[1].Traffic)))

			// Jump to step 1
			By("Jump to step 1")
			RolloutJumpCanaryStep(rollout.Name, 1)
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 2))
			// canary workload (won't scale down indeed)
			cWorkload, err = GetCanaryDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			canaryRevision = crss[0].Labels[apps.DefaultDeploymentUniqueLabelKey]
			Expect(cWorkload.Status.AvailableReplicas).Should(BeNumerically("==", 4))
			// workload
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 0))
			Expect(workload.Status.AvailableReplicas).Should(BeNumerically("==", 5))
			// canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[0].Traffic)))

			// Jump to step 5
			By("Jump to step 5")
			RolloutJumpCanaryStep(rollout.Name, 5)
			// wait rollout complete
			WaitRolloutStatusPhase(rollout.Name, v1beta1.RolloutPhaseHealthy)
			klog.Infof("rollout(%s) completed, and check", namespace)
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", -1))
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
			cond := getRolloutCondition(rollout.Status, v1beta1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1beta1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1beta1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)

		})

		// step1-> 2-> 3-> 4-> 3-(TrafficChange)-> 3-> 2-> 1-> 5
		It("V1->V2: Deployment, Partition, patch nextStepIndex to jump", func() {
			finder := util.NewControllerFinder(k8sClient)
			By("Creating Rollout...")
			rollout := &v1beta1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_v1beta1_partition_base.yaml", rollout)).ToNot(HaveOccurred())
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
			rss, err := finder.GetReplicaSetsForDeployment(workload)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rss)).Should(BeNumerically("==", 1))

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateDeployment(workload)
			By("Update deployment image from(version1) -> to(version2)")
			time.Sleep(time.Second * 3)

			// wait step 1 complete
			By("wait step(1) pause")
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			stableRevision := GetStableRSRevision(workload)
			By(stableRevision)
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
			strategy := util.GetDeploymentStrategy(workload)
			extraStatus := util.GetDeploymentExtraStatus(workload)
			Expect(extraStatus.UpdatedReadyReplicas).Should(BeNumerically("==", 1))
			Expect(strategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(util.ComputeHash(&workload.Spec.Template, nil)))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(GetCanaryRSRevision(workload)))
			canaryRevision := rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 2))
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
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))

			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[0].Traffic)))

			// wait step 2 complete
			By("wait step(2) pause")
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			stableRevision = GetStableRSRevision(workload)
			By(stableRevision)
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 2))
			strategy = util.GetDeploymentStrategy(workload)
			extraStatus = util.GetDeploymentExtraStatus(workload)
			Expect(extraStatus.UpdatedReadyReplicas).Should(BeNumerically("==", 2))
			Expect(strategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(util.ComputeHash(&workload.Spec.Template, nil)))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(GetCanaryRSRevision(workload)))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[1].Traffic)))

			// wait step 3 complete
			By("wait step(3) pause")
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			stableRevision = GetStableRSRevision(workload)
			By(stableRevision)
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 3))
			strategy = util.GetDeploymentStrategy(workload)
			extraStatus = util.GetDeploymentExtraStatus(workload)
			Expect(extraStatus.UpdatedReadyReplicas).Should(BeNumerically("==", 3))
			Expect(strategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(util.ComputeHash(&workload.Spec.Template, nil)))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(GetCanaryRSRevision(workload)))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[2].Traffic)))

			// wait step 4 complete
			By("wait step(4) pause")
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 4)
			stableRevision = GetStableRSRevision(workload)
			By(stableRevision)
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 4))
			strategy = util.GetDeploymentStrategy(workload)
			extraStatus = util.GetDeploymentExtraStatus(workload)
			Expect(extraStatus.UpdatedReadyReplicas).Should(BeNumerically("==", 4))
			Expect(strategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(util.ComputeHash(&workload.Spec.Template, nil)))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(GetCanaryRSRevision(workload)))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 5))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[3].Traffic)))

			// Jump to step 3
			By("Jump to step 3")
			RolloutJumpCanaryStep(rollout.Name, 3)
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			stableRevision = GetStableRSRevision(workload)
			By(stableRevision)
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			// won't scale down
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 4))
			strategy = util.GetDeploymentStrategy(workload)
			extraStatus = util.GetDeploymentExtraStatus(workload)
			Expect(extraStatus.UpdatedReadyReplicas).Should(BeNumerically("==", 4))
			Expect(strategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(util.ComputeHash(&workload.Spec.Template, nil)))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(GetCanaryRSRevision(workload)))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[2].Traffic)))

			// Change traffic of current step, which shouldn't cause jump
			By("Change traffic of step 3")
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// update rollout step configuration
			rollout.Spec.Strategy.Canary.Steps = []v1beta1.CanaryStep{
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("21%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "20%"},
					Pause:    v1beta1.RolloutPause{},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("41%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "40%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("61%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "60%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("81%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "80%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("100%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
			}
			rollout = UpdateRollout(rollout)
			By("update rollout configuration, and wait rollout re-run current step(3)")
			time.Sleep(time.Second * 3)
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// batch release
			batch := &v1beta1.BatchRelease{}
			Expect(GetObject(rollout.Name, batch)).NotTo(HaveOccurred())
			stableRevision = GetStableRSRevision(workload)
			By(stableRevision)
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			// won't scale down
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 4))
			strategy = util.GetDeploymentStrategy(workload)
			extraStatus = util.GetDeploymentExtraStatus(workload)
			Expect(extraStatus.UpdatedReadyReplicas).Should(BeNumerically("==", 4))
			Expect(strategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(util.ComputeHash(&workload.Spec.Template, nil)))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(GetCanaryRSRevision(workload)))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[2].Traffic)))

			// Jump to step 2
			By("Jump to step 2")
			RolloutJumpCanaryStep(rollout.Name, 2)
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			stableRevision = GetStableRSRevision(workload)
			By(stableRevision)
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 4))
			strategy = util.GetDeploymentStrategy(workload)
			extraStatus = util.GetDeploymentExtraStatus(workload)
			Expect(extraStatus.UpdatedReadyReplicas).Should(BeNumerically("==", 4))
			Expect(strategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(util.ComputeHash(&workload.Spec.Template, nil)))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(GetCanaryRSRevision(workload)))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[1].Traffic)))

			// Jump to step 1
			By("Jump to step 1")
			RolloutJumpCanaryStep(rollout.Name, 1)
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			stableRevision = GetStableRSRevision(workload)
			By(stableRevision)
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 4))
			strategy = util.GetDeploymentStrategy(workload)
			extraStatus = util.GetDeploymentExtraStatus(workload)
			Expect(extraStatus.UpdatedReadyReplicas).Should(BeNumerically("==", 4))
			Expect(strategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(util.ComputeHash(&workload.Spec.Template, nil)))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(GetCanaryRSRevision(workload)))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[0].Traffic)))

			// Jump to step 5
			By("Jump to step 5")
			RolloutJumpCanaryStep(rollout.Name, 5)
			// wait rollout complete
			WaitRolloutStatusPhase(rollout.Name, v1beta1.RolloutPhase(v1beta1.RolloutPhaseHealthy))
			klog.Infof("rollout(%s) completed, and check", namespace)
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", -1))
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
			cond := getRolloutCondition(rollout.Status, v1beta1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1beta1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1beta1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)
		})

		// step1-> 2-> 3-> 4-> 3-(TrafficChange)-> 3-> 2-> 1-> 5
		It("V1->V2: CloneSet, Partition, patch nextStepIndex to jump", func() {
			By("Creating Rollout...")
			rollout := &v1beta1.Rollout{}
			Expect(ReadYamlToObject("./test_data/rollout/rollout_v1beta1_partition_base.yaml", rollout)).ToNot(HaveOccurred())
			rollout.Spec.WorkloadRef = v1beta1.ObjectRef{
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
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseHealthy))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(workload.Status.CurrentRevision[strings.LastIndex(workload.Status.CurrentRevision, "-")+1:]))
			stableRevision := rollout.Status.CanaryStatus.StableRevision
			By("check rollout status & paused success")

			// v1 -> v2, start rollout action
			newEnvs := mergeEnvVar(workload.Spec.Template.Spec.Containers[0].Env, v1.EnvVar{Name: "NODE_NAME", Value: "version2"})
			workload.Spec.Template.Spec.Containers[0].Env = newEnvs
			UpdateCloneSet(workload)
			By("Update cloneSet env NODE_NAME from(version1) -> to(version2)")
			time.Sleep(time.Second * 3)

			// wait step 1 complete
			By("wait step(1) pause")
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 1))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			canaryRevision := rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 2))
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
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[0].Traffic)))

			// wait step 2 complete
			By("wait step(2) pause")
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 2))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 2))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[1].Traffic)))

			// wait step 3 complete
			By("wait step(3) pause")
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[2].Traffic)))

			// Change traffic of current step, which shouldn't cause jump
			By("Change traffic of step 3")
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// update rollout step configuration
			rollout.Spec.Strategy.Canary.Steps = []v1beta1.CanaryStep{
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("21%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "20%"},
					Pause:    v1beta1.RolloutPause{},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("41%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "40%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("61%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "60%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("81%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "80%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
				{
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Traffic: utilpointer.StringPtr("100%"),
					},
					Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
					Pause: v1beta1.RolloutPause{
						Duration: utilpointer.Int32(10),
					},
				},
			}
			rollout = UpdateRollout(rollout)
			By("update rollout configuration, and wait rollout re-run current step(3)")
			time.Sleep(time.Second * 3)
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// batch release
			batch := &v1beta1.BatchRelease{}
			Expect(GetObject(rollout.Name, batch)).NotTo(HaveOccurred())
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 3))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[2].Traffic)))

			// wait step 4 complete
			By("wait step(4) pause")
			ResumeRolloutCanary(rollout.Name)
			WaitRolloutCanaryStepPaused(rollout.Name, 4)
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 4))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 4))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 5))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[3].Traffic)))

			// Jump to step 3
			By("Jump to step 3")
			RolloutJumpCanaryStep(rollout.Name, 3)
			WaitRolloutCanaryStepPaused(rollout.Name, 3)
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 4))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 4))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 4))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[2].Traffic)))

			// Jump to step 2
			By("Jump to step 2")
			RolloutJumpCanaryStep(rollout.Name, 2)
			WaitRolloutCanaryStepPaused(rollout.Name, 2)
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 4))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 4))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 3))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[1].Traffic)))

			// Jump to step 1
			By("Jump to step 1")
			RolloutJumpCanaryStep(rollout.Name, 1)
			WaitRolloutCanaryStepPaused(rollout.Name, 1)
			// check workload status & paused
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			Expect(workload.Status.UpdatedReplicas).Should(BeNumerically("==", 4))
			Expect(workload.Status.UpdatedReadyReplicas).Should(BeNumerically("==", 4))
			Expect(workload.Spec.UpdateStrategy.Paused).Should(BeFalse())
			By("check cloneSet status & paused success")
			// check rollout status
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.Phase).Should(Equal(v1beta1.RolloutPhaseProgressing))
			Expect(rollout.Status.CanaryStatus.StableRevision).Should(Equal(stableRevision))
			Expect(rollout.Status.CanaryStatus.CanaryRevision).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			Expect(rollout.Status.CanaryStatus.PodTemplateHash).Should(Equal(workload.Status.UpdateRevision[strings.LastIndex(workload.Status.UpdateRevision, "-")+1:]))
			canaryRevision = rollout.Status.CanaryStatus.PodTemplateHash
			Expect(rollout.Status.CanaryStatus.CurrentStepIndex).Should(BeNumerically("==", 1))
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", 2))
			Expect(rollout.Status.CanaryStatus.RolloutHash).Should(Equal(rollout.Annotations[util.RolloutHashAnnotation]))
			// check stable, canary service & ingress
			// stable service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(stableRevision))
			//canary service
			cService = &v1.Service{}
			Expect(GetObject(service.Name+"-canary", cService)).NotTo(HaveOccurred())
			Expect(cService.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(canaryRevision))
			// canary ingress
			cIngress = &netv1.Ingress{}
			Expect(GetObject(service.Name+"-canary", cIngress)).NotTo(HaveOccurred())
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary", nginxIngressAnnotationDefaultPrefix)]).Should(Equal("true"))
			Expect(cIngress.Annotations[fmt.Sprintf("%s/canary-weight", nginxIngressAnnotationDefaultPrefix)]).Should(Equal(removePercentageSign(*rollout.Spec.Strategy.Canary.Steps[0].Traffic)))

			// Jump to step 5
			By("Jump to step 5")
			RolloutJumpCanaryStep(rollout.Name, 5)
			// wait rollout complete
			WaitRolloutStatusPhase(rollout.Name, v1beta1.RolloutPhase(v1beta1.RolloutPhaseHealthy))
			klog.Infof("rollout(%s) completed, and check", namespace)
			// rollout
			Expect(GetObject(rollout.Name, rollout)).NotTo(HaveOccurred())
			Expect(rollout.Status.CanaryStatus.NextStepIndex).Should(BeNumerically("==", -1))
			// check service & ingress
			// ingress
			Expect(GetObject(ingress.Name, ingress)).NotTo(HaveOccurred())
			cIngress = &netv1.Ingress{}
			Expect(GetObject(fmt.Sprintf("%s-canary", ingress.Name), cIngress)).To(HaveOccurred())
			// service
			Expect(GetObject(service.Name, service)).NotTo(HaveOccurred())
			Expect(service.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey]).Should(Equal(""))
			cService = &v1.Service{}
			Expect(GetObject(fmt.Sprintf("%s-canary", service.Name), cService)).To(HaveOccurred())
			// clonese
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
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
			cond := getRolloutCondition(rollout.Status, v1beta1.RolloutConditionProgressing)
			Expect(cond.Reason).Should(Equal(v1beta1.ProgressingReasonCompleted))
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionFalse)))
			cond = getRolloutCondition(rollout.Status, v1beta1.RolloutConditionSucceeded)
			Expect(string(cond.Status)).Should(Equal(string(metav1.ConditionTrue)))
			Expect(GetObject(workload.Name, workload)).NotTo(HaveOccurred())
			WaitRolloutWorkloadGeneration(rollout.Name, workload.Generation)

		})
	})
})

func removePercentageSign(input string) string {
	if input == "0" {
		return "0"
	}
	if strings.HasSuffix(input, "%") {
		return strings.TrimSuffix(input, "%")
	}
	fmt.Printf("input(%s) has no percentage sign!", input)
	return ""
}
