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

package rollout

import (
	"context"

	rolloutv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
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

var _ handler.EventHandler = &enqueueRequestForWorkload{}

type enqueueRequestForWorkload struct {
	reader client.Reader
	scheme *runtime.Scheme
}

func (w *enqueueRequestForWorkload) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	w.handleEvent(q, evt.Object)
}

func (w *enqueueRequestForWorkload) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	w.handleEvent(q, evt.Object)
}

func (w *enqueueRequestForWorkload) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (w *enqueueRequestForWorkload) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	w.handleEvent(q, evt.ObjectNew)
}

func (w *enqueueRequestForWorkload) handleEvent(q workqueue.RateLimitingInterface, obj client.Object) {
	key := types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}
	kinds, _, err := w.scheme.ObjectKinds(obj)
	if err != nil {
		klog.Errorf("scheme ObjectKinds key(%s) failed: %s", key.String(), err.Error())
		return
	}
	gvk := kinds[0]
	rollout, err := w.getRolloutForWorkload(key, gvk)
	if err != nil {
		klog.Errorf("unable to get Rollout related with %s (%s/%s), err: %v", gvk.Kind, key.Namespace, key.Name, err)
		return
	}
	if rollout != nil {
		klog.Infof("workload(%s/%s) and reconcile Rollout (%s/%s)", key.Namespace, key.Name, rollout.Namespace, rollout.Name)
		nsn := types.NamespacedName{Namespace: rollout.GetNamespace(), Name: rollout.GetName()}
		q.Add(reconcile.Request{NamespacedName: nsn})
	}
}

func (w *enqueueRequestForWorkload) getRolloutForWorkload(key types.NamespacedName, gvk schema.GroupVersionKind) (*rolloutv1beta1.Rollout, error) {
	rList := &rolloutv1beta1.RolloutList{}
	if err := w.reader.List(context.TODO(), rList, client.InNamespace(key.Namespace), utilclient.DisableDeepCopy); err != nil {
		klog.Errorf("List WorkloadSpread failed: %s", err.Error())
		return nil, err
	}

	for _, rollout := range rList.Items {
		targetRef := rollout.Spec.WorkloadRef
		targetGV, err := schema.ParseGroupVersion(targetRef.APIVersion)
		if err != nil {
			klog.Errorf("failed to parse rollout(%s/%s) targetRef's group version: %s", rollout.Namespace, rollout.Name, targetRef.APIVersion)
			continue
		}

		if targetRef.Kind == gvk.Kind && targetGV.Group == gvk.Group && targetRef.Name == key.Name {
			return &rollout, nil
		}
	}

	return nil, nil
}

var _ handler.EventHandler = &enqueueRequestForBatchRelease{}

type enqueueRequestForBatchRelease struct {
	reader client.Reader
}

func (w *enqueueRequestForBatchRelease) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
}

func (w *enqueueRequestForBatchRelease) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
}

func (w *enqueueRequestForBatchRelease) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (w *enqueueRequestForBatchRelease) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	w.handleEvent(q, evt.ObjectNew)
}

func (w *enqueueRequestForBatchRelease) handleEvent(q workqueue.RateLimitingInterface, obj client.Object) {
	klog.Infof("BatchRelease(%s/%s) and reconcile Rollout (%s)", obj.GetNamespace(), obj.GetName(), obj.GetName())
	nsn := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	q.Add(reconcile.Request{NamespacedName: nsn})
}
