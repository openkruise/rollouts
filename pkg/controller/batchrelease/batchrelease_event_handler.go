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
	"reflect"

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	kruiseappsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	expectations "github.com/openkruise/rollouts/pkg/util/expectation"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

var _ handler.EventHandler = &workloadEventHandler{}
var _ handler.EventHandler = &podEventHandler{}

type podEventHandler struct {
	client.Reader
}

func (p podEventHandler) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	pod, ok := evt.Object.(*corev1.Pod)
	if !ok {
		return
	}
	p.enqueue(pod, q)
}

func (p podEventHandler) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (p podEventHandler) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
}

func (p podEventHandler) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	oldPod, oldOK := evt.ObjectOld.(*corev1.Pod)
	newPod, newOK := evt.ObjectNew.(*corev1.Pod)
	if !oldOK || !newOK {
		return
	}
	if oldPod.ResourceVersion == newPod.ResourceVersion || (util.IsEqualRevision(oldPod, newPod) && util.IsPodReady(oldPod) == util.IsPodReady(newPod)) {
		return
	}

	klog.Infof("Pod %v ready condition changed, then enqueue", client.ObjectKeyFromObject(newPod))
	p.enqueue(newPod, q)
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
	workloadObj, err := util.GetOwnerWorkload(p.Reader, pod)
	if err != nil || workloadObj == nil {
		//klog.Errorf("Failed to get owner workload for pod %v, err: %v", client.ObjectKeyFromObject(pod), err)
		return
	}

	controlInfo, ok := workloadObj.GetAnnotations()[util.BatchReleaseControlAnnotation]
	// only consider enqueue during rollout progressing
	if !ok || controlInfo == "" {
		return
	}
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
	expectationObserved(evt.Object)
	w.handleWorkload(q, evt.Object, CreateEventAction)
}

func (w workloadEventHandler) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	var gvk schema.GroupVersionKind
	switch obj := evt.ObjectNew.(type) {
	case *kruiseappsv1alpha1.CloneSet:
		gvk = util.ControllerKruiseKindCS
	case *kruiseappsv1alpha1.DaemonSet:
		gvk = util.ControllerKruiseKindDS
	case *appsv1.Deployment:
		gvk = util.ControllerKindDep
	case *appsv1.StatefulSet:
		gvk = util.ControllerKindSts
	case *kruiseappsv1beta1.StatefulSet:
		gvk = util.ControllerKruiseKindSts
	case *unstructured.Unstructured:
		gvk = obj.GroupVersionKind()
	default:
		return
	}

	newObject := evt.ObjectNew
	oldObject := evt.ObjectOld
	expectationObserved(newObject)
	if newObject.GetResourceVersion() == oldObject.GetResourceVersion() {
		return
	}

	oldStatus := util.ParseWorkloadStatus(oldObject)
	newStatus := util.ParseWorkloadStatus(newObject)
	if oldObject.GetGeneration() != newObject.GetGeneration() || !reflect.DeepEqual(oldStatus, newStatus) {
		workloadNamespacedName := client.ObjectKeyFromObject(newObject)
		controllerInfo := newObject.GetAnnotations()[util.BatchReleaseControlAnnotation]
		brNsn, err := getBatchRelease(w.Reader, workloadNamespacedName, gvk, controllerInfo)
		if err != nil {
			klog.Errorf("unable to get BatchRelease related with %s (%s/%s), error: %v",
				gvk.Kind, workloadNamespacedName.Namespace, workloadNamespacedName.Name, err)
			return
		}

		if len(brNsn.Name) != 0 {
			klog.V(3).Infof("%s (%s/%s) changed generation from %d to %d managed by BatchRelease (%v)",
				gvk.Kind, workloadNamespacedName.Namespace, workloadNamespacedName.Name, oldObject.GetGeneration(), newObject.GetGeneration(), brNsn)
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
	switch o := obj.(type) {
	case *kruiseappsv1alpha1.CloneSet:
		gvk = util.ControllerKruiseKindCS
	case *kruiseappsv1alpha1.DaemonSet:
		gvk = util.ControllerKruiseKindDS
	case *appsv1.Deployment:
		gvk = util.ControllerKindDep
	case *appsv1.StatefulSet:
		gvk = util.ControllerKindSts
	case *kruiseappsv1beta1.StatefulSet:
		gvk = util.ControllerKruiseKindSts
	case *unstructured.Unstructured:
		gvk = o.GroupVersionKind()
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
		klog.V(3).Infof("Something related %s %s (%s/%s) happen and will reconcile BatchRelease (%v)",
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

		if br.APIVersion == v1beta1.GroupVersion.String() && br.Kind == "BatchRelease" {
			klog.V(3).Infof("%s (%v) is managed by BatchRelease (%s), append queue and will reconcile BatchRelease", gvk.Kind, workloadNamespaceName, br.Name)
			nsn = types.NamespacedName{Namespace: workloadNamespaceName.Namespace, Name: br.Name}
			return
		}
	}

	brList := &v1beta1.BatchReleaseList{}
	namespace := workloadNamespaceName.Namespace
	if err = c.List(context.TODO(), brList, client.InNamespace(namespace), utilclient.DisableDeepCopy); err != nil {
		klog.Errorf("List BatchRelease failed: %s", err.Error())
		return
	}

	for i := range brList.Items {
		br := &brList.Items[i]
		targetRef := br.Spec.WorkloadRef
		targetGV, err := schema.ParseGroupVersion(targetRef.APIVersion)
		if err != nil {
			klog.Errorf("Failed to parse targetRef's group version: %s for BatchRelease(%v)", targetRef.APIVersion, client.ObjectKeyFromObject(br))
			continue
		}

		if targetRef.Kind == gvk.Kind && targetGV.Group == gvk.Group && targetRef.Name == workloadNamespaceName.Name {
			nsn = client.ObjectKeyFromObject(br)
		}
	}

	return
}

func expectationObserved(object client.Object) {
	controllerKey := getControllerKey(object)
	if controllerKey != nil {
		klog.V(3).Infof("observed %v, remove from expectation %s: %s",
			klog.KObj(object), *controllerKey, string(object.GetUID()))
		expectations.ResourceExpectations.Observe(*controllerKey, expectations.Create, string(object.GetUID()))
	}
}

func getControllerKey(object client.Object) *string {
	owner := metav1.GetControllerOfNoCopy(object)
	if owner == nil {
		return nil
	}
	if owner.Kind == "BatchRelease" {
		key := types.NamespacedName{Namespace: object.GetNamespace(), Name: owner.Name}.String()
		return &key
	}
	return nil
}
