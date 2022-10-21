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

package rollouthistory

import (
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ handler.EventHandler = &enqueueRequestForRolloutHistory{}

type enqueueRequestForRolloutHistory struct {
}

func (w *enqueueRequestForRolloutHistory) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	w.handleEvent(q, evt.Object)
}

func (w *enqueueRequestForRolloutHistory) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
}

func (w *enqueueRequestForRolloutHistory) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (w *enqueueRequestForRolloutHistory) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	w.handleEvent(q, evt.ObjectNew)
}

func (w *enqueueRequestForRolloutHistory) handleEvent(q workqueue.RateLimitingInterface, obj client.Object) {
	// In fact, rolloutHistory which is created by controller must have rolloutNameLabel and rolloutIDLabe
	rolloutName, ok1 := obj.(*rolloutv1alpha1.RolloutHistory).Labels[rolloutNameLabel]
	_, ok2 := obj.(*rolloutv1alpha1.RolloutHistory).Labels[rolloutIDLabel]
	if !ok1 || !ok2 {
		return
	}
	// add rollout which just creates a rolloutHistory to queue
	nsn := types.NamespacedName{Namespace: obj.GetNamespace(), Name: rolloutName}
	q.Add(reconcile.Request{NamespacedName: nsn})
}

var _ handler.EventHandler = &enqueueRequestForRollout{}

type enqueueRequestForRollout struct {
}

func (w *enqueueRequestForRollout) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	w.handleEvent(q, evt.Object)
}

func (w *enqueueRequestForRollout) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
}

func (w *enqueueRequestForRollout) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (w *enqueueRequestForRollout) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	w.handleEvent(q, evt.ObjectNew)
}

func (w *enqueueRequestForRollout) handleEvent(q workqueue.RateLimitingInterface, obj client.Object) {
	// RolloutID shouldn't be empty
	rollout := obj.(*rolloutv1alpha1.Rollout)
	if rollout.Status.CanaryStatus == nil || rollout.Status.CanaryStatus.ObservedRolloutID == "" {
		return
	}
	// add rollout with RolloutID to queue
	nsn := types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	q.Add(reconcile.Request{NamespacedName: nsn})
}
