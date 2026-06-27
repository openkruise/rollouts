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
	"errors"
	"testing"

	apps "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	deploymentutil "github.com/openkruise/rollouts/pkg/controller/deployment/util"
	rolloututil "github.com/openkruise/rollouts/pkg/util"
)

type statusCallCounter struct {
	patchCalls  int
	updateCalls int
}

type statusCountingClient struct {
	ctrlclient.Client
	counter   statusCallCounter
	patchErr  error
	createErr error
}

func (c *statusCountingClient) Status() ctrlclient.SubResourceWriter {
	return &statusCountingWriter{
		SubResourceWriter: c.Client.Status(),
		counter:           &c.counter,
		patchErr:          c.patchErr,
	}
}

func (c *statusCountingClient) Create(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
	if c.createErr != nil {
		if _, ok := obj.(*apps.ReplicaSet); ok {
			return c.createErr
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

type statusCountingWriter struct {
	ctrlclient.SubResourceWriter
	counter  *statusCallCounter
	patchErr error
}

func (w *statusCountingWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	w.counter.updateCalls++
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func (w *statusCountingWriter) Patch(ctx context.Context, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.SubResourcePatchOption) error {
	w.counter.patchCalls++
	if w.patchErr != nil {
		return w.patchErr
	}
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

func newStatusPatchTestController(t *testing.T, objects ...ctrlclient.Object) (*statusCountingClient, *DeploymentController) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := apps.AddToScheme(scheme); err != nil {
		t.Fatalf("add apps/v1 scheme failed: %v", err)
	}

	baseClient := ctrlfake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&apps.Deployment{}).
		WithObjects(objects...).
		Build()
	countingClient := &statusCountingClient{Client: baseClient}
	dc := &DeploymentController{
		eventRecorder: record.NewFakeRecorder(10),
		runtimeClient: countingClient,
	}
	return countingClient, dc
}

func mustGetDeployment(t *testing.T, c ctrlclient.Client, deployment *apps.Deployment) *apps.Deployment {
	t.Helper()

	current := &apps.Deployment{}
	if err := c.Get(context.TODO(), ctrlclient.ObjectKeyFromObject(deployment), current); err != nil {
		t.Fatalf("get deployment failed: %v", err)
	}
	return current
}

func mustGetReplicaSet(t *testing.T, c ctrlclient.Client, rs *apps.ReplicaSet) *apps.ReplicaSet {
	t.Helper()

	current := &apps.ReplicaSet{}
	if err := c.Get(context.TODO(), ctrlclient.ObjectKeyFromObject(rs), current); err != nil {
		t.Fatalf("get replicaSet failed: %v", err)
	}
	return current
}

func TestPatchDeploymentStatusUsesStatusPatch(t *testing.T) {
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

	countingClient, dc := newStatusPatchTestController(t, baseDeployment)

	current := mustGetDeployment(t, countingClient, baseDeployment)
	key := ctrlclient.ObjectKeyFromObject(baseDeployment)
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

func TestSyncDeploymentSelectingAllIgnoresStatusPatchError(t *testing.T) {
	deployment := generateDeployment("busybox")
	deployment.Namespace = "default"
	deployment.Spec.Selector = &metav1.LabelSelector{}
	deployment.Generation = 7
	deployment.Status.ObservedGeneration = 3

	countingClient, dc := newStatusPatchTestController(t, &deployment)
	countingClient.patchErr = errors.New("status patch failed")

	if err := dc.syncDeployment(context.TODO(), &deployment); err != nil {
		t.Fatalf("syncDeployment returned unexpected error: %v", err)
	}
	if countingClient.counter.patchCalls != 1 {
		t.Fatalf("expected 1 status patch call, got %d", countingClient.counter.patchCalls)
	}
	if countingClient.counter.updateCalls != 0 {
		t.Fatalf("expected 0 status update calls, got %d", countingClient.counter.updateCalls)
	}

	result := mustGetDeployment(t, countingClient, &deployment)
	if result.Status.ObservedGeneration != 3 {
		t.Fatalf("expected observedGeneration to remain 3 after patch error, got %d", result.Status.ObservedGeneration)
	}
}

func TestGetNewReplicaSetUsesStatusPatchForExistingNewRS(t *testing.T) {
	deployment := generateDeployment("busybox")
	deployment.Namespace = "default"
	progressDeadlineSeconds := int32(30)
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadlineSeconds

	existingNewRS := generateRS(deployment)
	existingNewRS.Name = "busybox-existing-rs"
	existingNewRS.Namespace = deployment.Namespace
	existingNewRS.Annotations = map[string]string{
		deploymentutil.RevisionAnnotation: "1",
	}

	countingClient, dc := newStatusPatchTestController(t, &deployment, &existingNewRS)
	currentDeployment := mustGetDeployment(t, countingClient, &deployment)
	currentNewRS := mustGetReplicaSet(t, countingClient, &existingNewRS)

	newRS, err := dc.getNewReplicaSet(context.TODO(), currentDeployment, []*apps.ReplicaSet{currentNewRS}, nil, false)
	if err != nil {
		t.Fatalf("getNewReplicaSet returned unexpected error: %v", err)
	}
	if newRS == nil || newRS.Name != currentNewRS.Name {
		t.Fatalf("expected existing newRS %q, got %#v", currentNewRS.Name, newRS)
	}
	if countingClient.counter.patchCalls != 1 {
		t.Fatalf("expected 1 status patch call, got %d", countingClient.counter.patchCalls)
	}
	if countingClient.counter.updateCalls != 0 {
		t.Fatalf("expected 0 status update calls, got %d", countingClient.counter.updateCalls)
	}

	result := mustGetDeployment(t, countingClient, &deployment)
	condition := deploymentutil.GetDeploymentCondition(result.Status, apps.DeploymentProgressing)
	if condition == nil {
		t.Fatalf("expected progressing condition to be set")
	}
	if condition.Reason != deploymentutil.FoundNewRSReason {
		t.Fatalf("expected progressing reason %q, got %q", deploymentutil.FoundNewRSReason, condition.Reason)
	}
}

func TestGetNewReplicaSetUsesStatusPatchWhenCreatingNewRS(t *testing.T) {
	deployment := generateDeployment("busybox")
	deployment.Namespace = "default"
	progressDeadlineSeconds := int32(30)
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadlineSeconds

	countingClient, dc := newStatusPatchTestController(t, &deployment)
	currentDeployment := mustGetDeployment(t, countingClient, &deployment)

	newRS, err := dc.getNewReplicaSet(context.TODO(), currentDeployment, nil, nil, true)
	if err != nil {
		t.Fatalf("getNewReplicaSet returned unexpected error: %v", err)
	}
	if newRS == nil {
		t.Fatalf("expected a new ReplicaSet to be created")
	}
	if countingClient.counter.patchCalls != 1 {
		t.Fatalf("expected 1 status patch call, got %d", countingClient.counter.patchCalls)
	}
	if countingClient.counter.updateCalls != 0 {
		t.Fatalf("expected 0 status update calls, got %d", countingClient.counter.updateCalls)
	}

	result := mustGetDeployment(t, countingClient, &deployment)
	condition := deploymentutil.GetDeploymentCondition(result.Status, apps.DeploymentProgressing)
	if condition == nil {
		t.Fatalf("expected progressing condition to be set")
	}
	if condition.Reason != deploymentutil.NewReplicaSetReason {
		t.Fatalf("expected progressing reason %q, got %q", deploymentutil.NewReplicaSetReason, condition.Reason)
	}

	storedNewRS := &apps.ReplicaSet{}
	if err := countingClient.Get(context.TODO(), ctrlclient.ObjectKeyFromObject(newRS), storedNewRS); err != nil {
		t.Fatalf("get created ReplicaSet failed: %v", err)
	}
}

func TestGetNewReplicaSetUsesStatusPatchWhenCreateFails(t *testing.T) {
	deployment := generateDeployment("busybox")
	deployment.Namespace = "default"
	progressDeadlineSeconds := int32(30)
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadlineSeconds

	countingClient, dc := newStatusPatchTestController(t, &deployment)
	countingClient.createErr = errors.New("create replicaSet failed")
	currentDeployment := mustGetDeployment(t, countingClient, &deployment)

	newRS, err := dc.getNewReplicaSet(context.TODO(), currentDeployment, nil, nil, true)
	if err == nil {
		t.Fatalf("expected getNewReplicaSet to return an error")
	}
	if newRS != nil {
		t.Fatalf("expected no ReplicaSet to be returned on create failure, got %q", newRS.Name)
	}
	if countingClient.counter.patchCalls != 1 {
		t.Fatalf("expected 1 status patch call, got %d", countingClient.counter.patchCalls)
	}
	if countingClient.counter.updateCalls != 0 {
		t.Fatalf("expected 0 status update calls, got %d", countingClient.counter.updateCalls)
	}

	result := mustGetDeployment(t, countingClient, &deployment)
	condition := deploymentutil.GetDeploymentCondition(result.Status, apps.DeploymentProgressing)
	if condition == nil {
		t.Fatalf("expected progressing condition to be set on create failure")
	}
	if condition.Reason != deploymentutil.FailedRSCreateReason {
		t.Fatalf("expected progressing reason %q, got %q", deploymentutil.FailedRSCreateReason, condition.Reason)
	}
}

func TestGetNewReplicaSetUsesStatusPatchOnHashCollision(t *testing.T) {
	deployment := generateDeployment("busybox")
	deployment.Namespace = "default"
	deployment.UID = "deployment-uid"

	newRSTemplate := *deployment.Spec.Template.DeepCopy()
	hash := rolloututil.ComputeHash(&newRSTemplate, deployment.Status.CollisionCount)
	conflictingRS := generateRS(deployment)
	conflictingRS.Name = deployment.Name + "-" + hash
	conflictingRS.Namespace = deployment.Namespace
	conflictingRS.OwnerReferences = nil
	conflictingRS.Spec.Template.Spec.Containers[0].Image = "other-image"

	countingClient, dc := newStatusPatchTestController(t, &deployment, &conflictingRS)
	countingClient.createErr = apierrors.NewAlreadyExists(schema.GroupResource{Group: "apps", Resource: "replicasets"}, conflictingRS.Name)
	currentDeployment := mustGetDeployment(t, countingClient, &deployment)

	newRS, err := dc.getNewReplicaSet(context.TODO(), currentDeployment, nil, nil, true)
	if !apierrors.IsAlreadyExists(err) {
		t.Fatalf("expected already exists error, got %v", err)
	}
	if newRS != nil {
		t.Fatalf("expected no ReplicaSet to be returned on hash collision, got %q", newRS.Name)
	}
	if countingClient.counter.patchCalls != 1 {
		t.Fatalf("expected 1 status patch call, got %d", countingClient.counter.patchCalls)
	}
	if countingClient.counter.updateCalls != 0 {
		t.Fatalf("expected 0 status update calls, got %d", countingClient.counter.updateCalls)
	}

	result := mustGetDeployment(t, countingClient, &deployment)
	if result.Status.CollisionCount == nil || *result.Status.CollisionCount != 1 {
		t.Fatalf("expected collisionCount=1, got %#v", result.Status.CollisionCount)
	}
}
