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

package workloads

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	apimachineryruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	scheme       *runtime.Scheme
	releaseClone = &v1alpha1.BatchRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "BatchRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release",
			Namespace: "application",
			UID:       uuid.NewUUID(),
		},
		Spec: v1alpha1.BatchReleaseSpec{
			TargetRef: v1alpha1.ObjectRef{
				WorkloadRef: &v1alpha1.WorkloadRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "sample",
				},
			},
			ReleasePlan: v1alpha1.ReleasePlan{
				Batches: []v1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("10%"),
					},
					{
						CanaryReplicas: intstr.FromString("50%"),
					},
					{
						CanaryReplicas: intstr.FromString("80%"),
					},
				},
			},
		},
	}

	stableClone = &kruiseappsv1alpha1.CloneSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kruiseappsv1alpha1.SchemeGroupVersion.String(),
			Kind:       "CloneSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "sample",
			Namespace:  "application",
			UID:        types.UID("87076677"),
			Generation: 1,
			Labels: map[string]string{
				"app": "busybox",
			},
			Annotations: map[string]string{
				"something": "whatever",
			},
		},
		Spec: kruiseappsv1alpha1.CloneSetSpec{
			Replicas: pointer.Int32Ptr(100),
			UpdateStrategy: kruiseappsv1alpha1.CloneSetUpdateStrategy{
				Partition:      &intstr.IntOrString{Type: intstr.Int, IntVal: int32(1)},
				MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: int32(2)},
				MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(2)},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "busybox",
				},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: containers("v2"),
				},
			},
		},
		Status: kruiseappsv1alpha1.CloneSetStatus{
			Replicas:             100,
			ReadyReplicas:        100,
			UpdatedReplicas:      0,
			UpdatedReadyReplicas: 0,
			ObservedGeneration:   1,
		},
	}
)

func init() {
	scheme = runtime.NewScheme()
	apimachineryruntime.Must(apps.AddToScheme(scheme))
	apimachineryruntime.Must(v1alpha1.AddToScheme(scheme))
	apimachineryruntime.Must(kruiseappsv1alpha1.AddToScheme(scheme))

	canaryTemplate := stableClone.Spec.Template.DeepCopy()
	stableTemplate := canaryTemplate.DeepCopy()
	stableTemplate.Spec.Containers = containers("v1")
	stableClone.Status.CurrentRevision = util.ComputeHash(stableTemplate, nil)
	stableClone.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
}

func TestCloneSetController(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name    string
		Paused  bool
		Cleanup bool
	}{
		{
			Name:    "paused=true, cleanup=true",
			Paused:  true,
			Cleanup: true,
		},
		{
			Name:    "paused=true, cleanup=false",
			Paused:  true,
			Cleanup: false,
		},
		{
			Name:    "paused=false cleanup=true",
			Paused:  false,
			Cleanup: true,
		},
		{
			Name:    "paused=false , cleanup=false",
			Paused:  false,
			Cleanup: false,
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(releaseClone.DeepCopy(), stableClone.DeepCopy()).Build()
			rec := record.NewFakeRecorder(100)
			c := cloneSetController{
				workloadController: workloadController{
					client:           cli,
					recorder:         rec,
					parentController: releaseClone,
					releasePlan:      &releaseClone.Spec.ReleasePlan,
					releaseStatus:    &releaseClone.Status,
				},
				targetNamespacedName: client.ObjectKeyFromObject(stableClone),
			}
			oldObject := &kruiseappsv1alpha1.CloneSet{}
			Expect(cli.Get(context.TODO(), c.targetNamespacedName, oldObject)).NotTo(HaveOccurred())
			succeed, err := c.claimCloneSet(oldObject.DeepCopy())
			Expect(succeed).Should(BeTrue())
			Expect(err).NotTo(HaveOccurred())

			newObject := &kruiseappsv1alpha1.CloneSet{}
			Expect(cli.Get(context.TODO(), c.targetNamespacedName, newObject)).NotTo(HaveOccurred())
			succeed, err = c.releaseCloneSet(newObject.DeepCopy(), cs.Cleanup)
			Expect(succeed).Should(BeTrue())
			Expect(err).NotTo(HaveOccurred())

			newObject = &kruiseappsv1alpha1.CloneSet{}
			Expect(cli.Get(context.TODO(), c.targetNamespacedName, newObject)).NotTo(HaveOccurred())
			newObject.Spec.UpdateStrategy.Paused = oldObject.Spec.UpdateStrategy.Paused
			newObject.Spec.UpdateStrategy.Partition = oldObject.Spec.UpdateStrategy.Partition
			Expect(reflect.DeepEqual(oldObject.Spec, newObject.Spec)).Should(BeTrue())
			Expect(reflect.DeepEqual(oldObject.Labels, newObject.Labels)).Should(BeTrue())
			Expect(reflect.DeepEqual(oldObject.Finalizers, newObject.Finalizers)).Should(BeTrue())
			Expect(reflect.DeepEqual(oldObject.Annotations, newObject.Annotations)).Should(BeTrue())
		})
	}
}

