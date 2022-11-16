package workloads

import (
	"fmt"
	"math/rand"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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
				filteredPods := filterPodsForUnorderedRollback(pods, cs.PlannedBatchCanaryReplicas, cs.ExpectedBatchStableReplicas, cs.Replicas, "0x1", "version-1")
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
				filteredPods := filterPodsForOrderedRollback(pods, cs.PlannedBatchCanaryReplicas, cs.ExpectedBatchStableReplicas, cs.Replicas, "0x1", "version-1")
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

func TestIsBatchReady(t *testing.T) {
	RegisterFailHandler(Fail)

	p := func(f intstr.IntOrString) *intstr.IntOrString {
		return &f
	}
	r := func(f *intstr.IntOrString, id, revision string) *v1alpha1.BatchRelease {
		return &v1alpha1.BatchRelease{
			Spec:   v1alpha1.BatchReleaseSpec{ReleasePlan: v1alpha1.ReleasePlan{RolloutID: id, FailureThreshold: f}},
			Status: v1alpha1.BatchReleaseStatus{UpdateRevision: revision},
		}
	}
	cases := map[string]struct {
		release        *v1alpha1.BatchRelease
		pods           []*corev1.Pod
		maxUnavailable *intstr.IntOrString
		labelDesired   int32
		desired        int32
		updated        int32
		updatedReady   int32
		result         bool
	}{
		"ready: no-rollout-id, all pod ready": {
			release:        r(p(intstr.FromInt(1)), "", "v2"),
			pods:           nil,
			maxUnavailable: p(intstr.FromInt(1)),
			labelDesired:   5,
			desired:        5,
			updated:        5,
			updatedReady:   5,
			result:         true,
		},
		"ready: no-rollout-id, tolerated failed pods": {
			release:        r(p(intstr.FromInt(1)), "", "v2"),
			pods:           nil,
			maxUnavailable: p(intstr.FromInt(1)),
			labelDesired:   5,
			desired:        5,
			updated:        5,
			updatedReady:   4,
			result:         true,
		},
		"false: no-rollout-id, un-tolerated failed pods": {
			release:        r(p(intstr.FromInt(1)), "", "v2"),
			pods:           nil,
			maxUnavailable: p(intstr.FromInt(5)),
			labelDesired:   5,
			desired:        5,
			updated:        5,
			updatedReady:   3,
			result:         false,
		},
		"false: no-rollout-id, tolerated failed pods, but 1 pod isn't updated": {
			release:        r(p(intstr.FromString("60%")), "", "v2"),
			pods:           nil,
			maxUnavailable: p(intstr.FromInt(5)),
			labelDesired:   5,
			desired:        5,
			updated:        4,
			updatedReady:   4,
			result:         false,
		},
		"false: no-rollout-id, tolerated, but no-pod-ready": {
			release:        r(p(intstr.FromInt(100)), "", "v2"),
			pods:           nil,
			maxUnavailable: p(intstr.FromInt(5)),
			labelDesired:   5,
			desired:        5,
			updated:        5,
			updatedReady:   0,
			result:         false,
		},
		"true: no-rollout-id, tolerated failed pods, failureThreshold=nil": {
			release:        r(nil, "", "v2"),
			pods:           nil,
			maxUnavailable: p(intstr.FromInt(3)),
			labelDesired:   5,
			desired:        5,
			updated:        5,
			updatedReady:   3,
			result:         true,
		},
		"false: no-rollout-id, un-tolerated failed pods, failureThreshold=nil": {
			release:        r(nil, "", "v2"),
			pods:           nil,
			maxUnavailable: p(intstr.FromInt(1)),
			labelDesired:   5,
			desired:        5,
			updated:        5,
			updatedReady:   3,
			result:         false,
		},
		"true: rollout-id, labeled pods satisfied": {
			release:        r(p(intstr.FromInt(1)), "1", "version-1"),
			pods:           generatePods(5, 0),
			maxUnavailable: p(intstr.FromInt(5)),
			labelDesired:   5,
			desired:        5,
			updated:        5,
			updatedReady:   5,
			result:         true,
		},
		"false: rollout-id, labeled pods not satisfied": {
			release:        r(p(intstr.FromInt(1)), "1", "version-1"),
			pods:           generatePods(3, 0),
			maxUnavailable: p(intstr.FromInt(5)),
			labelDesired:   5,
			desired:        5,
			updated:        5,
			updatedReady:   5,
			result:         false,
		},
		"true: rollout-id, no updated-ready field": {
			release:        r(p(intstr.FromInt(1)), "1", "version-1"),
			pods:           generatePods(5, 0),
			maxUnavailable: p(intstr.FromInt(5)),
			labelDesired:   5,
			desired:        5,
			updated:        5,
			updatedReady:   0,
			result:         true,
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			got := isBatchReady(cs.release, cs.pods, cs.maxUnavailable, cs.labelDesired, cs.desired, cs.updated, cs.updatedReady)
			fmt.Printf("%v  %v", got, cs.result)
			Expect(got).To(Equal(cs.result))
			fmt.Printf("%v  %v", got, cs.result)

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
		util.RolloutIDLabel:                 "1",
		apps.ControllerRevisionHashLabelKey: "version-1",
	}, noNeedRollbackReplicas, 0)
	return append(generatePodsWith(map[string]string{
		util.RolloutIDLabel:                 "1",
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
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{
					{
						Type:   corev1.PodReady,
						Status: corev1.ConditionTrue,
					},
				},
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
