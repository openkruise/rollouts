package e2e

import (
	"context"
	"fmt"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads"
	"github.com/openkruise/rollouts/test/images"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/integer"
	"k8s.io/utils/pointer"
	"sort"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = SIGDescribe("Test BatchRelease Controller", func() {
	var namespace string

	CreateObject := func(object client.Object, options ...client.CreateOption) {
		object.SetNamespace(namespace)
		Expect(k8sClient.Create(context.TODO(), object)).NotTo(HaveOccurred())
	}

	GetObject := func(namespace, name string, object client.Object) error {
		key := types.NamespacedName{Namespace: namespace, Name: name}
		return k8sClient.Get(context.TODO(), key, object)
	}

	DeleteObject := func(object client.Object, options ...client.DeleteOption) {
		Expect(k8sClient.Delete(context.TODO(), object)).NotTo(HaveOccurred())
	}

	UpdateCloneSet := func(object *kruiseappsv1alpha1.CloneSet) *kruiseappsv1alpha1.CloneSet {
		var clone *kruiseappsv1alpha1.CloneSet
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone = &kruiseappsv1alpha1.CloneSet{}
			err := GetObject(object.Namespace, object.Name, clone)
			if err != nil {
				return err
			}
			clone.Spec = *object.Spec.DeepCopy()
			return k8sClient.Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())

		return clone
	}

	WaitCloneSetAllPodsReady := func(cloneset *kruiseappsv1alpha1.CloneSet) {
		Eventually(func() bool {
			clone := &kruiseappsv1alpha1.CloneSet{}
			Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
			return clone.Status.ObservedGeneration == clone.Generation && clone.Status.Replicas == clone.Status.ReadyReplicas
		}, 20*time.Minute, time.Second).Should(BeTrue())
	}

	GetUpdateRevision := func(cloneset *kruiseappsv1alpha1.CloneSet) string {
		var revision string
		Eventually(func() bool {
			clone := &kruiseappsv1alpha1.CloneSet{}
			Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
			revision = clone.Status.UpdateRevision
			return clone.Status.ObservedGeneration == clone.Generation
		}, 20*time.Minute, time.Second).Should(BeTrue())
		return revision
	}

	UpdateDeployment := func(object *apps.Deployment) *apps.Deployment {
		var clone *apps.Deployment
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			clone = &apps.Deployment{}
			err := GetObject(object.Namespace, object.Name, clone)
			if err != nil {
				return err
			}
			clone.Spec = *object.Spec.DeepCopy()
			return k8sClient.Update(context.TODO(), clone)
		})).NotTo(HaveOccurred())

		return clone
	}

	GetCanaryDeployment := func(deployment *apps.Deployment) *apps.Deployment {
		var dList *apps.DeploymentList
		fetchedDeployment := &apps.Deployment{}
		Expect(GetObject(deployment.Namespace, deployment.Name, fetchedDeployment)).NotTo(HaveOccurred())
		Eventually(func() int {
			dList = &apps.DeploymentList{}
			Expect(k8sClient.List(
				context.TODO(), dList,
				client.InNamespace(deployment.Namespace),
				client.MatchingLabels(map[string]string{workloads.CanaryDeploymentLabelKey: string(fetchedDeployment.UID)}))).NotTo(HaveOccurred())
			return len(dList.Items)
		}, 5*time.Minute, time.Second).Should(BeNumerically(">", 0))

		var ds []*apps.Deployment
		for i := range dList.Items {
			d := &dList.Items[i]
			if d.DeletionTimestamp != nil {
				continue
			}
			ds = append(ds, d)
		}

		sort.Slice(ds, func(i, j int) bool {
			return ds[j].CreationTimestamp.Before(&ds[i].CreationTimestamp)
		})

		return ds[0]
	}

	WaitDeploymentAllPodsReady := func(deployment *apps.Deployment) {
		Eventually(func() bool {
			clone := &apps.Deployment{}
			Expect(GetObject(deployment.Namespace, deployment.Name, clone)).NotTo(HaveOccurred())
			return clone.Status.ObservedGeneration == clone.Generation && clone.Status.Replicas == clone.Status.ReadyReplicas
		}, 20*time.Minute, time.Second).Should(BeTrue())
	}

	BeforeEach(func() {
		namespace = randomNamespaceName("batchrelease")
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
		Expect(k8sClient.Delete(context.TODO(), &v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, client.PropagationPolicy(metav1.DeletePropagationForeground))).Should(Succeed())
	})

	KruiseDescribe("CloneSet BatchRelease Checker", func() {

		It("V1->V2: Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/cloneset_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			cloneset := &kruiseappsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/workload/cloneset.yaml", cloneset)).ToNot(HaveOccurred())
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(cloneset)
			WaitCloneSetAllPodsReady(cloneset)

			// record stable revision --> v1
			stableRevision := GetUpdateRevision(cloneset)

			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(5)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateCloneSet(cloneset)

			// record canary revision --> v2
			canaryRevision := GetUpdateRevision(cloneset)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking CloneSet updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneset.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))
		})

		It("V1->V2: Percentage, 50%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/cloneset_percentage_50.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			cloneset := &kruiseappsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/workload/cloneset.yaml", cloneset)).ToNot(HaveOccurred())
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(cloneset)
			WaitCloneSetAllPodsReady(cloneset)

			// record stable revision --> v1
			stableRevision := GetUpdateRevision(cloneset)

			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(5)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateCloneSet(cloneset)

			// record canary revision --> v2
			canaryRevision := GetUpdateRevision(cloneset)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking CloneSet updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneset.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() int32 {
				clone := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas
			}, 5*time.Minute, time.Second).Should(Equal(*cloneset.Spec.Replicas))
		})

		It("V1->V2(Completed)->V3: Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease....")
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/cloneset_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating CloneSet and waiting for all pods ready....")
			By("Creating workload and waiting for all pods ready...")
			cloneset := &kruiseappsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/workload/cloneset.yaml", cloneset)).ToNot(HaveOccurred())
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(cloneset)
			WaitCloneSetAllPodsReady(cloneset)
			stableRevisionV1 := GetUpdateRevision(cloneset)

			/*************************************************************************************
							Start to release V1->V2
			 *************************************************************************************/
			By("Start to release V1->V2....")
			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(5)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateCloneSet(cloneset)

			// record canary revision --> v2
			canaryRevisionV2 := GetUpdateRevision(cloneset)
			Expect(canaryRevisionV2).ShouldNot(Equal(stableRevisionV1))

			By("V1->V2: Checking CloneSet updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneset.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("V1->V2: Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			/*************************************************************************************
							V1->V2 Succeeded, Start to release V2->V3
			 *************************************************************************************/
			By("Start to release V2->V3....")
			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(5)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV3)
			UpdateCloneSet(cloneset)

			// record canary revision --> v3
			canaryRevisionV3 := GetUpdateRevision(cloneset)
			Expect(canaryRevisionV3).ShouldNot(Equal(stableRevisionV1))
			Expect(canaryRevisionV3).ShouldNot(Equal(canaryRevisionV2))

			By("V2->V3: Checking CloneSet updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneset.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, 20*time.Second, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("V2->V3: Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))
		})

		It("V1->V2(UnCompleted)->V3: Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease....")
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/cloneset_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating CloneSet and waiting for all pods ready....")
			By("Creating workload and waiting for all pods ready...")
			cloneset := &kruiseappsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/workload/cloneset.yaml", cloneset)).ToNot(HaveOccurred())
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(cloneset)
			WaitCloneSetAllPodsReady(cloneset)
			stableRevisionV1 := GetUpdateRevision(cloneset)

			/*************************************************************************************
							Start to release V1->V2
			 *************************************************************************************/
			By("Start to release V1->V2....")
			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(5)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateCloneSet(cloneset)

			// record canary revision --> v2
			canaryRevisionV2 := GetUpdateRevision(cloneset)
			Expect(canaryRevisionV2).ShouldNot(Equal(stableRevisionV1))

			By("V1->V2: Checking CloneSet updated replicas...")
			for i := 0; i < len(release.Spec.ReleasePlan.Batches)-2; i++ {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneset.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("V1->V2: Checking BatchRelease status...")
			clone := &rolloutsv1alpha1.BatchRelease{}
			Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
			Expect(clone.Status.Phase).ShouldNot(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			/*************************************************************************************
							V1->V2 Succeeded, Start to release V2->V3
			 *************************************************************************************/
			By("Start to release V2->V3....")
			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(5)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV3)
			UpdateCloneSet(cloneset)

			// record canary revision --> v3
			canaryRevisionV3 := GetUpdateRevision(cloneset)
			Expect(canaryRevisionV3).ShouldNot(Equal(stableRevisionV1))
			Expect(canaryRevisionV3).ShouldNot(Equal(canaryRevisionV2))

			By("V2->V3: Checking CloneSet updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneset.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("V2->V3: Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))
		})

		It("V1->V2: ScalingUp, Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/cloneset_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			cloneset := &kruiseappsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/workload/cloneset.yaml", cloneset)).ToNot(HaveOccurred())
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(cloneset)
			WaitCloneSetAllPodsReady(cloneset)

			// record stable revision --> v1
			stableRevision := GetUpdateRevision(cloneset)

			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(5)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateCloneSet(cloneset)

			// record canary revision --> v2
			canaryRevision := GetUpdateRevision(cloneset)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking CloneSet updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				cloneCopy := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, cloneCopy)).NotTo(HaveOccurred())
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneCopy.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(BeNumerically(">=", int32(expectedUpdatedReplicas)))
				if i == 1 {
					By("\tScaling up from 5 to 10...")
					cloneCopy := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, cloneCopy)).NotTo(HaveOccurred())
					cloneCopy.Spec.Replicas = pointer.Int32Ptr(10)
					UpdateCloneSet(cloneCopy)
				}
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() bool {
				clone := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas == *clone.Spec.Replicas
			}, 10*time.Minute, time.Second).Should(BeTrue())
		})

		It("V1->V2: ScalingDown, Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/cloneset_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			cloneset := &kruiseappsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/workload/cloneset.yaml", cloneset)).ToNot(HaveOccurred())
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(cloneset)
			WaitCloneSetAllPodsReady(cloneset)

			// record stable revision --> v1
			stableRevision := GetUpdateRevision(cloneset)

			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(10)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateCloneSet(cloneset)

			// record canary revision --> v2
			canaryRevision := GetUpdateRevision(cloneset)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking CloneSet updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				cloneCopy := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, cloneCopy)).NotTo(HaveOccurred())
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneCopy.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(BeNumerically(">=", int32(expectedUpdatedReplicas)))
				if i == 1 {
					By("\tScaling down from 10 to 2...")
					cloneCopy := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, cloneCopy)).NotTo(HaveOccurred())
					cloneCopy.Spec.Replicas = pointer.Int32Ptr(2)
					UpdateCloneSet(cloneCopy)
				}
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() bool {
				clone := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas == *clone.Spec.Replicas
			}, 10*time.Minute, time.Second).Should(BeTrue())
		})

		It("V1->V2: ScalingUp, Number, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/cloneset_number_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			cloneset := &kruiseappsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/workload/cloneset.yaml", cloneset)).ToNot(HaveOccurred())
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(cloneset)
			WaitCloneSetAllPodsReady(cloneset)

			// record stable revision --> v1
			stableRevision := GetUpdateRevision(cloneset)

			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(5)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateCloneSet(cloneset)

			// record canary revision --> v2
			canaryRevision := GetUpdateRevision(cloneset)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking CloneSet updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				cloneCopy := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, cloneCopy)).NotTo(HaveOccurred())
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneCopy.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(BeNumerically(">=", int32(expectedUpdatedReplicas)))
				if i == 1 {
					By("\tScaling up from 5 to 10...")
					cloneCopy := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, cloneCopy)).NotTo(HaveOccurred())
					cloneCopy.Spec.Replicas = pointer.Int32Ptr(10)
					UpdateCloneSet(cloneCopy)
				}
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() bool {
				clone := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas == *clone.Spec.Replicas
			}, 10*time.Minute, time.Second).Should(BeTrue())
		})

		It("V1->V2: ScalingDown, Number, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/cloneset_number_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			cloneset := &kruiseappsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/workload/cloneset.yaml", cloneset)).ToNot(HaveOccurred())
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(cloneset)
			WaitCloneSetAllPodsReady(cloneset)

			// record stable revision --> v1
			stableRevision := GetUpdateRevision(cloneset)

			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(10)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateCloneSet(cloneset)

			// record canary revision --> v2
			canaryRevision := GetUpdateRevision(cloneset)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking CloneSet updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				cloneCopy := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, cloneCopy)).NotTo(HaveOccurred())
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*cloneCopy.Spec.Replicas), true)
				expectedUpdatedReplicas = integer.IntMin(expectedUpdatedReplicas, int(*cloneCopy.Spec.Replicas))
				Eventually(func() int32 {
					clone := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(BeNumerically(">=", int32(expectedUpdatedReplicas)))
				if i == 1 {
					By("\tScaling down from 10 to 2...")
					cloneCopy := &kruiseappsv1alpha1.CloneSet{}
					Expect(GetObject(cloneset.Namespace, cloneset.Name, cloneCopy)).NotTo(HaveOccurred())
					cloneCopy.Spec.Replicas = pointer.Int32Ptr(2)
					UpdateCloneSet(cloneCopy)
				}
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() bool {
				clone := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas == *clone.Spec.Replicas
			}, 10*time.Minute, time.Second).Should(BeTrue())
		})

		It("Rollback V1->V2->V1: Percentage, 100%, Succeeded", func() {
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/cloneset_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			cloneset := &kruiseappsv1alpha1.CloneSet{}
			Expect(ReadYamlToObject("./test_data/workload/cloneset.yaml", cloneset)).ToNot(HaveOccurred())
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			cloneset.Spec.Template.Spec.Containers[0].ImagePullPolicy = v1.PullIfNotPresent
			CreateObject(cloneset)
			WaitCloneSetAllPodsReady(cloneset)

			// record stable revision --> v1
			stableRevision := GetUpdateRevision(cloneset)

			cloneset.Spec.UpdateStrategy.Paused = true
			cloneset.Spec.Replicas = pointer.Int32Ptr(10)
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.FailedImage)
			UpdateCloneSet(cloneset)

			// record canary revision --> v2
			canaryRevision := GetUpdateRevision(cloneset)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Waiting a minute and checking failed revision...")
			time.Sleep(time.Minute)
			for i := 0; i < 30; i++ {
				fetchedRelease := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, fetchedRelease)).NotTo(HaveOccurred())
				Expect(fetchedRelease.Status.CanaryStatus.CurrentBatch).Should(Equal(int32(1)))
				time.Sleep(time.Second)
			}

			By("Updating cloneset to V1...")
			cloneset.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			UpdateCloneSet(cloneset)

			By("Checking all pod were updated when release completed...")
			Eventually(func() bool {
				clone := &kruiseappsv1alpha1.CloneSet{}
				Expect(GetObject(cloneset.Namespace, cloneset.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas == *clone.Spec.Replicas
			}, 10*time.Minute, time.Second).Should(BeTrue())

			By("Checking BatchRelease completed status phase...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCancelled))
		})
	})

	KruiseDescribe("Deployment BatchRelease Checker", func() {

		It("V1->V2: Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)

			// record stable revision --> v1
			stableRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(5)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking Deployment updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*deployment.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.Replicas
				}, 5*time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))
		})

		It("V1->V2: Percentage, 50%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_percentage_50.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)

			// record stable revision --> v1
			stableRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(5)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking Deployment updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*deployment.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 5*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() int32 {
				clone := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas
			}, 10*time.Minute, time.Second).Should(Equal(*deployment.Spec.Replicas))
		})

		It("V1->V2(Completed)->V3: Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease....")
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating Deployment and waiting for all pods ready....")
			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)
			stableRevisionV1 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			/*************************************************************************************
							Start to release V1->V2
			 *************************************************************************************/
			By("Start to release V1->V2....")
			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(5)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevisionV2 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevisionV2).ShouldNot(Equal(stableRevisionV1))

			By("V1->V2: Checking Deployment updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*deployment.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("V1->V2: Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			/*************************************************************************************
							V1->V2 Succeeded, Start to release V2->V3
			 *************************************************************************************/
			By("Start to release V2->V3....")
			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(5)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV3)
			UpdateDeployment(deployment)

			// record canary revision --> v3
			canaryRevisionV3 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevisionV3).ShouldNot(Equal(stableRevisionV1))
			Expect(canaryRevisionV3).ShouldNot(Equal(canaryRevisionV2))

			By("V2->V3: Checking Deployment updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*deployment.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("V2->V3: Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))
		})

		It("V1->V2(UnCompleted)->V3: Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease....")
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating Deployment and waiting for all pods ready....")
			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)
			stableRevisionV1 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			/*************************************************************************************
							Start to release V1->V2
			 *************************************************************************************/
			By("Start to release V1->V2....")
			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(5)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevisionV2 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevisionV2).ShouldNot(Equal(stableRevisionV1))

			By("V1->V2: Checking Deployment updated replicas...")
			for i := 0; i < len(release.Spec.ReleasePlan.Batches)-1; i++ {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*deployment.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("V1->V2: Checking BatchRelease status...")
			clone := &rolloutsv1alpha1.BatchRelease{}
			Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
			Expect(clone.Status.Phase).ShouldNot(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			/*************************************************************************************
							V1->V2 Not Completed, Start to release V1,V2->V3
			 *************************************************************************************/
			By("Start to release V1,V2->V3...")
			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(5)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV3)
			UpdateDeployment(deployment)

			// record canary revision --> v3
			canaryRevisionV3 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevisionV3).ShouldNot(Equal(stableRevisionV1))
			Expect(canaryRevisionV3).ShouldNot(Equal(canaryRevisionV2))

			By("V2->V3: Checking Deployment updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*deployment.Spec.Replicas), true)
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(Equal(int32(expectedUpdatedReplicas)))
				time.Sleep(time.Duration(batch.PauseSeconds) * time.Second)
			}

			By("V2->V3: Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))
		})

		It("V1->V2: ScalingUp, Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)

			// record stable revision --> v1
			stableRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(5)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking Deployment updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				fetchedDeployment := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, fetchedDeployment)).NotTo(HaveOccurred())
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*fetchedDeployment.Spec.Replicas), true)
				expectedUpdatedReplicas = integer.IntMin(expectedUpdatedReplicas, int(*fetchedDeployment.Spec.Replicas))
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(BeNumerically(">=", int32(expectedUpdatedReplicas)))
				if i == 1 {
					By("\tScaling up from 5 to 10....")
					deployCopy := &apps.Deployment{}
					Expect(GetObject(deployment.Namespace, deployment.Name, deployCopy)).NotTo(HaveOccurred())
					deployCopy.Spec.Replicas = pointer.Int32Ptr(10)
					UpdateDeployment(deployCopy)
				}
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() int32 {
				clone := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas
			}, 10*time.Minute, time.Second).Should(Equal(*deployment.Spec.Replicas))
		})

		It("V1->V2: ScalingDown, Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)

			// record stable revision --> v1
			stableRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(10)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking Deployment updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				fetchedDeployment := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, fetchedDeployment)).NotTo(HaveOccurred())
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*fetchedDeployment.Spec.Replicas), true)
				expectedUpdatedReplicas = integer.IntMin(expectedUpdatedReplicas, int(*fetchedDeployment.Spec.Replicas))
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(BeNumerically(">=", int32(expectedUpdatedReplicas)))
				if i == 1 {
					By("\tScaling down from 10 to 2....")
					deployCopy := &apps.Deployment{}
					Expect(GetObject(deployment.Namespace, deployment.Name, deployCopy)).NotTo(HaveOccurred())
					deployCopy.Spec.Replicas = pointer.Int32Ptr(2)
					UpdateDeployment(deployCopy)
				}
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() bool {
				clone := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas == *clone.Spec.Replicas
			}, 10*time.Minute, time.Second).Should(BeTrue())
		})

		It("V1->V2: ScalingUp, Number, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_number_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)

			// record stable revision --> v1
			stableRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(5)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking Deployment updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				batch := &release.Spec.ReleasePlan.Batches[i]
				fetchedDeployment := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, fetchedDeployment)).NotTo(HaveOccurred())
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*fetchedDeployment.Spec.Replicas), true)
				expectedUpdatedReplicas = integer.IntMin(expectedUpdatedReplicas, int(*fetchedDeployment.Spec.Replicas))
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(BeNumerically(">=", int32(expectedUpdatedReplicas)))
				if i == 1 {
					By("\tScaling up from 5 to 10....")
					deployCopy := &apps.Deployment{}
					Expect(GetObject(deployment.Namespace, deployment.Name, deployCopy)).NotTo(HaveOccurred())
					deployCopy.Spec.Replicas = pointer.Int32Ptr(10)
					UpdateDeployment(deployCopy)
				}
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() bool {
				clone := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas == *clone.Spec.Replicas
			}, 10*time.Minute, time.Second).Should(BeTrue())
		})

		It("V1->V2: ScalingDown, Number, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_number_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)

			// record stable revision --> v1
			stableRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(5)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV2)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevision := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevision).ShouldNot(Equal(stableRevision))

			By("Checking Deployment updated replicas...")
			for i := range release.Spec.ReleasePlan.Batches {
				By(fmt.Sprintf("\tWaiting for batch[%v] completed...", i))
				batch := &release.Spec.ReleasePlan.Batches[i]
				fetchedDeployment := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, fetchedDeployment)).NotTo(HaveOccurred())
				expectedUpdatedReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&batch.CanaryReplicas, int(*fetchedDeployment.Spec.Replicas), true)
				expectedUpdatedReplicas = integer.IntMin(expectedUpdatedReplicas, int(*fetchedDeployment.Spec.Replicas))
				Eventually(func() int32 {
					clone := GetCanaryDeployment(deployment)
					return clone.Status.UpdatedReplicas
				}, 5*time.Minute, time.Second).Should(BeNumerically(">=", int32(expectedUpdatedReplicas)))
				if i == 1 {
					By("\tScaling down from 10 to 2....")
					deployCopy := &apps.Deployment{}
					Expect(GetObject(deployment.Namespace, deployment.Name, deployCopy)).NotTo(HaveOccurred())
					deployCopy.Spec.Replicas = pointer.Int32Ptr(2)
					UpdateDeployment(deployCopy)
				}
			}

			By("Checking BatchRelease status...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 10*time.Minute, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCompleted))

			By("Checking all pod were updated when release completed...")
			Eventually(func() bool {
				clone := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas == *clone.Spec.Replicas
			}, 10*time.Minute, time.Second).Should(BeTrue())
		})

		It("Rollback V1->V2->V1: Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy = v1.PullIfNotPresent
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)

			// record stable revision --> v1
			stableRevisionV1 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			deployment.Spec.Paused = true
			deployment.Spec.Replicas = pointer.Int32Ptr(10)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.FailedImage)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevisionV2 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevisionV2).ShouldNot(Equal(stableRevisionV1))

			By("Waiting a minute and checking failed revision...")
			time.Sleep(time.Minute)
			for i := 0; i < 30; i++ {
				fetchedRelease := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, fetchedRelease)).NotTo(HaveOccurred())
				Expect(fetchedRelease.Status.CanaryStatus.CurrentBatch).Should(Equal(int32(1)))
				time.Sleep(time.Second)
			}

			By("Updating cloneset to V1...")
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			UpdateDeployment(deployment)
			// record canary revision --> v2
			canaryRevisionV3 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevisionV3).Should(Equal(stableRevisionV1))

			By("Checking all pod were updated when release completed...")
			Eventually(func() int32 {
				clone := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas
			}, 100*time.Second, time.Second).Should(Equal(*deployment.Spec.Replicas))

			By("Checking BatchRelease completed status phase...")
			Eventually(func() rolloutsv1alpha1.RolloutPhase {
				clone := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.Phase
			}, 100*time.Second, time.Second).Should(Equal(rolloutsv1alpha1.RolloutPhaseCancelled))
		})

		It("Rollback V1->V2: Delete BatchRelease, Percentage, 100%, Succeeded", func() {
			By("Creating BatchRelease...")
			release := &rolloutsv1alpha1.BatchRelease{}
			Expect(ReadYamlToObject("./test_data/batchrelease/deployment_percentage_100.yaml", release)).ToNot(HaveOccurred())
			CreateObject(release)

			By("Creating workload and waiting for all pods ready...")
			deployment := &apps.Deployment{}
			Expect(ReadYamlToObject("./test_data/workload/deployment.yaml", deployment)).ToNot(HaveOccurred())
			deployment.Spec.Replicas = pointer.Int32Ptr(10)
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			deployment.Spec.Template.Spec.Containers[0].ImagePullPolicy = v1.PullIfNotPresent
			CreateObject(deployment)
			WaitDeploymentAllPodsReady(deployment)

			// record stable revision --> v1
			stableRevisionV1 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)

			deployment.Spec.Paused = true
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.FailedImage)
			UpdateDeployment(deployment)

			// record canary revision --> v2
			canaryRevisionV2 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevisionV2).ShouldNot(Equal(stableRevisionV1))

			By("Waiting a minute and checking failed revision...")
			time.Sleep(time.Minute)
			for i := 0; i < 30; i++ {
				fetchedRelease := &rolloutsv1alpha1.BatchRelease{}
				Expect(GetObject(release.Namespace, release.Name, fetchedRelease)).NotTo(HaveOccurred())
				Expect(fetchedRelease.Status.CanaryStatus.CurrentBatch).Should(Equal(int32(1)))
				time.Sleep(time.Second)
			}

			By("Deleting BatchReleasing...")
			DeleteObject(release)
			Eventually(func() bool {
				objectCopy := &rolloutsv1alpha1.BatchRelease{}
				err := GetObject(release.Namespace, release.Name, objectCopy)
				return errors.IsNotFound(err)
			}, time.Minute, time.Second).Should(BeTrue())

			By("Updating cloneset to V1...")
			deployment.Spec.Template.Spec.Containers[0].Image = images.GetE2EImage(images.BusyBoxV1)
			UpdateDeployment(deployment)
			// record canary revision --> v2
			canaryRevisionV3 := workloads.ComputeHash(&deployment.Spec.Template, deployment.Status.CollisionCount)
			Expect(canaryRevisionV3).Should(Equal(stableRevisionV1))

			By("Checking all pod were updated when release completed...")
			Eventually(func() int32 {
				clone := &apps.Deployment{}
				Expect(GetObject(deployment.Namespace, deployment.Name, clone)).NotTo(HaveOccurred())
				return clone.Status.UpdatedReplicas
			}, 100*time.Second, time.Second).Should(Equal(*deployment.Spec.Replicas))
		})
	})
})
