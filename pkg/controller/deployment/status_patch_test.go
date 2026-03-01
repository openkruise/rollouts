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

package deployment

import (
	"context"
	"testing"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type statusCallCounter struct {
	patchCalls  int
	updateCalls int
}

type statusCountingClient struct {
	ctrlclient.Client
	counter statusCallCounter
}

func (c *statusCountingClient) Status() ctrlclient.SubResourceWriter {
	return &statusCountingWriter{
		SubResourceWriter: c.Client.Status(),
		counter:           &c.counter,
	}
}

type statusCountingWriter struct {
	ctrlclient.SubResourceWriter
	counter *statusCallCounter
}

func (w *statusCountingWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	w.counter.updateCalls++
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func (w *statusCountingWriter) Patch(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
	w.counter.patchCalls++
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func TestPatchDeploymentStatusUsesStatusPatch(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := apps.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps/v1 scheme failed: %v", err)
	}

	baseDeployment := &apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "demo",
			Namespace:  "default",
			Generation: 5,
		},
		Status: apps.DeploymentStatus{
			ObservedGeneration: 1,
		},
	}

	baseClient := ctrlfake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&apps.Deployment{}).
		WithObjects(baseDeployment).
		Build()
	countingClient := &statusCountingClient{Client: baseClient}
	dc := &DeploymentController{runtimeClient: countingClient}

	current := &apps.Deployment{}
	key := ctrlclient.ObjectKeyFromObject(baseDeployment)
	if err := countingClient.Get(context.TODO(), key, current); err != nil {
		t.Fatalf("get deployment failed: %v", err)
	}
	oldD := current.DeepCopy()
	newD := current.DeepCopy()
	newD.Status.ObservedGeneration = newD.Generation

	if err := dc.patchDeploymentStatus(context.TODO(), oldD, newD); err != nil {
		t.Fatalf("patch deployment status failed: %v", err)
	}
	if countingClient.counter.patchCalls != 1 {
		t.Fatalf("expected 1 status patch call, got %d", countingClient.counter.patchCalls)
	}
	if countingClient.counter.updateCalls != 0 {
		t.Fatalf("expected 0 status update calls, got %d", countingClient.counter.updateCalls)
	}

	result := &apps.Deployment{}
	if err := countingClient.Get(context.TODO(), key, result); err != nil {
		t.Fatalf("get patched deployment failed: %v", err)
	}
	if result.Status.ObservedGeneration != result.Generation {
		t.Fatalf("expected observedGeneration=%d, got %d", result.Generation, result.Status.ObservedGeneration)
	}
}
