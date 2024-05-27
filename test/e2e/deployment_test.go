package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
)

var _ = SIGDescribe("Advanced Deployment", func() {
	var namespace string

	DumpAllResources := func() {
		deploy := &apps.DeploymentList{}
		k8sClient.List(context.TODO(), deploy, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(deploy))
		rs := &apps.ReplicaSetList{}
		k8sClient.List(context.TODO(), rs, client.InNamespace(namespace))
		fmt.Println(util.DumpJSON(rs))
	}

	defaultRetry := wait.Backoff{
		Steps:    10,
		Duration: 10 * time.Millisecond,
		Factor:   1.0,
		Jitter:   0.1,
	}

	CreateObject := func(object client.Object, options ...client.CreateOption) {
		By(fmt.Sprintf("create deployment %v", client.ObjectKeyFromObject(object)))
		object.SetNamespace(namespace)
		Expect(k8sClient.Create(context.TODO(), object)).NotTo(HaveOccurred())
	}

	GetObject := func(namespace, name string, object client.Object) error {
		key := types.NamespacedName{Namespace: namespace, Name: name}
		return k8sClient.Get(context.TODO(), key, object)
	}

	UpdateDeployment := func(deployment *apps.Deployment, version string, images ...string) *apps.Deployment {
		By(fmt.Sprintf("update deployment %v to version: %v", client.ObjectKeyFromObject(deployment), version))
		var clone *apps.Deployment
		Expect(retry.RetryOnConflict(defaultRetry, func() error {
			clone = &apps.Deployment{}
			err := GetObject(deployment.Namespace, deployment.Name, clone)
			if err != nil {
				return err
			}
			if len(images) == 0 {
				clone.Spec.Template.Spec.Containers[0].Image = deployment.Spec.Template.Spec.Containers[0].Image
			} else {
				clone.Spec.Template.Spec.Containers[0].Image = images[0]
			}
			clone.Spec.Template.Spec.Containers[0].Env[0].Value = version
			strategy := unmarshal(clone.Annotations[rolloutsv1alpha1.DeploymentStrategyAnnotation])
			strategy.Paused = true
			clone.Annotations[rolloutsv1alpha1.DeploymentStrategyAnnotation] = marshal(strategy)
			return k8sClient.Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())

		Eventually(func() bool {
			clone = &apps.Deployment{}
			err := GetObject(deployment.Namespace, deployment.Name, clone)
			Expect(err).NotTo(HaveOccurred())
			By(fmt.Sprintf("image: %s, version env: %s", clone.Spec.Template.Spec.Containers[0].Image, clone.Spec.Template.Spec.Containers[0].Env[0].Value))
			return clone.Status.ObservedGeneration >= clone.Generation
		}, time.Minute, time.Second).Should(BeTrue())
		return clone
	}

	UpdatePartitionWithoutCheck := func(deployment *apps.Deployment, desired intstr.IntOrString) *apps.Deployment {
		By(fmt.Sprintf("update deployment %v to desired: %v", client.ObjectKeyFromObject(deployment), desired))
		var clone *apps.Deployment
		Expect(retry.RetryOnConflict(defaultRetry, func() error {
			clone = &apps.Deployment{}
			err := GetObject(deployment.Namespace, deployment.Name, clone)
			if err != nil {
				return err
			}
			strategy := unmarshal(clone.Annotations[rolloutsv1alpha1.DeploymentStrategyAnnotation])
			if reflect.DeepEqual(desired, strategy.Partition) {
				return nil
			}
			strategy.Paused = false
			strategy.Partition = desired
			clone.Annotations[rolloutsv1alpha1.DeploymentStrategyAnnotation] = marshal(strategy)
			return k8sClient.Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())
		return clone
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

	ListReplicaSets := func(namespace string, labelSelector *metav1.LabelSelector) ([]*apps.ReplicaSet, error) {
		appList := &apps.ReplicaSetList{}
		selector, _ := metav1.LabelSelectorAsSelector(labelSelector)
		err := k8sClient.List(context.TODO(), appList, &client.ListOptions{Namespace: namespace, LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		apps := make([]*apps.ReplicaSet, 0)
		for i := range appList.Items {
			pod := &appList.Items[i]
			if pod.DeletionTimestamp.IsZero() {
				apps = append(apps, pod)
			}
		}
		return apps, nil
	}

	CheckReplicas := func(deployment *apps.Deployment, replicas, available, updated int32) {
		var clone *apps.Deployment
		start := time.Now()
		Eventually(func() bool {
			if start.Add(time.Minute * 2).Before(time.Now()) {
				DumpAllResources()
				Expect(true).Should(BeFalse())
			}
			clone = &apps.Deployment{}
			err := GetObject(deployment.Namespace, deployment.Name, clone)
			Expect(err).NotTo(HaveOccurred())
			fmt.Printf("replicas %d, available: %d, updated: %d\n",
				clone.Status.Replicas, clone.Status.AvailableReplicas, clone.Status.UpdatedReplicas)
			return clone.Status.Replicas == replicas && clone.Status.AvailableReplicas == available && clone.Status.UpdatedReplicas == updated
		}, 10*time.Minute, time.Second).Should(BeTrue())

		Eventually(func() int {
			pods, err := ListPods(deployment.Namespace, deployment.Spec.Selector)
			Expect(err).NotTo(HaveOccurred())
			return len(pods)
		}, 10*time.Second, time.Second).Should(BeNumerically("==", replicas))

		rss, err := ListReplicaSets(deployment.Namespace, deployment.Spec.Selector)
		Expect(err).NotTo(HaveOccurred())
		var rsReplicas, rsAvailable, rsUpdated int32
		for _, rs := range rss {
			if !rs.DeletionTimestamp.IsZero() {
				continue
			}
			if util.EqualIgnoreHash(&rs.Spec.Template, &clone.Spec.Template) {
				rsUpdated = rs.Status.Replicas
			}
			rsReplicas += rs.Status.Replicas
			rsAvailable += rs.Status.AvailableReplicas
		}
		Expect(rsUpdated).Should(BeNumerically("==", updated))
		Expect(rsReplicas).Should(BeNumerically("==", replicas))
		Expect(rsAvailable).Should(BeNumerically("==", available))
	}

	ScaleDeployment := func(deployment *apps.Deployment, replicas int32) *apps.Deployment {
		By(fmt.Sprintf("update deployment %v to replicas: %v", client.ObjectKeyFromObject(deployment), replicas))
		var clone *apps.Deployment
		Expect(retry.RetryOnConflict(defaultRetry, func() error {
			clone = &apps.Deployment{}
			err := GetObject(deployment.Namespace, deployment.Name, clone)
			if err != nil {
				return err
			}
			clone.Spec.Replicas = pointer.Int32(replicas)
			return k8sClient.Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())

		Eventually(func() bool {
			clone = &apps.Deployment{}
			err := GetObject(deployment.Namespace, deployment.Name, clone)
			Expect(err).NotTo(HaveOccurred())
			return clone.Status.ObservedGeneration >= clone.Generation
		}, time.Minute, time.Second).Should(BeTrue())
		return clone
	}

	UpdatePartitionWithCheck := func(deployment *apps.Deployment, desired intstr.IntOrString) {
		By(fmt.Sprintf("update deployment %v to desired: %v, strategy: %v, and check",
			client.ObjectKeyFromObject(deployment), deployment.Annotations[rolloutsv1alpha1.DeploymentStrategyAnnotation], desired))
		clone := UpdatePartitionWithoutCheck(deployment, desired)
		count := 5
		for count > 0 {
			desiredUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&desired, int(*deployment.Spec.Replicas), true)
			CheckReplicas(deployment, *clone.Spec.Replicas, *clone.Spec.Replicas, int32(desiredUpdatedReplicas))
			time.Sleep(time.Second)
			count--
		}
	}

	BeforeEach(func() {
		namespace = randomNamespaceName("deployment")
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
		k8sClient.DeleteAllOf(context.TODO(), &v1.Service{}, client.InNamespace(namespace))
		k8sClient.DeleteAllOf(context.TODO(), &netv1.Ingress{}, client.InNamespace(namespace))
		Expect(k8sClient.Delete(context.TODO(), &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
		time.Sleep(time.Second * 3)
	})

	KruiseDescribe("Advanced Deployment Checker", func() {
		It("update with partition", func() {
			deployment := &apps.Deployment{}
			deployment.Namespace = namespace
			Expect(ReadYamlToObject("./test_data/deployment/deployment.yaml", deployment)).ToNot(HaveOccurred())
			CreateObject(deployment)
			CheckReplicas(deployment, 5, 5, 5)
			UpdateDeployment(deployment, "version2")
			UpdatePartitionWithCheck(deployment, intstr.FromInt(0))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(1))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(2))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(3))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(5))
		})

		It("update with scale up", func() {
			deployment := &apps.Deployment{}
			deployment.Namespace = namespace
			Expect(ReadYamlToObject("./test_data/deployment/deployment.yaml", deployment)).ToNot(HaveOccurred())
			CreateObject(deployment)
			CheckReplicas(deployment, 5, 5, 5)
			UpdateDeployment(deployment, "version2")
			UpdatePartitionWithCheck(deployment, intstr.FromInt(0))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(1))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(2))
			deployment = ScaleDeployment(deployment, 10)
			CheckReplicas(deployment, 10, 10, 4)
			UpdatePartitionWithCheck(deployment, intstr.FromInt(7))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(10))
		})

		It("update with scale down", func() {
			deployment := &apps.Deployment{}
			deployment.Namespace = namespace
			Expect(ReadYamlToObject("./test_data/deployment/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Replicas = pointer.Int32(10)
			CreateObject(deployment)
			CheckReplicas(deployment, 10, 10, 10)
			UpdateDeployment(deployment, "version2")
			UpdatePartitionWithCheck(deployment, intstr.FromString("0%"))
			UpdatePartitionWithCheck(deployment, intstr.FromString("40%"))
			deployment = ScaleDeployment(deployment, 5)
			CheckReplicas(deployment, 5, 5, 2)
			UpdatePartitionWithCheck(deployment, intstr.FromString("60%"))
			UpdatePartitionWithCheck(deployment, intstr.FromString("100%"))
		})

		It("update with MaxSurge=1, MaxUnavailable=0", func() {
			deployment := &apps.Deployment{}
			deployment.Namespace = namespace
			Expect(ReadYamlToObject("./test_data/deployment/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Annotations[rolloutsv1alpha1.DeploymentStrategyAnnotation] =
				`{"rollingStyle":"Partition","rollingUpdate":{"maxUnavailable":0,"maxSurge":1}}`
			CreateObject(deployment)
			CheckReplicas(deployment, 5, 5, 5)
			deployment.Spec.Template.Spec.Containers[0].Image = "failed_image:failed"
			UpdateDeployment(deployment, "version2")
			UpdatePartitionWithCheck(deployment, intstr.FromInt(0))
			UpdatePartitionWithoutCheck(deployment, intstr.FromInt(3))
			CheckReplicas(deployment, 6, 5, 1)
		})

		It("update with MaxSurge=0, MaxUnavailable=1", func() {
			deployment := &apps.Deployment{}
			deployment.Namespace = namespace
			Expect(ReadYamlToObject("./test_data/deployment/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Annotations[rolloutsv1alpha1.DeploymentStrategyAnnotation] =
				`{"rollingStyle":"Partition","rollingUpdate":{"maxUnavailable":1,"maxSurge":0}}`
			deployment.Spec.MinReadySeconds = 10
			CreateObject(deployment)
			CheckReplicas(deployment, 5, 5, 5)
			UpdateDeployment(deployment, "version2")
			UpdatePartitionWithCheck(deployment, intstr.FromInt(0))
			UpdatePartitionWithoutCheck(deployment, intstr.FromInt(3))
			CheckReplicas(deployment, 5, 4, 1)
			CheckReplicas(deployment, 5, 4, 2)
			CheckReplicas(deployment, 5, 4, 3)
			UpdatePartitionWithoutCheck(deployment, intstr.FromInt(5))
			CheckReplicas(deployment, 5, 4, 4)
			CheckReplicas(deployment, 5, 5, 5)
		})

		It("continuous update", func() {
			deployment := &apps.Deployment{}
			deployment.Namespace = namespace
			Expect(ReadYamlToObject("./test_data/deployment/deployment.yaml", deployment)).ToNot(HaveOccurred())
			CreateObject(deployment)
			CheckReplicas(deployment, 5, 5, 5)
			UpdateDeployment(deployment, "version2")
			UpdatePartitionWithCheck(deployment, intstr.FromInt(0))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(2))
			UpdateDeployment(deployment, "version3")
			UpdatePartitionWithCheck(deployment, intstr.FromInt(0))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(3))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(5))
		})

		It("rollback", func() {
			deployment := &apps.Deployment{}
			deployment.Namespace = namespace
			Expect(ReadYamlToObject("./test_data/deployment/deployment.yaml", deployment)).ToNot(HaveOccurred())
			CreateObject(deployment)
			CheckReplicas(deployment, 5, 5, 5)
			UpdateDeployment(deployment, "version2")
			UpdatePartitionWithCheck(deployment, intstr.FromInt(0))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(2))
			UpdateDeployment(deployment, "version3")
			UpdatePartitionWithCheck(deployment, intstr.FromInt(0))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(3))
			UpdateDeployment(deployment, "version2")
			UpdatePartitionWithCheck(deployment, intstr.FromInt(2))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(3))
			UpdatePartitionWithCheck(deployment, intstr.FromInt(5))
		})

		It("scale down old unhealthy first", func() {
			deployment := &apps.Deployment{}
			deployment.Namespace = namespace
			Expect(ReadYamlToObject("./test_data/deployment/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Annotations["rollouts.kruise.io/deployment-strategy"] = `{"rollingUpdate":{"maxUnavailable":0,"maxSurge":1}}`
			CreateObject(deployment)
			CheckReplicas(deployment, 5, 5, 5)
			UpdateDeployment(deployment, "version2", "busybox:not-exists")
			UpdatePartitionWithoutCheck(deployment, intstr.FromInt(1))
			CheckReplicas(deployment, 6, 5, 1)
			UpdateDeployment(deployment, "version3", "busybox:1.32")
			CheckReplicas(deployment, 5, 5, 0)
		})
	})
})

func unmarshal(strategyAnno string) *rolloutsv1alpha1.DeploymentStrategy {
	strategy := &rolloutsv1alpha1.DeploymentStrategy{}
	_ = json.Unmarshal([]byte(strategyAnno), strategy)
	return strategy
}

func marshal(strategy *rolloutsv1alpha1.DeploymentStrategy) string {
	strategyAnno, _ := json.Marshal(strategy)
	return string(strategyAnno)
}
