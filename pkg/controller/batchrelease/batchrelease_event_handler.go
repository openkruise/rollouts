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
	"context"
	"encoding/json"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	kruiseappsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type EventAction string

const (
	CreateEventAction EventAction = "Create"
	DeleteEventAction EventAction = "Delete"
)

var (
	controllerKruiseKindCS  = kruiseappsv1alpha1.SchemeGroupVersion.WithKind("CloneSet")
	controllerKruiseKindSts = kruiseappsv1beta1.SchemeGroupVersion.WithKind("StatefulSet")
	controllerKindDep       = appsv1.SchemeGroupVersion.WithKind("Deployment")
	controllerKindSts       = appsv1.SchemeGroupVersion.WithKind("StatefulSet")
)

var _ handler.EventHandler = &workloadEventHandler{}
var _ handler.EventHandler = &podEventHandler{}

type podEventHandler struct {
	client.Reader
}

func (p podEventHandler) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
}
func (p podEventHandler) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}
func (p podEventHandler) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
}
func (p podEventHandler) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	oldPod := evt.ObjectOld.(*corev1.Pod)
	newPod := evt.ObjectNew.(*corev1.Pod)
	if oldPod.ResourceVersion != newPod.ResourceVersion ||
		(!IsRolloutIDChanged(oldPod, newPod) && util.IsPodReady(oldPod) == util.IsPodReady(newPod)) {
		return
	}

	klog.Infof("Pod %v ready condition changed, then enqueue", client.ObjectKeyFromObject(newPod))
	p.enqueue(newPod, q)
}

func IsRolloutIDChanged(oldPod, newPod *corev1.Pod) bool {
	if oldPod.Labels[util.RolloutIDLabel] != newPod.Labels[util.RolloutIDLabel] {
		return true
	}
	if oldPod.Labels[util.RolloutBatchIDLabel] != newPod.Labels[util.RolloutBatchIDLabel] {
		return true
	}
	return false
}

func (p podEventHandler) enqueue(pod *corev1.Pod, q workqueue.RateLimitingInterface) {
	owner := metav1.GetControllerOfNoCopy(pod)
	if owner == nil {
		return
	}
	workloadNamespacedName := types.NamespacedName{
		Name: owner.Name, Namespace: pod.Namespace,
	}
	workloadGVK := schema.FromAPIVersionAndKind(owner.APIVersion, owner.Kind)
	workloadObj := util.GetEmptyWorkloadObject(workloadGVK)
	err := p.Get(context.TODO(), workloadNamespacedName, workloadObj)
	if err != nil {
		return
	}

	controlInfo := workloadObj.GetAnnotations()[util.BatchReleaseControlAnnotation]
	brNsn, err := getBatchRelease(p.Reader, workloadNamespacedName, workloadGVK, controlInfo)
	if err != nil {
		klog.Errorf("unable to get BatchRelease related with %s (%s/%s), error: %v",
			workloadGVK.Kind, workloadNamespacedName.Namespace, workloadNamespacedName.Name, err)
		return
	}

	if len(brNsn.Name) != 0 {
		klog.V(3).Infof("Pod (%s/%s) ready condition changed, managed by BatchRelease (%v)",
			workloadNamespacedName.Namespace, workloadNamespacedName.Name, brNsn)
		q.Add(reconcile.Request{NamespacedName: brNsn})
	}
}

type workloadEventHandler struct {
	client.Reader
}

func (w workloadEventHandler) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	w.handleWorkload(q, evt.Object, CreateEventAction)
}

func (w workloadEventHandler) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	var gvk schema.GroupVersionKind
	switch evt.ObjectNew.(type) {
	case *appsv1.Deployment:
		gvk = controllerKindDep
	case *appsv1.StatefulSet:
		gvk = controllerKindSts
	case *kruiseappsv1alpha1.CloneSet:
		gvk = controllerKruiseKindCS
	case *kruiseappsv1beta1.StatefulSet:
		gvk = controllerKruiseKindSts
	case *unstructured.Unstructured:
		gvk = evt.ObjectNew.(*unstructured.Unstructured).GroupVersionKind()
	default:
		return
	}

	oldObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(evt.ObjectOld)
	if err != nil {
		return
	}
	newObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(evt.ObjectNew)
	if err != nil {
		return
	}

	objectKey := client.ObjectKeyFromObject(evt.ObjectNew)
	oldAccessor := util.ParseWorkloadInfo(&unstructured.Unstructured{Object: oldObject}, objectKey)
	newAccessor := util.ParseWorkloadInfo(&unstructured.Unstructured{Object: newObject}, objectKey)
	if newAccessor.Metadata.ResourceVersion == oldAccessor.Metadata.ResourceVersion {
		return
	}

	if observeGenerationChanged(newAccessor, oldAccessor) ||
		observeLatestGeneration(newAccessor, oldAccessor) ||
		observeScaleEventDone(newAccessor, oldAccessor) ||
		observeReplicasChanged(newAccessor, oldAccessor) {

		workloadNamespacedName := types.NamespacedName{
			Namespace: newAccessor.Metadata.Namespace,
			Name:      newAccessor.Metadata.Name,
		}

		brNsn, err := getBatchRelease(w.Reader, workloadNamespacedName, gvk, newAccessor.Metadata.Annotations[util.BatchReleaseControlAnnotation])
		if err != nil {
			klog.Errorf("unable to get BatchRelease related with %s (%s/%s), error: %v",
				gvk.Kind, workloadNamespacedName.Namespace, workloadNamespacedName.Name, err)
			return
		}

		if len(brNsn.Name) != 0 {
			klog.V(3).Infof("%s (%s/%s) changed generation from %d to %d managed by BatchRelease (%v)",
				gvk.Kind, workloadNamespacedName.Namespace, workloadNamespacedName.Name, oldAccessor.Metadata.Generation, newAccessor.Metadata.Generation, brNsn)
			q.Add(reconcile.Request{NamespacedName: brNsn})
		}
	}
}

