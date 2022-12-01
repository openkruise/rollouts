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

package labelpatch

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestFilterPodsForUnorderedRollback(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)

	cases := []struct {
		Name                   string
		GetPods                func() []*corev1.Pod
		ExpectWithLabels       int
		ExpectWithoutLabels    int
		Replicas               int32
		NoNeedUpdatedReplicas  int32
		PlannedUpdatedReplicas int32
		DesiredUpdatedReplicas int32
	}{
		{
			Name: "replicas=10, updatedReplicas=10, noNeedRollback=5, stepCanary=20%, realCanary=6",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(10, 5)
			},
			Replicas:               10,
			NoNeedUpdatedReplicas:  5,
			PlannedUpdatedReplicas: 2,
			DesiredUpdatedReplicas: 6,
			ExpectWithoutLabels:    5,
			ExpectWithLabels:       1,
		},
		{
			Name: "replicas=10, updatedReplicas=10, noNeedRollback=5, stepCanary=60%, realCanary=8",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(10, 5)
			},
			Replicas:               10,
			NoNeedUpdatedReplicas:  5,
			PlannedUpdatedReplicas: 6,
			DesiredUpdatedReplicas: 8,
			ExpectWithoutLabels:    5,
			ExpectWithLabels:       3,
		},
		{
			Name: "replicas=10, updatedReplicas=10, noNeedRollback=5, stepCanary=100%, realCanary=10",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(10, 5)
			},
			Replicas:               10,
			NoNeedUpdatedReplicas:  5,
			PlannedUpdatedReplicas: 10,
			DesiredUpdatedReplicas: 10,
			ExpectWithoutLabels:    5,
			ExpectWithLabels:       5,
		},
		{
			Name: "replicas=10, updatedReplicas=9, noNeedRollback=7, stepCanary=20%, realCanary=6",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(9, 7)
			},
			Replicas:               10,
			NoNeedUpdatedReplicas:  7,
			PlannedUpdatedReplicas: 2,
			DesiredUpdatedReplicas: 6,
			ExpectWithoutLabels:    2,
			ExpectWithLabels:       7,
		},
		{
			Name: "replicas=10, updatedReplicas=8, noNeedRollback=7, stepCanary=60%, realCanary=8",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(8, 7)
			},
			Replicas:               10,
			NoNeedUpdatedReplicas:  7,
			PlannedUpdatedReplicas: 6,
			DesiredUpdatedReplicas: 8,
			ExpectWithoutLabels:    1,
			ExpectWithLabels:       5,
		},
		{
			Name: "replicas=10, updatedReplicas=9, noNeedRollback=7, stepCanary=100%, realCanary=10",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(9, 7)
			},
			Replicas:               10,
			NoNeedUpdatedReplicas:  7,
			PlannedUpdatedReplicas: 10,
			DesiredUpdatedReplicas: 10,
			ExpectWithoutLabels:    2,
			ExpectWithLabels:       7,
		},
		{
			Name: "replicas=10, updatedReplicas=6, noNeedRollback=5, stepCanary=20%, realCanary=6",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(6, 5)
			},
			Replicas:               10,
			NoNeedUpdatedReplicas:  5,
			PlannedUpdatedReplicas: 2,
			DesiredUpdatedReplicas: 6,
			ExpectWithoutLabels:    1,
			ExpectWithLabels:       1,
		},
		{
			Name: "replicas=10, updatedReplicas=6, noNeedRollback=5, stepCanary=60%, realCanary=8",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(6, 5)
			},
			Replicas:               10,
			NoNeedUpdatedReplicas:  5,
			PlannedUpdatedReplicas: 6,
			DesiredUpdatedReplicas: 8,
			ExpectWithoutLabels:    1,
			ExpectWithLabels:       3,
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
				batchCtx := &batchcontext.BatchContext{
					Replicas:               cs.Replicas,
					RolloutID:              "0x1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: cs.PlannedUpdatedReplicas,
					DesiredUpdatedReplicas: cs.DesiredUpdatedReplicas,
				}
				filteredPods := FilterPodsForUnorderedUpdate(pods, batchCtx)
				var podName []string
				for i := range filteredPods {
					podName = append(podName, filteredPods[i].Name)
				}
				fmt.Println(podName)
				gomega.Expect(check(filteredPods, cs.ExpectWithLabels, cs.ExpectWithoutLabels)).To(gomega.BeTrue())
			}
		})
	}
}

