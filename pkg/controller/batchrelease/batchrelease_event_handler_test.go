package batchrelease

import (
	"encoding/json"
	"fmt"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestEventHandler_Update(t *testing.T) {
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
				oldObject.Spec.Replicas = pointer.Int32Ptr(1000)
				return oldObject
			},
			GetNewWorkload: func() client.Object {
				newObject := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				newObject.SetGeneration(2)
				newObject.Status.ObservedGeneration = 2
				newObject.Spec.Replicas = pointer.Int32Ptr(1000)
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

func TestEventHandler_Create(t *testing.T) {
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
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "Rollout",
					Name:       "whatever",
				})
				object.SetName("another")
				object.Annotations[workloads.BatchReleaseControlAnnotation] = string(controlInfo)
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

func TestEventHandler_Delete(t *testing.T) {
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
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "Rollout",
					Name:       "whatever",
				})
				object.SetName("another")
				object.Annotations[workloads.BatchReleaseControlAnnotation] = string(controlInfo)
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
