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

package context

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestIsBatchReady(t *testing.T) {
	RegisterFailHandler(Fail)

	p := func(f intstr.IntOrString) *intstr.IntOrString {
		return &f
	}
	r := func(f *intstr.IntOrString, id, revision string) *v1beta1.BatchRelease {
		return &v1beta1.BatchRelease{
			Spec:   v1beta1.BatchReleaseSpec{ReleasePlan: v1beta1.ReleasePlan{RolloutID: id, FailureThreshold: f}},
			Status: v1beta1.BatchReleaseStatus{UpdateRevision: revision},
		}
	}
	cases := map[string]struct {
		release        *v1beta1.BatchRelease
		pods           []*corev1.Pod
		maxUnavailable *intstr.IntOrString
		labelDesired   int32
		desired        int32
		updated        int32
		updatedReady   int32
		isReady        bool
	}{
		"ready: no-rollout-id, all pod ready": {
			release:      r(p(intstr.FromInt(1)), "", "v2"),
			pods:         nil,
			labelDesired: 5,
			desired:      5,
			updated:      5,
			updatedReady: 5,
			isReady:      true,
		},
		"ready: no-rollout-id, tolerated failed pods": {
			release:      r(p(intstr.FromInt(1)), "", "v2"),
			pods:         nil,
			labelDesired: 5,
			desired:      5,
			updated:      5,
			updatedReady: 4,
			isReady:      true,
		},
		"false: no-rollout-id, un-tolerated failed pods": {
			release:      r(p(intstr.FromInt(1)), "", "v2"),
			pods:         nil,
			labelDesired: 5,
			desired:      5,
			updated:      5,
			updatedReady: 3,
			isReady:      false,
		},
		"false: no-rollout-id, tolerated failed pods, but 1 pod isn't updated": {
			release:      r(p(intstr.FromString("60%")), "", "v2"),
			pods:         nil,
			labelDesired: 5,
			desired:      5,
			updated:      4,
			updatedReady: 4,
			isReady:      false,
		},
		"false: no-rollout-id, tolerated, but no-pod-ready": {
			release:      r(p(intstr.FromInt(100)), "", "v2"),
			pods:         nil,
			labelDesired: 5,
			desired:      5,
			updated:      5,
			updatedReady: 0,
			isReady:      false,
		},
		"false: no-rollout-id, un-tolerated failed pods, failureThreshold=nil": {
			release:      r(nil, "", "v2"),
			pods:         nil,
			labelDesired: 5,
			desired:      5,
			updated:      5,
			updatedReady: 3,
			isReady:      false,
		},
		"true: rollout-id, labeled pods satisfied": {
			release:      r(p(intstr.FromInt(1)), "1", "version-1"),
			pods:         generatePods(5, 0),
			labelDesired: 5,
			desired:      5,
			updated:      5,
			updatedReady: 5,
			isReady:      true,
		},
		"false: rollout-id, labeled pods not satisfied": {
			release:      r(p(intstr.FromInt(1)), "1", "version-1"),
			pods:         generatePods(3, 0),
			labelDesired: 5,
			desired:      5,
			updated:      5,
			updatedReady: 5,
			isReady:      false,
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := BatchContext{
				Pods:                   cs.pods,
				PlannedUpdatedReplicas: cs.labelDesired,
				DesiredUpdatedReplicas: cs.desired,
				UpdatedReplicas:        cs.updated,
				UpdatedReadyReplicas:   cs.updatedReady,
				UpdateRevision:         cs.release.Status.UpdateRevision,
				RolloutID:              cs.release.Spec.ReleasePlan.RolloutID,
				FailureThreshold:       cs.release.Spec.ReleasePlan.FailureThreshold,
			}
			err := ctx.IsBatchReady()
			if cs.isReady {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		})
	}
}

func generatePods(updatedReplicas, noNeedRollbackReplicas int) []*corev1.Pod {
	podsNoNeed := generatePodsWith(map[string]string{
		util.NoNeedUpdatePodLabel:           "0x1",
		v1beta1.RolloutIDLabel:              "1",
		apps.ControllerRevisionHashLabelKey: "version-1",
	}, noNeedRollbackReplicas, 0)
	return append(generatePodsWith(map[string]string{
		v1beta1.RolloutIDLabel:              "1",
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