func TestFilterPodsForOrderedRollback(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)

	cases := []struct {
		Name                   string
		GetPods                func() []*corev1.Pod
		ExpectWithLabels       int
		ExpectWithoutLabels    int
		Replicas               int32
		PlannedUpdatedReplicas int32
		DesiredUpdatedReplicas int32
	}{
		{
			Name: "replicas=10, updatedReplicas=10, stepCanary=40%, realCanary=2",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(10, 8)
			},
			Replicas:               10,
			PlannedUpdatedReplicas: 4,
			DesiredUpdatedReplicas: 8,
			ExpectWithoutLabels:    2,
			ExpectWithLabels:       2,
		},
		{
			Name: "replicas=10, updatedReplicas=10, stepCanary=60%, realCanary=2",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(10, 8)
			},
			Replicas:               10,
			PlannedUpdatedReplicas: 6,
			DesiredUpdatedReplicas: 8,
			ExpectWithoutLabels:    2,
			ExpectWithLabels:       4,
		},
		{
			Name: "replicas=10, updatedReplicas=10, stepCanary=100%, realCanary=10",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(10, 0)
			},
			Replicas:               10,
			PlannedUpdatedReplicas: 10,
			DesiredUpdatedReplicas: 0,
			ExpectWithoutLabels:    10,
			ExpectWithLabels:       0,
		},
		{
			Name: "replicas=10, updatedReplicas=9, stepCanary=20%, realCanary=2",
			GetPods: func() []*corev1.Pod {
				return generatePodsWith(9, 8)
			},
			Replicas:               10,
			PlannedUpdatedReplicas: 2,
			DesiredUpdatedReplicas: 8,
			ExpectWithoutLabels:    1,
			ExpectWithLabels:       0,
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
				batchCtx := &batchcontext.BatchContext{
					DesiredUpdatedReplicas: cs.PlannedUpdatedReplicas,
					DesiredPartition:       intstr.FromInt(int(cs.DesiredUpdatedReplicas)),
					Replicas:               cs.Replicas,
					RolloutID:              "0x1",
					UpdateRevision:         "version-1",
					PlannedUpdatedReplicas: cs.PlannedUpdatedReplicas,
				}
				filteredPods := FilterPodsForOrderedUpdate(pods, batchCtx)
				var podName []string
				for i := range filteredPods {
					podName = append(podName, filteredPods[i].Name)
				}
				fmt.Println(podName)
				gomega.Expect(check(filteredPods, cs.ExpectWithLabels, cs.ExpectWithoutLabels)).To(gomega.BeTrue())
			}
		})
	}
}

func TestSortPodsByOrdinal(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)

	pods := generatePodsWith(100, 10)
	rand.Shuffle(len(pods), func(i, j int) {
		pods[i], pods[j] = pods[j], pods[i]
	})
	sortPodsByOrdinal(pods)
	for i, pod := range pods {
		expectedName := fmt.Sprintf("pod-name-%d", 99-i)
		gomega.Expect(pod.Name == expectedName).Should(gomega.BeTrue())
	}
}

func generatePodsWith(updatedReplicas, noNeedRollbackReplicas int) []*corev1.Pod {
	podsNoNeed := generateLabeledPods(map[string]string{
		util.NoNeedUpdatePodLabel:           "0x1",
		apps.ControllerRevisionHashLabelKey: "version-1",
	}, noNeedRollbackReplicas, 0)
	return append(generateLabeledPods(map[string]string{
		apps.ControllerRevisionHashLabelKey: "version-1",
	}, updatedReplicas-noNeedRollbackReplicas, noNeedRollbackReplicas), podsNoNeed...)
}

func generateLabeledPods(labels map[string]string, replicas int, beginOrder int) []*corev1.Pod {
	pods := make([]*corev1.Pod, replicas)
	for i := 0; i < replicas; i++ {
		pods[i] = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   fmt.Sprintf("pod-name-%d", beginOrder+i),
				Labels: labels,
			},
		}
	}
	return pods
}