func (w workloadEventHandler) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	w.handleWorkload(q, evt.Object, DeleteEventAction)
}

func (w workloadEventHandler) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (w *workloadEventHandler) handleWorkload(q workqueue.RateLimitingInterface, obj client.Object, action EventAction) {
	var gvk schema.GroupVersionKind
	switch obj.(type) {
	case *kruiseappsv1alpha1.CloneSet:
		gvk = controllerKruiseKindCS
	case *appsv1.Deployment:
		gvk = controllerKindDep
	case *appsv1.StatefulSet:
		gvk = controllerKindSts
	case *kruiseappsv1beta1.StatefulSet:
		gvk = controllerKruiseKindSts
	case *unstructured.Unstructured:
		gvk = obj.(*unstructured.Unstructured).GroupVersionKind()
	default:
		return
	}

	controlInfo := obj.GetAnnotations()[util.BatchReleaseControlAnnotation]
	workloadNamespacedName := types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
	brNsn, err := getBatchRelease(w.Reader, workloadNamespacedName, gvk, controlInfo)
	if err != nil {
		klog.Errorf("Unable to get BatchRelease related with %s (%s/%s), err: %v",
			gvk.Kind, workloadNamespacedName.Namespace, workloadNamespacedName.Name, err)
		return
	}
	if len(brNsn.Name) != 0 {
		klog.V(5).Infof("Something related %s %s (%s/%s) happen and will reconcile BatchRelease (%v)",
			action, gvk.Kind, workloadNamespacedName.Namespace, workloadNamespacedName.Name, brNsn)
		q.Add(reconcile.Request{NamespacedName: brNsn})
	}
}

func getBatchRelease(c client.Reader, workloadNamespaceName types.NamespacedName, gvk schema.GroupVersionKind, controlInfo string) (nsn types.NamespacedName, err error) {
	if len(controlInfo) > 0 {
		br := &metav1.OwnerReference{}
		err = json.Unmarshal([]byte(controlInfo), br)
		if err != nil {
			klog.Errorf("Failed to unmarshal controller info annotations for %v(%v)", gvk, workloadNamespaceName)
		}

		if br.APIVersion == v1alpha1.GroupVersion.String() && br.Kind == "BatchRelease" {
			klog.V(3).Infof("%s (%v) is managed by BatchRelease (%s), append queue and will reconcile BatchRelease", gvk.Kind, workloadNamespaceName, br.Name)
			nsn = types.NamespacedName{Namespace: workloadNamespaceName.Namespace, Name: br.Name}
			return
		}
	}

	brList := &v1alpha1.BatchReleaseList{}
	listOptions := &client.ListOptions{Namespace: workloadNamespaceName.Namespace}
	if err = c.List(context.TODO(), brList, listOptions); err != nil {
		klog.Errorf("List BatchRelease failed: %s", err.Error())
		return
	}

	for i := range brList.Items {
		br := &brList.Items[i]
		targetRef := br.Spec.TargetRef
		targetGV, err := schema.ParseGroupVersion(targetRef.WorkloadRef.APIVersion)
		if err != nil {
			klog.Errorf("Failed to parse targetRef's group version: %s for BatchRelease(%v)", targetRef.WorkloadRef.APIVersion, client.ObjectKeyFromObject(br))
			continue
		}

		if targetRef.WorkloadRef.Kind == gvk.Kind && targetGV.Group == gvk.Group && targetRef.WorkloadRef.Name == workloadNamespaceName.Name {
			nsn = client.ObjectKeyFromObject(br)
		}
	}

	return
}

func observeGenerationChanged(newOne, oldOne *util.WorkloadInfo) bool {
	return newOne.Metadata.Generation != oldOne.Metadata.Generation
}

func observeLatestGeneration(newOne, oldOne *util.WorkloadInfo) bool {
	oldNot := oldOne.Metadata.Generation != oldOne.Status.ObservedGeneration
	newDid := newOne.Metadata.Generation == newOne.Status.ObservedGeneration
	return oldNot && newDid
}

func observeScaleEventDone(newOne, oldOne *util.WorkloadInfo) bool {
	_, controlled := newOne.Metadata.Annotations[util.BatchReleaseControlAnnotation]
	if !controlled {
		return false
	}

	oldScaling := *oldOne.Replicas != *newOne.Replicas ||
		*oldOne.Replicas != oldOne.Status.Replicas
	newDone := newOne.Metadata.Generation == newOne.Status.ObservedGeneration &&
		*newOne.Replicas == newOne.Status.Replicas
	return oldScaling && newDone
}

func observeReplicasChanged(newOne, oldOne *util.WorkloadInfo) bool {
	_, controlled := newOne.Metadata.Annotations[util.BatchReleaseControlAnnotation]
	if !controlled {
		return false
	}

	return *oldOne.Replicas != *newOne.Replicas ||
		oldOne.Status.Replicas != newOne.Status.Replicas ||
		oldOne.Status.ReadyReplicas != newOne.Status.ReadyReplicas ||
		oldOne.Status.UpdatedReplicas != newOne.Status.UpdatedReplicas ||
		oldOne.Status.AvailableReplicas != newOne.Status.AvailableReplicas ||
		oldOne.Status.UpdatedReadyReplicas != newOne.Status.UpdatedReadyReplicas
}
