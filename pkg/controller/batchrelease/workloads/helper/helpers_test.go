package helper

import (
	"fmt"
	"math/rand"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFilterPodsForUnorderedRollback(t *testing.T) {
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
			Name: "replicas=10, updatedReplicas=10, noNeedRollback=5, stepCanary=20%, realCanary=6",
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
			Name: "replicas=10, updatedReplicas=10, noNeedRollback=5, stepCanary=60%, realCanary=8",
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
			Name: "replicas=10, updatedReplicas=10, noNeedRollback=5, stepCanary=100%, realCanary=10",
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
			Name: "replicas=10, updatedReplicas=9, noNeedRollback=7, stepCanary=20%, realCanary=6",
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
			Name: "replicas=10, updatedReplicas=9, noNeedRollback=7, stepCanary=60%, realCanary=8",
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
			Name: "replicas=10, updatedReplicas=9, noNeedRollback=7, stepCanary=100%, realCanary=10",
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
			Name: "replicas=10, updatedReplicas=6, noNeedRollback=5, stepCanary=20%, realCanary=6",
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
			Name: "replicas=10, updatedReplicas=6, noNeedRollback=5, stepCanary=60%, realCanary=8",
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
				filteredPods := FilterPodsForUnorderedRollback(pods, cs.PlannedBatchCanaryReplicas, cs.ExpectedBatchStableReplicas, cs.Replicas, "0x1", "version-1")
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

func TestFilterPodsForOrderedRollback(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name                        string
		GetPods                     func() []*corev1.Pod
		ExpectWithLabels            int
		ExpectWithoutLabels         int
		Replicas                    int32
		PlannedBatchCanaryReplicas  int32
		ExpectedBatchStableReplicas int32
	}{
		{
			Name: "replicas=10, updatedReplicas=10, stepCanary=40%, realCanary=2",
			GetPods: func() []*corev1.Pod {
				return generatePods(10, 8)
			},
			Replicas:                    10,
			PlannedBatchCanaryReplicas:  4,
			ExpectedBatchStableReplicas: 8,
			ExpectWithoutLabels:         2,
			ExpectWithLabels:            2,
		},
		{
			Name: "replicas=10, updatedReplicas=10, stepCanary=60%, realCanary=2",
			GetPods: func() []*corev1.Pod {
				return generatePods(10, 8)
			},
			Replicas:                    10,
			PlannedBatchCanaryReplicas:  6,
			ExpectedBatchStableReplicas: 8,
			ExpectWithoutLabels:         2,
			ExpectWithLabels:            4,
		},
		{
			Name: "replicas=10, updatedReplicas=10, stepCanary=100%, realCanary=10",
			GetPods: func() []*corev1.Pod {
				return generatePods(10, 0)
			},
			Replicas:                    10,
			PlannedBatchCanaryReplicas:  10,
			ExpectedBatchStableReplicas: 0,
			ExpectWithoutLabels:         10,
			ExpectWithLabels:            0,
		},
		{
			Name: "replicas=10, updatedReplicas=9, stepCanary=20%, realCanary=2",
			GetPods: func() []*corev1.Pod {
				return generatePods(9, 8)
			},
			Replicas:                    10,
			PlannedBatchCanaryReplicas:  2,
			ExpectedBatchStableReplicas: 8,
			ExpectWithoutLabels:         1,
			ExpectWithLabels:            0,
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
				filteredPods := FilterPodsForOrderedRollback(pods, cs.PlannedBatchCanaryReplicas, cs.ExpectedBatchStableReplicas, cs.Replicas, "0x1", "version-1")
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

func TestSortPodsByOrdinal(t *testing.T) {
	RegisterFailHandler(Fail)

	pods := generatePods(100, 10)
	rand.Shuffle(len(pods), func(i, j int) {
		pods[i], pods[j] = pods[j], pods[i]
	})
	sortPodsByOrdinal(pods)
	for i, pod := range pods {
		expectedName := fmt.Sprintf("pod-name-%d", 99-i)
		Expect(pod.Name == expectedName).Should(BeTrue())
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
				Name:   fmt.Sprintf("pod-name-%d", beginOrder+i),
				Labels: labels,
			},
		}
	}
	return pods
}