func TestParseIntegerAsPercentage(t *testing.T) {
	RegisterFailHandler(Fail)

	supposeUpper := 10000
	for allReplicas := 1; allReplicas <= supposeUpper; allReplicas++ {
		for percent := 0; percent <= 100; percent++ {
			canaryPercent := intstr.FromString(fmt.Sprintf("%v%%", percent))
			canaryReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&canaryPercent, allReplicas, true)
			partition := ParseIntegerAsPercentageIfPossible(int32(allReplicas-canaryReplicas), int32(allReplicas), &canaryPercent)
			stableReplicas, _ := intstr.GetScaledValueFromIntOrPercent(&partition, allReplicas, true)
			if percent == 0 {
				Expect(stableReplicas).Should(BeNumerically("==", allReplicas))
			} else if percent == 100 {
				Expect(stableReplicas).Should(BeNumerically("==", 0))
			} else if percent > 0 {
				Expect(allReplicas - stableReplicas).To(BeNumerically(">", 0))
			}
			Expect(stableReplicas).Should(BeNumerically("<=", allReplicas))
			Expect(math.Abs(float64((allReplicas - canaryReplicas) - stableReplicas))).Should(BeNumerically("<", float64(allReplicas)*0.01))
		}
	}
}

func TestFilterBeforePatchBatchID(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name                        string
		GetPods                     func() []*corev1.Pod
		ExpectWithLabels            int
		ExpectWithoutLabels         int
		Replicas                    int32
		NoNeedRollbackReplicas      int32
		PlannedBatchCanaryReplicas  int32
		ExpectedBatchStableReplicas int32
	}{
		{
			Name: "replicas=10, updatedReplicas=10, noNeedRollback=5, stepCanary=20%, stableCanary=6",
			GetPods: func() []*corev1.Pod {
				return generatePods(10, 5)
			},
			Replicas:                    10,
			NoNeedRollbackReplicas:      5,
			PlannedBatchCanaryReplicas:  2,
			ExpectedBatchStableReplicas: 4,
			ExpectWithoutLabels:         5,
			ExpectWithLabels:            1,
		},
		{
			Name: "replicas=10, updatedReplicas=10, noNeedRollback=5, stepCanary=60%, stableCanary=8",
			GetPods: func() []*corev1.Pod {
				return generatePods(10, 5)
			},
			Replicas:                    10,
			NoNeedRollbackReplicas:      5,
			PlannedBatchCanaryReplicas:  6,
			ExpectedBatchStableReplicas: 2,
			ExpectWithoutLabels:         5,
			ExpectWithLabels:            3,
		},
		{
			Name: "replicas=10, updatedReplicas=10, noNeedRollback=5, stepCanary=100%, stableCanary=10",
			GetPods: func() []*corev1.Pod {
				return generatePods(10, 5)
			},
			Replicas:                    10,
			NoNeedRollbackReplicas:      5,
			PlannedBatchCanaryReplicas:  10,
			ExpectedBatchStableReplicas: 0,
			ExpectWithoutLabels:         5,
			ExpectWithLabels:            5,
		},
		{
			Name: "replicas=10, updatedReplicas=9, noNeedRollback=7, stepCanary=20%, stableCanary=6",
			GetPods: func() []*corev1.Pod {
				return generatePods(9, 7)
			},
			Replicas:                    10,
			NoNeedRollbackReplicas:      7,
			PlannedBatchCanaryReplicas:  2,
			ExpectedBatchStableReplicas: 2,
			ExpectWithoutLabels:         2,
			ExpectWithLabels:            1,
		},
		{
			Name: "replicas=10, updatedReplicas=9, noNeedRollback=7, stepCanary=60%, stableCanary=8",
			GetPods: func() []*corev1.Pod {
				return generatePods(9, 7)
			},
			Replicas:                    10,
			NoNeedRollbackReplicas:      7,
			PlannedBatchCanaryReplicas:  6,
			ExpectedBatchStableReplicas: 1,
			ExpectWithoutLabels:         2,
			ExpectWithLabels:            4,
		},
		{
			Name: "replicas=10, updatedReplicas=9, noNeedRollback=7, stepCanary=100%, stableCanary=10",
			GetPods: func() []*corev1.Pod {
				return generatePods(9, 7)
			},
			Replicas:                    10,
			NoNeedRollbackReplicas:      7,
			PlannedBatchCanaryReplicas:  10,
			ExpectedBatchStableReplicas: 0,
			ExpectWithoutLabels:         2,
			ExpectWithLabels:            7,
		},
		{
			Name: "replicas=10, updatedReplicas=6, noNeedRollback=5, stepCanary=20%, stableCanary=6",
			GetPods: func() []*corev1.Pod {
				return generatePods(6, 5)
			},
			Replicas:                    10,
			NoNeedRollbackReplicas:      5,
			PlannedBatchCanaryReplicas:  2,
			ExpectedBatchStableReplicas: 4,
			ExpectWithoutLabels:         1,
			ExpectWithLabels:            1,
		},
		{
			Name: "replicas=10, updatedReplicas=6, noNeedRollback=5, stepCanary=60%, stableCanary=8",
			GetPods: func() []*corev1.Pod {
				return generatePods(6, 5)
			},
			Replicas:                    10,
			NoNeedRollbackReplicas:      5,
			PlannedBatchCanaryReplicas:  6,
			ExpectedBatchStableReplicas: 2,
			ExpectWithoutLabels:         1,
			ExpectWithLabels:            3,
		},
	}

	check := func(pods []*corev1.Pod, expectWith, expectWithout int) bool {
		var with, without int
		for _, pod := range pods {
			if pod.Labels[util.NoNeedUpdatePodLabel] == "0x1" {
				with++
			} else {
				without++
			}
		}
		return with == expectWith && without == expectWithout
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			pods := cs.GetPods()
			for i := 0; i < 10; i++ {
				rand.Shuffle(len(pods), func(i, j int) {
					pods[i], pods[j] = pods[j], pods[i]
				})
				filteredPods := filterPodsForRollback(pods, cs.PlannedBatchCanaryReplicas, cs.ExpectedBatchStableReplicas, cs.Replicas, "0x1", "version-1")
				var podName []string
				for i := range filteredPods {
					podName = append(podName, filteredPods[i].Name)
				}
				fmt.Println(podName)
				Expect(check(filteredPods, cs.ExpectWithLabels, cs.ExpectWithoutLabels)).To(BeTrue())
			}
		})
	}
}

func generatePods(updatedReplicas, noNeedRollbackReplicas int) []*corev1.Pod {
	podsNoNeed := generatePodsWith(map[string]string{
		util.NoNeedUpdatePodLabel:           "0x1",
		apps.ControllerRevisionHashLabelKey: "version-1",
	}, noNeedRollbackReplicas, 0)
	return append(generatePodsWith(map[string]string{
		apps.ControllerRevisionHashLabelKey: "version-1",
	}, updatedReplicas-noNeedRollbackReplicas, noNeedRollbackReplicas), podsNoNeed...)
}

func generatePodsWith(labels map[string]string, replicas int, beginOrder int) []*corev1.Pod {
	pods := make([]*corev1.Pod, replicas)
	for i := 0; i < replicas; i++ {
		pods[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   fmt.Sprintf("pod-%d", beginOrder+i),
				Labels: labels,
			},
		}
	}
	return pods
}

func containers(version string) []corev1.Container {
	return []corev1.Container{
		{
			Name:  "busybox",
			Image: fmt.Sprintf("busybox:%v", version),
		},
	}
}
