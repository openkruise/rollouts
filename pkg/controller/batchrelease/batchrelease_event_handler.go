/*
Copyright 2021 The Kruise Authors.

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
	"context"
	"encoding/json"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/workloads"
)

type EventAction string

const (
	CreateEventAction EventAction = "Create"
	DeleteEventAction EventAction = "Delete"
)

var (
	controllerKruiseKindCS = kruiseappsv1alpha1.SchemeGroupVersion.WithKind("CloneSet")
	controllerKindDep      = appsv1.SchemeGroupVersion.WithKind("Deployment")
)

var _ handler.EventHandler = &workloadEventHandler{}

type workloadEventHandler struct {
	client.Reader
}

func (w workloadEventHandler) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	w.handleWorkload(q, evt.Object, CreateEventAction)
}

func (w workloadEventHandler) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	var oldAccessor, newAccessor *workloads.Accessor
	var gvk schema.GroupVersionKind

	switch evt.ObjectNew.(type) {
	case *kruiseappsv1alpha1.CloneSet:
		gvk = controllerKruiseKindCS
		oldClone := evt.ObjectOld.(*kruiseappsv1alpha1.CloneSet)
		newClone := evt.ObjectNew.(*kruiseappsv1alpha1.CloneSet)

		var oldReplicas, newReplicas int32
		if oldClone.Spec.Replicas != nil {
			oldReplicas = *oldClone.Spec.Replicas
		}
		if newClone.Spec.Replicas != nil {
			newReplicas = *newClone.Spec.Replicas
		}

		oldAccessor = &workloads.Accessor{
			Replicas: &oldReplicas,
			Paused:   oldClone.Spec.UpdateStrategy.Paused,
			Status: &workloads.Status{
				Replicas:             oldClone.Status.Replicas,
				ReadyReplicas:        oldClone.Status.ReadyReplicas,
				UpdatedReplicas:      oldClone.Status.UpdatedReplicas,
				UpdatedReadyReplicas: oldClone.Status.UpdatedReadyReplicas,
				ObservedGeneration:   oldClone.Status.ObservedGeneration,
			},
			Metadata: &oldClone.ObjectMeta,
		}

		newAccessor = &workloads.Accessor{
			Replicas: &newReplicas,
			Paused:   newClone.Spec.UpdateStrategy.Paused,
			Status: &workloads.Status{
				Replicas:             newClone.Status.Replicas,
				ReadyReplicas:        newClone.Status.ReadyReplicas,
				UpdatedReplicas:      newClone.Status.UpdatedReplicas,
				UpdatedReadyReplicas: newClone.Status.UpdatedReadyReplicas,
				ObservedGeneration:   newClone.Status.ObservedGeneration,
			},
			Metadata: &newClone.ObjectMeta,
		}

	case *appsv1.Deployment:
		gvk = controllerKindDep
		oldDeploy := evt.ObjectOld.(*appsv1.Deployment)
		newDeploy := evt.ObjectNew.(*appsv1.Deployment)

		var oldReplicas, newReplicas int32
		if oldDeploy.Spec.Replicas != nil {
			oldReplicas = *oldDeploy.Spec.Replicas
		}
		if newDeploy.Spec.Replicas != nil {
			newReplicas = *newDeploy.Spec.Replicas
		}

		oldAccessor = &workloads.Accessor{
			Replicas: &oldReplicas,
			Paused:   oldDeploy.Spec.Paused,
			Status: &workloads.Status{
				Replicas:           oldDeploy.Status.Replicas,
				ReadyReplicas:      oldDeploy.Status.AvailableReplicas,
				UpdatedReplicas:    oldDeploy.Status.UpdatedReplicas,
				ObservedGeneration: oldDeploy.Status.ObservedGeneration,
			},
			Metadata: &oldDeploy.ObjectMeta,
		}

		newAccessor = &workloads.Accessor{
			Replicas: &newReplicas,
			Paused:   newDeploy.Spec.Paused,
			Status: &workloads.Status{
				Replicas:           newDeploy.Status.Replicas,
				ReadyReplicas:      newDeploy.Status.AvailableReplicas,
				UpdatedReplicas:    newDeploy.Status.UpdatedReplicas,
				ObservedGeneration: newDeploy.Status.ObservedGeneration,
			},
			Metadata: &newDeploy.ObjectMeta,
		}

	default:
		return
	}

	if observeGenerationChanged(newAccessor, oldAccessor) ||
		observeLatestGeneration(newAccessor, oldAccessor) ||
		observeScaleEventDone(newAccessor, oldAccessor) ||
		observeReplicasChanged(newAccessor, oldAccessor) {

		workloadNsn := types.NamespacedName{
			Namespace: newAccessor.Metadata.Namespace,
			Name:      newAccessor.Metadata.Name,
		}

		controllerInfo, controlled := newAccessor.Metadata.Annotations[workloads.BatchReleaseControlAnnotation]
		if controlled && len(controllerInfo) > 0 {
			br := &metav1.OwnerReference{}
			if err := json.Unmarshal([]byte(controllerInfo), br); err == nil {
				klog.V(3).Infof("%s (%v) is managed by BatchRelease (%s), append queue", gvk.Kind, workloadNsn, br.Name)
				nsn := types.NamespacedName{Namespace: workloadNsn.Namespace, Name: br.Name}
				q.Add(reconcile.Request{NamespacedName: nsn})
				return
			}
		}

		br, err := w.getBatchRelease(workloadNsn, gvk)
		if err != nil {
			klog.Errorf("unable to get BatchRelease related with %s (%s/%s), err: %v",
				gvk.Kind, workloadNsn.Namespace, workloadNsn.Name, err)
			return
		}

		if br != nil {
			klog.V(3).Infof("%s (%s/%s) changed generation from %d to %d managed by BatchRelease (%s/%s)",
				gvk.Kind, workloadNsn.Namespace, workloadNsn.Name, oldAccessor.Metadata.Generation, newAccessor.Metadata.Generation, br.GetNamespace(), br.GetName())
			nsn := types.NamespacedName{Namespace: br.GetNamespace(), Name: br.GetName()}
			q.Add(reconcile.Request{NamespacedName: nsn})
		}
	}
}

func (w workloadEventHandler) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	w.handleWorkload(q, evt.Object, DeleteEventAction)
}

func (w workloadEventHandler) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (w *workloadEventHandler) handleWorkload(q workqueue.RateLimitingInterface,
	obj client.Object, action EventAction) {
	var gvk schema.GroupVersionKind
	switch obj.(type) {
	case *kruiseappsv1alpha1.CloneSet:
		gvk = controllerKruiseKindCS
	case *appsv1.Deployment:
		gvk = controllerKindDep
	default:
		return
	}

	workloadNsn := types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
	ws, err := w.getBatchRelease(workloadNsn, gvk)
	if err != nil {
		klog.Errorf("unable to get BatchRelease related with %s (%s/%s), err: %v",
			gvk.Kind, workloadNsn.Namespace, workloadNsn.Name, err)
		return
	}
	if ws != nil {
		klog.V(5).Infof("%s %s (%s/%s) and reconcile BatchRelease (%s/%s)",
			action, gvk.Kind, workloadNsn.Namespace, workloadNsn.Namespace, ws.Namespace, ws.Name)
		nsn := types.NamespacedName{Namespace: ws.GetNamespace(), Name: ws.GetName()}
		q.Add(reconcile.Request{NamespacedName: nsn})
	}
}

func (w *workloadEventHandler) getBatchRelease(workloadNamespaceName types.NamespacedName, gvk schema.GroupVersionKind) (*v1alpha1.BatchRelease, error) {
	bsList := &v1alpha1.BatchReleaseList{}
	listOptions := &client.ListOptions{Namespace: workloadNamespaceName.Namespace}
	if err := w.List(context.TODO(), bsList, listOptions); err != nil {
		klog.Errorf("List BatchRelease failed: %s", err.Error())
		return nil, err
	}

	for _, bs := range bsList.Items {
		if bs.DeletionTimestamp != nil {
			continue
		}

		targetRef := bs.Spec.TargetRef
		targetGV, err := schema.ParseGroupVersion(targetRef.APIVersion)
		if err != nil {
			klog.Errorf("failed to parse targetRef's group version: %s", targetRef.APIVersion)
			continue
		}

		if targetRef.Kind == gvk.Kind && targetGV.Group == gvk.Group && targetRef.Name == workloadNamespaceName.Name {
			return &bs, nil
		}
	}

	return nil, nil
}

func observeGenerationChanged(newOne, oldOne *workloads.Accessor) bool {
	return newOne.Metadata.Generation != oldOne.Metadata.Generation
}

func observeLatestGeneration(newOne, oldOne *workloads.Accessor) bool {
	oldNot := oldOne.Metadata.Generation != oldOne.Status.ObservedGeneration
	newDid := newOne.Metadata.Generation == newOne.Status.ObservedGeneration
	return oldNot && newDid
}

func observeScaleEventDone(newOne, oldOne *workloads.Accessor) bool {
	_, controlled := newOne.Metadata.Annotations[workloads.BatchReleaseControlAnnotation]
	if !controlled {
		return false
	}

	oldScaling := oldOne.Replicas != newOne.Replicas ||
		*oldOne.Replicas != oldOne.Status.Replicas
	newDone := newOne.Metadata.Generation == newOne.Status.ObservedGeneration &&
		*oldOne.Replicas == oldOne.Status.Replicas
	return oldScaling && newDone
}

func observeReplicasChanged(newOne, oldOne *workloads.Accessor) bool {
	_, controlled := newOne.Metadata.Annotations[workloads.BatchReleaseControlAnnotation]
	if !controlled {
		return false
	}

	return oldOne.Status.Replicas != newOne.Status.Replicas ||
		oldOne.Status.ReadyReplicas != newOne.Status.ReadyReplicas ||
		oldOne.Status.UpdatedReplicas != newOne.Status.UpdatedReplicas ||
		oldOne.Status.UpdatedReadyReplicas != newOne.Status.UpdatedReadyReplicas
}
