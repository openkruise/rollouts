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
	"testing"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestPodEventHandler(t *testing.T) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	handler := enqueueRequestForWorkload{reader: fakeClient, scheme: scheme}

	err := fakeClient.Create(context.TODO(), rolloutDemo.DeepCopy())
	if nil != err {
		t.Fatalf("unexpected create rollout %s failed: %v", rolloutDemo.Name, err)
	}

	// create
	createQ := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	createEvt := event.CreateEvent{
		Object: deploymentDemo,
	}
	handler.Create(createEvt, createQ)
	if createQ.Len() != 1 {
		t.Errorf("unexpected create event handle queue size, expected 1 actual %d", createQ.Len())
	}

	// other namespace
	demo1 := deploymentDemo.DeepCopy()
	demo1.Namespace = "other-ns"
	createEvt = event.CreateEvent{
		Object: demo1,
	}
	handler.Create(createEvt, createQ)
	if createQ.Len() != 1 {
		t.Errorf("unexpected create event handle queue size, expected 1 actual %d", createQ.Len())
	}
}
