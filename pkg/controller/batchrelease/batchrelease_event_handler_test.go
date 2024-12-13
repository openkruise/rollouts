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

package batchrelease

import (
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestWorkloadEventHandler_Update(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name             string
		GetOldWorkload   func() client.Object
		GetNewWorkload   func() client.Object
		ExpectedQueueLen int
	}{
		{
			Name: "Deployment Batch NotReady -> Ready",
			GetOldWorkload: func() client.Object {
				return getCanaryWithStage(stableDeploy, "v2", 0, false)
			},
			GetNewWorkload: func() client.Object {
				return getCanaryWithStage(stableDeploy, "v2", 0, true)
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "Deployment Generation 1 -> 2",
			GetOldWorkload: func() client.Object {
				oldObject := getStableWithReady(stableDeploy, "v2")
				oldObject.SetGeneration(1)
				return oldObject
			},
			GetNewWorkload: func() client.Object {
				newObject := getStableWithReady(stableDeploy, "v2")
				newObject.SetGeneration(2)
				return newObject
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "Deployment watched the latest generation",
			GetOldWorkload: func() client.Object {
				oldObject := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				oldObject.SetGeneration(2)
				oldObject.Status.ObservedGeneration = 1
				return oldObject
			},
			GetNewWorkload: func() client.Object {
				newObject := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				newObject.SetGeneration(2)
				newObject.Status.ObservedGeneration = 2
				return newObject
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "Deployment scaling done",
			GetOldWorkload: func() client.Object {
				oldObject := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				oldObject.SetGeneration(2)
				oldObject.Status.ObservedGeneration = 2
				oldObject.Spec.Replicas = pointer.Int32(1000)
				return oldObject
			},
			GetNewWorkload: func() client.Object {
				newObject := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				newObject.SetGeneration(2)
				newObject.Status.ObservedGeneration = 2
				newObject.Spec.Replicas = pointer.Int32(1000)
				newObject.Status.Replicas = 1000
				return newObject
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "Deployment available pod changed",
			GetOldWorkload: func() client.Object {
				oldObject := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				oldObject.SetGeneration(2)
				oldObject.Status.ObservedGeneration = 2
				oldObject.Status.AvailableReplicas = 20
				return oldObject
			},
			GetNewWorkload: func() client.Object {
				newObject := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				newObject.SetGeneration(2)
				newObject.Status.ObservedGeneration = 2
				newObject.Status.AvailableReplicas = 50
				return newObject
			},
			ExpectedQueueLen: 1,
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			oldObject := cs.GetOldWorkload()
			newObject := cs.GetNewWorkload()
			newSJk := scheme
			fmt.Println(newSJk)
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(releaseDeploy.DeepCopy()).Build()
			handler := workloadEventHandler{Reader: cli}
			updateQ := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			updateEvt := event.UpdateEvent{
				ObjectOld: oldObject,
				ObjectNew: newObject,
			}
			handler.Update(updateEvt, updateQ)
			Expect(updateQ.Len()).Should(Equal(cs.ExpectedQueueLen))
		})
	}
}

func TestWorkloadEventHandler_Create(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name             string
		GetNewWorkload   func() client.Object
		ExpectedQueueLen int
	}{
		{
			Name: "Deployment with controller info annotation",
			GetNewWorkload: func() client.Object {
				return getCanaryWithStage(stableDeploy, "v2", 0, true)
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "Deployment without controller info annotation",
			GetNewWorkload: func() client.Object {
				object := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				object.Annotations = nil
				return object
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "Deployment with wrong controller info annotation",
			GetNewWorkload: func() client.Object {
				object := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				controlInfo, _ := json.Marshal(&metav1.OwnerReference{
					APIVersion: v1beta1.GroupVersion.String(),
					Kind:       "Rollout",
					Name:       "whatever",
				})
				object.SetName("another")
				object.Annotations[util.BatchReleaseControlAnnotation] = string(controlInfo)
				return object
			},
			ExpectedQueueLen: 0,
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			newObject := cs.GetNewWorkload()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(releaseDeploy.DeepCopy()).Build()
			handler := workloadEventHandler{Reader: cli}
			createQ := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			createEvt := event.CreateEvent{
				Object: newObject,
			}
			handler.Create(createEvt, createQ)
			Expect(createQ.Len()).Should(Equal(cs.ExpectedQueueLen))
		})
	}
}

func TestWorkloadEventHandler_Delete(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name             string
		GetNewWorkload   func() client.Object
		ExpectedQueueLen int
	}{
		{
			Name: "Deployment with controller info annotation",
			GetNewWorkload: func() client.Object {
				return getCanaryWithStage(stableDeploy, "v2", 0, true)
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "Deployment without controller info annotation",
			GetNewWorkload: func() client.Object {
				object := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				object.Annotations = nil
				return object
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "Deployment with wrong controller info annotation",
			GetNewWorkload: func() client.Object {
				object := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				controlInfo, _ := json.Marshal(&metav1.OwnerReference{
					APIVersion: v1beta1.GroupVersion.String(),
					Kind:       "Rollout",
					Name:       "whatever",
				})
				object.SetName("another")
				object.Annotations[util.BatchReleaseControlAnnotation] = string(controlInfo)
				return object
			},
			ExpectedQueueLen: 0,
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			newObject := cs.GetNewWorkload()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(releaseDeploy.DeepCopy()).Build()
			handler := workloadEventHandler{Reader: cli}
			deleteQ := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			deleteEvt := event.DeleteEvent{
				Object: newObject,
			}
			handler.Delete(deleteEvt, deleteQ)
			Expect(deleteQ.Len()).Should(Equal(cs.ExpectedQueueLen))
		})
	}
}

func TestPodEventHandler_Update(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name             string
		GetOldPod        func() client.Object
		GetNewPod        func() client.Object
		GetWorkload      func() client.Object
		ExpectedQueueLen int
	}{
		{
			Name: "CloneSet Pod NotReady -> Ready",
			GetOldPod: func() client.Object {
				return generatePod(false, true, "version-1")
			},
			GetNewPod: func() client.Object {
				return generatePod(true, true, "version-1")
			},
			GetWorkload: func() client.Object {
				clone := stableClone.DeepCopy()
				owner, _ := json.Marshal(metav1.NewControllerRef(releaseClone, releaseClone.GetObjectKind().GroupVersionKind()))
				clone.Annotations = map[string]string{
					util.BatchReleaseControlAnnotation: string(owner),
				}
				return clone
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "CloneSet Pod Ready -> Ready",
			GetOldPod: func() client.Object {
				return generatePod(true, true, "version-1")
			},
			GetNewPod: func() client.Object {
				return generatePod(true, true, "version-1")
			},
			GetWorkload: func() client.Object {
				clone := stableClone.DeepCopy()
				owner, _ := json.Marshal(metav1.NewControllerRef(releaseClone, releaseClone.GetObjectKind().GroupVersionKind()))
				clone.Annotations = map[string]string{
					util.BatchReleaseControlAnnotation: string(owner),
				}
				return clone
			},
			ExpectedQueueLen: 0,
		},
		{
			Name: "Orphan Pod NotReady -> Ready",
			GetOldPod: func() client.Object {
				return generatePod(false, false, "version-1")
			},
			GetNewPod: func() client.Object {
				return generatePod(true, false, "version-1")
			},
			GetWorkload: func() client.Object {
				clone := stableClone.DeepCopy()
				owner, _ := json.Marshal(metav1.NewControllerRef(releaseClone, releaseClone.GetObjectKind().GroupVersionKind()))
				clone.Annotations = map[string]string{
					util.BatchReleaseControlAnnotation: string(owner),
				}
				return clone
			},
			ExpectedQueueLen: 0,
		},
		{
			Name: "Orphan Pod Ready -> Ready",
			GetOldPod: func() client.Object {
				return generatePod(true, false, "version-1")
			},
			GetNewPod: func() client.Object {
				return generatePod(true, false, "version-1")
			},
			GetWorkload: func() client.Object {
				clone := stableClone.DeepCopy()
				owner, _ := json.Marshal(metav1.NewControllerRef(releaseClone, releaseClone.GetObjectKind().GroupVersionKind()))
				clone.Annotations = map[string]string{
					util.BatchReleaseControlAnnotation: string(owner),
				}
				return clone
			},
			ExpectedQueueLen: 0,
		},
		{
			Name: "Free CloneSet Pod NotReady -> Ready",
			GetOldPod: func() client.Object {
				return generatePod(false, false, "version-1")
			},
			GetNewPod: func() client.Object {
				return generatePod(true, false, "version-1")
			},
			GetWorkload: func() client.Object {
				clone := stableClone.DeepCopy()
				return clone
			},
			ExpectedQueueLen: 0,
		},
		{
			Name: "Free CloneSet Pod Ready -> Ready",
			GetOldPod: func() client.Object {
				return generatePod(true, false, "version-1")
			},
			GetNewPod: func() client.Object {
				return generatePod(true, false, "version-1")
			},
			GetWorkload: func() client.Object {
				clone := stableClone.DeepCopy()
				return clone
			},
			ExpectedQueueLen: 0,
		},
		{
			Name: "CloneSet Pod V1 -> V2",
			GetOldPod: func() client.Object {
				return generatePod(true, true, "version-1")
			},
			GetNewPod: func() client.Object {
				return generatePod(true, true, "version-2")
			},
			GetWorkload: func() client.Object {
				clone := stableClone.DeepCopy()
				owner, _ := json.Marshal(metav1.NewControllerRef(releaseClone, releaseClone.GetObjectKind().GroupVersionKind()))
				clone.Annotations = map[string]string{
					util.BatchReleaseControlAnnotation: string(owner),
				}
				return clone
			},
			ExpectedQueueLen: 1,
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			oldObject := cs.GetOldPod()
			newObject := cs.GetNewPod()
			workload := cs.GetWorkload()
			newSJk := scheme
			fmt.Println(newSJk)
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(releaseDeploy.DeepCopy(), workload).Build()
			handler := podEventHandler{Reader: cli}
			updateQ := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			updateEvt := event.UpdateEvent{
				ObjectOld: oldObject,
				ObjectNew: newObject,
			}
			handler.Update(updateEvt, updateQ)
			Expect(updateQ.Len()).Should(Equal(cs.ExpectedQueueLen))
		})
	}
}

func TestPodEventHandler_Create(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name             string
		GetNewPod        func() client.Object
		GetWorkload      func() client.Object
		ExpectedQueueLen int
	}{
		{
			Name: "CloneSet Pod",
			GetNewPod: func() client.Object {
				return generatePod(false, true, "version-1")
			},
			GetWorkload: func() client.Object {
				clone := stableClone.DeepCopy()
				owner, _ := json.Marshal(metav1.NewControllerRef(releaseClone, releaseClone.GetObjectKind().GroupVersionKind()))
				clone.Annotations = map[string]string{
					util.BatchReleaseControlAnnotation: string(owner),
				}
				return clone
			},
			ExpectedQueueLen: 1,
		},
		{
			Name: "Orphan Pod",
			GetNewPod: func() client.Object {
				return generatePod(false, false, "version-1")
			},
			GetWorkload: func() client.Object {
				clone := stableClone.DeepCopy()
				owner, _ := json.Marshal(metav1.NewControllerRef(releaseClone, releaseClone.GetObjectKind().GroupVersionKind()))
				clone.Annotations = map[string]string{
					util.BatchReleaseControlAnnotation: string(owner),
				}
				return clone
			},
			ExpectedQueueLen: 0,
		},
		{
			Name: "Free CloneSet Pod",
			GetNewPod: func() client.Object {
				return generatePod(false, true, "version-1")
			},
			GetWorkload: func() client.Object {
				return stableClone.DeepCopy()
			},
			ExpectedQueueLen: 0,
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			newObject := cs.GetNewPod()
			workload := cs.GetWorkload()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(releaseDeploy.DeepCopy(), workload).Build()
			handler := podEventHandler{Reader: cli}
			createQ := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			createEvt := event.CreateEvent{
				Object: newObject,
			}
			handler.Create(createEvt, createQ)
			Expect(createQ.Len()).Should(Equal(cs.ExpectedQueueLen))
		})
	}
}

func generatePod(ready, owned bool, version string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "pod",
			Namespace:       stableClone.Namespace,
			ResourceVersion: string(uuid.NewUUID()),
			Labels: map[string]string{
				apps.ControllerRevisionHashLabelKey: version,
			},
		},
	}
	if ready {
		pod.Status.Phase = corev1.PodRunning
		pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		})
	}
	if owned {
		pod.OwnerReferences = append(pod.OwnerReferences,
			*metav1.NewControllerRef(stableClone, stableClone.GroupVersionKind()))
	}
	return pod
}
