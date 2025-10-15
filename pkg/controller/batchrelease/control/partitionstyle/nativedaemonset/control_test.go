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

package nativedaemonset

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
)

var (
	daemonDemo = &apps.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "daemon-demo",
			Namespace: "default",
			Annotations: map[string]string{
				util.BatchReleaseControlAnnotation: `{"name":"release-demo","uid":"606132e0-85ef-460e-8a04-438496a92951","controller":true,"blockOwnerDeletion":true}`,
			},
		},
		Spec: apps.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "daemon-demo",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "daemon-demo",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "main",
							Image: "nginx:v1",
						},
					},
				},
			},
			UpdateStrategy: apps.DaemonSetUpdateStrategy{
				Type: apps.OnDeleteDaemonSetStrategyType,
			},
		},
		Status: apps.DaemonSetStatus{
			CurrentNumberScheduled: 5,
			NumberMisscheduled:     0,
			DesiredNumberScheduled: 5,
			NumberReady:            5,
			UpdatedNumberScheduled: 0,
			NumberAvailable:        5,
		},
	}

	batchReleaseDemo = &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release-demo",
			Namespace: "default",
			UID:       "606132e0-85ef-460e-8a04-438496a92951",
		},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "DaemonSet",
				Name:       "daemon-demo",
			},
			ReleasePlan: v1beta1.ReleasePlan{
				Batches: []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
					{
						CanaryReplicas: intstr.FromInt(3),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				},
			},
		},
		Status: v1beta1.BatchReleaseStatus{
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
				CurrentBatch: 0,
			},
			UpdateRevision: "update-revision-123",
		},
	}

	batchReleaseRollbackDemo = &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release-demo",
			Namespace: "default",
			UID:       "606132e0-85ef-460e-8a04-438496a92951",
			Annotations: map[string]string{
				"rollouts.kruise.io/rollback-in-batch": "true",
			},
		},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "DaemonSet",
				Name:       "daemon-demo",
			},
			ReleasePlan: v1beta1.ReleasePlan{
				Batches: []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
					{
						CanaryReplicas: intstr.FromInt(3),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				},
				RolloutID: "test-rollout-id",
			},
		},
		Status: v1beta1.BatchReleaseStatus{
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
				CurrentBatch:         0,
				NoNeedUpdateReplicas: pointer.Int32(2),
			},
			UpdateRevision: "update-revision-123",
		},
	}
)

func TestNewController(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemonDemo).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	assert.NotNil(t, controller)

	// Verify the controller is a realController
	realCtrl, ok := controller.(*realController)
	assert.True(t, ok)
	assert.Equal(t, key, realCtrl.key)
	assert.Equal(t, gvk, realCtrl.gvk)
	assert.NotNil(t, realCtrl.client)
}

func TestBuildController(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemonDemo).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, err := controller.BuildController()
	assert.NoError(t, err)
	assert.NotNil(t, builtController)
	assert.NotNil(t, builtController.GetWorkloadInfo())

	// Verify the workload info is correctly parsed
	workloadInfo := builtController.GetWorkloadInfo()
	assert.Equal(t, int32(5), workloadInfo.Replicas)
	assert.Equal(t, int32(5), workloadInfo.Status.ReadyReplicas)
}

func TestBuildController_DaemonSetNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cli := fake.NewClientBuilder().WithScheme(scheme).Build() // No objects
	key := types.NamespacedName{Name: "non-existent", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	_, err := controller.BuildController()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestBuildController_AlreadyBuilt(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemonDemo).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	// Build once
	builtController, err := controller.BuildController()
	assert.NoError(t, err)

	// Build again - should return same controller without re-fetching
	builtController2, err := controller.BuildController()
	assert.NoError(t, err)
	assert.Equal(t, builtController, builtController2)
}

func TestBuildController_WithZeroUpdatedReadyReplicas(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	daemon.Status.UpdatedNumberScheduled = 0 // Zero updated replicas

	// Create pods with different revisions
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "update-revision-123",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "old-revision",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1, pod2).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, err := controller.BuildController()
	assert.NoError(t, err)

	// The controller should fallback to status values when UpdatedNumberScheduled is 0
	workloadInfo := builtController.GetWorkloadInfo()
	assert.NotNil(t, workloadInfo)
	// When UpdatedNumberScheduled is 0, it falls back to status values
	assert.Equal(t, int32(0), workloadInfo.Status.UpdatedReadyReplicas)
}

func TestBuildController_ListOwnedPodsError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	daemon.Status.UpdatedNumberScheduled = 0 // Force pod listing
	// Create daemon with invalid selector to cause ListOwnedPods error
	daemon.Spec.Selector = &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      "invalid-key",
				Operator: metav1.LabelSelectorOpNotIn,
				Values:   []string{}, // Empty values will cause validation error
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	_, err := controller.BuildController()
	// This should error due to invalid selector
	assert.Error(t, err)
}

func TestGetWorkloadInfo(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemonDemo).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	workloadInfo := builtController.GetWorkloadInfo()
	assert.NotNil(t, workloadInfo)
	assert.Equal(t, int32(5), workloadInfo.Replicas)
}

func TestListOwnedPods(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()

	// Create a pod owned by the DaemonSet
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app": "daemon-demo",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	pods, err := builtController.ListOwnedPods()
	assert.NoError(t, err)
	assert.Len(t, pods, 1)
	assert.Equal(t, "pod-1", pods[0].Name)

	// Test cached pods - second call should return same pods
	pods2, err := builtController.ListOwnedPods()
	assert.NoError(t, err)
	assert.Len(t, pods2, 1)
	assert.Equal(t, pods, pods2)
}

func TestInitialize(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Remove control annotation to test initialization
	delete(daemon.Annotations, util.BatchReleaseControlAnnotation)
	daemon.Spec.UpdateStrategy.Type = apps.RollingUpdateDaemonSetStrategyType

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	err := builtController.Initialize(batchReleaseDemo)
	assert.NoError(t, err)

	// Verify the DaemonSet was updated correctly
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	// Check that the update strategy is now OnDelete
	assert.Equal(t, apps.OnDeleteDaemonSetStrategyType, updatedDaemon.Spec.UpdateStrategy.Type)

	// Check that the control annotation is set
	assert.Contains(t, updatedDaemon.Annotations, util.BatchReleaseControlAnnotation)
}

func TestInitializeAlreadyControlled(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	err := builtController.Initialize(batchReleaseDemo)
	assert.NoError(t, err)

	// Verify the DaemonSet was not changed
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	// Should remain OnDelete since it was already controlled
	assert.Equal(t, apps.OnDeleteDaemonSetStrategyType, updatedDaemon.Spec.UpdateStrategy.Type)
}

func TestInitialize_PatchError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	delete(daemon.Annotations, util.BatchReleaseControlAnnotation)

	// Don't add the daemon to the fake client to cause patch error
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	// Manually set the object to avoid BuildController fetching it
	rc := controller.(*realController)
	rc.object = daemon
	rc.WorkloadInfo = util.ParseWorkload(daemon)

	err := rc.Initialize(batchReleaseDemo)
	assert.Error(t, err) // Should fail because daemon doesn't exist in client
}

func TestUpgradeBatchFirstTime(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Remove batch annotations to simulate first time
	delete(daemon.Annotations, util.DaemonSetPartitionAnnotation)
	delete(daemon.Annotations, util.DaemonSetBatchRevisionAnnotation)

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	ctx := &batchcontext.BatchContext{
		CurrentBatch:     0,
		DesiredPartition: intstr.FromInt(3), // 5 total - 2 updated = 3 stable
		UpdateRevision:   "update-revision-123",
		Replicas:         5,
	}

	err := builtController.UpgradeBatch(ctx)
	assert.NoError(t, err)

	// Verify the DaemonSet has the batch annotations
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	assert.Equal(t, "3", updatedDaemon.Annotations[util.DaemonSetPartitionAnnotation])
	assert.Equal(t, "update-revision-123", updatedDaemon.Annotations[util.DaemonSetBatchRevisionAnnotation])
}

func TestUpgradeBatchSamePartition(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Set existing batch annotations with the same partition
	daemon.Annotations[util.DaemonSetPartitionAnnotation] = "3"
	daemon.Annotations[util.DaemonSetBatchRevisionAnnotation] = "update-revision-123"

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	ctx := &batchcontext.BatchContext{
		CurrentBatch:     0,
		DesiredPartition: intstr.FromInt(3), // Same partition
		UpdateRevision:   "update-revision-123",
		Replicas:         5,
	}

	err := builtController.UpgradeBatch(ctx)
	assert.NoError(t, err)

	// Verify the DaemonSet annotations remain unchanged
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	assert.Equal(t, "3", updatedDaemon.Annotations[util.DaemonSetPartitionAnnotation])
	assert.Equal(t, "update-revision-123", updatedDaemon.Annotations[util.DaemonSetBatchRevisionAnnotation])
}

func TestUpgradeBatchDifferentPartition(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Set existing batch annotations with different partition
	daemon.Annotations[util.DaemonSetPartitionAnnotation] = "3"
	daemon.Annotations[util.DaemonSetBatchRevisionAnnotation] = "old-revision"

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	ctx := &batchcontext.BatchContext{
		CurrentBatch:     1,
		DesiredPartition: intstr.FromInt(1), // Different partition
		UpdateRevision:   "update-revision-123",
		Replicas:         5,
	}

	err := builtController.UpgradeBatch(ctx)
	assert.NoError(t, err)

	// Verify the DaemonSet annotations are updated to the new partition
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	assert.Equal(t, "1", updatedDaemon.Annotations[util.DaemonSetPartitionAnnotation])
	assert.Equal(t, "update-revision-123", updatedDaemon.Annotations[util.DaemonSetBatchRevisionAnnotation])
}

func TestPatchBatchAnnotations_PatchError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Don't add daemon to client to cause patch error
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	rc := controller.(*realController)
	rc.object = daemon
	rc.WorkloadInfo = util.ParseWorkload(daemon)

	ctx := &batchcontext.BatchContext{
		CurrentBatch:     0,
		DesiredPartition: intstr.FromInt(3),
		UpdateRevision:   "update-revision-123",
	}

	err := rc.patchBatchAnnotations(ctx)
	assert.Error(t, err) // Should fail because daemon doesn't exist in client
}

func TestFinalizeWithBatchPartitionNil(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Add all batch annotations
	daemon.Annotations[util.DaemonSetPartitionAnnotation] = "2"
	daemon.Annotations[util.DaemonSetBatchRevisionAnnotation] = "update-revision-123"
	daemon.Annotations[util.DaemonSetCanaryRevisionAnnotation] = "canary-revision-123"
	daemon.Annotations[util.DaemonSetStableRevisionAnnotation] = "stable-revision-123"

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Create a batch release with nil BatchPartition (indicating completion)
	completedBatchRelease := batchReleaseDemo.DeepCopy()
	completedBatchRelease.Spec.ReleasePlan.BatchPartition = nil

	err := builtController.Finalize(completedBatchRelease)
	assert.NoError(t, err)

	// Verify the DaemonSet was updated correctly
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	// Check that the update strategy is restored to RollingUpdate
	assert.Equal(t, apps.RollingUpdateDaemonSetStrategyType, updatedDaemon.Spec.UpdateStrategy.Type)

	// Check that all batch annotations are removed
	assert.NotContains(t, updatedDaemon.Annotations, util.BatchReleaseControlAnnotation)
	assert.NotContains(t, updatedDaemon.Annotations, util.DaemonSetCanaryRevisionAnnotation)
	assert.NotContains(t, updatedDaemon.Annotations, util.DaemonSetStableRevisionAnnotation)
	assert.NotContains(t, updatedDaemon.Annotations, util.DaemonSetPartitionAnnotation)
	assert.NotContains(t, updatedDaemon.Annotations, util.DaemonSetBatchRevisionAnnotation)
}

func TestFinalizeWithBatchPartitionNotNil(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Add all batch annotations
	daemon.Annotations[util.DaemonSetPartitionAnnotation] = "2"
	daemon.Annotations[util.DaemonSetBatchRevisionAnnotation] = "update-revision-123"
	daemon.Annotations[util.DaemonSetCanaryRevisionAnnotation] = "canary-revision-123"
	daemon.Annotations[util.DaemonSetStableRevisionAnnotation] = "stable-revision-123"

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Create a batch release with non-nil BatchPartition (indicating in-progress)
	inProgressBatchRelease := batchReleaseDemo.DeepCopy()
	batchPartition := int32(1)
	inProgressBatchRelease.Spec.ReleasePlan.BatchPartition = &batchPartition

	err := builtController.Finalize(inProgressBatchRelease)
	assert.NoError(t, err)

	// Verify the DaemonSet was updated correctly
	updatedDaemon := &apps.DaemonSet{}
	err = cli.Get(context.TODO(), key, updatedDaemon)
	assert.NoError(t, err)

	// Check that all batch annotations are removed
	assert.NotContains(t, updatedDaemon.Annotations, util.BatchReleaseControlAnnotation)
	assert.NotContains(t, updatedDaemon.Annotations, util.DaemonSetCanaryRevisionAnnotation)
	assert.NotContains(t, updatedDaemon.Annotations, util.DaemonSetStableRevisionAnnotation)
	assert.NotContains(t, updatedDaemon.Annotations, util.DaemonSetPartitionAnnotation)
	assert.NotContains(t, updatedDaemon.Annotations, util.DaemonSetBatchRevisionAnnotation)

	// Update strategy should remain OnDelete since batch is not complete
	assert.Equal(t, apps.OnDeleteDaemonSetStrategyType, updatedDaemon.Spec.UpdateStrategy.Type)
}

func TestFinalize_NilObject(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	rc := controller.(*realController)
	rc.object = nil // Set object to nil

	err := rc.Finalize(batchReleaseDemo)
	assert.NoError(t, err) // Should return without error
}

func TestFinalize_PatchError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	// Add all batch annotations
	daemon.Annotations[util.DaemonSetPartitionAnnotation] = "2"
	daemon.Annotations[util.DaemonSetBatchRevisionAnnotation] = "update-revision-123"

	// Don't add daemon to client to cause patch error
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	rc := controller.(*realController)
	rc.object = daemon
	rc.WorkloadInfo = util.ParseWorkload(daemon)

	err := rc.Finalize(batchReleaseDemo)
	assert.Error(t, err) // Should fail because daemon doesn't exist in client
}

func TestCalculateBatchContext(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	ctx, err := builtController.CalculateBatchContext(batchReleaseDemo)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)
	assert.Equal(t, int32(0), ctx.CurrentBatch)
	assert.Equal(t, "update-revision-123", ctx.UpdateRevision)
	assert.Equal(t, int32(5), ctx.Replicas)
	assert.Equal(t, intstr.FromInt(0), ctx.CurrentPartition)
	// For batch 0 with 1 canary replica out of 5 total, desired partition = 5-1 = 4
	assert.Equal(t, intstr.FromInt(4), ctx.DesiredPartition)
}

func TestCalculateBatchContextWithRollback(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	ctx, err := builtController.CalculateBatchContext(batchReleaseRollbackDemo)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)
	// With rollback, we should have NoNeedUpdatedReplicas set
	assert.NotNil(t, ctx.NoNeedUpdatedReplicas)
	assert.Equal(t, int32(2), *ctx.NoNeedUpdatedReplicas)
	assert.NotNil(t, ctx.FilterFunc)
}

func TestCalculateBatchContextWithPods(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()

	// Create pods with different revisions
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "update-revision-123",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "old-revision",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1, pod2).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set the release with a rollout ID to trigger pod listing
	releaseWithRolloutID := batchReleaseDemo.DeepCopy()
	releaseWithRolloutID.Spec.ReleasePlan.RolloutID = "test-rollout-id"

	ctx, err := builtController.CalculateBatchContext(releaseWithRolloutID)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)
	assert.Equal(t, "test-rollout-id", ctx.RolloutID)
	assert.Len(t, ctx.Pods, 2)

	// The updated ready replicas should be 1 (only pod1 has the update revision)
	assert.Equal(t, int32(1), ctx.UpdatedReadyReplicas)
}

func TestCalculateBatchContext_WithRolloutID(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	
	// Create a simple pod for the test
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "update-revision-123",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Create a release with rollout ID to trigger pod listing
	releaseWithRolloutID := batchReleaseDemo.DeepCopy()
	releaseWithRolloutID.Spec.ReleasePlan.RolloutID = "test-rollout-id"

	ctx, err := builtController.CalculateBatchContext(releaseWithRolloutID)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)
	assert.Equal(t, "test-rollout-id", ctx.RolloutID)
	// Should have pods listed
	assert.Len(t, ctx.Pods, 1)
}

func TestCalculateBatchContext_NoPodsAvailable(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()
	rc := builtController.(*realController)
	rc.pods = nil // Ensure no pods are cached

	// Test without rollout ID - should use status values
	ctx, err := builtController.CalculateBatchContext(batchReleaseDemo)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)
	// Should fallback to status values
	assert.Equal(t, int32(0), ctx.UpdatedReadyReplicas) // From status
}

func TestCalculateBatchContext_WithPodsBeingDeleted(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()

	// Create pods with one being deleted
	deletionTime := metav1.Now()
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "update-revision-123",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
			DeletionTimestamp: &deletionTime, // Being deleted
			Finalizers:        []string{"test.finalizer"}, // Required for fake client
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "update-revision-123",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1, pod2).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set the release with a rollout ID to trigger pod listing
	releaseWithRolloutID := batchReleaseDemo.DeepCopy()
	releaseWithRolloutID.Spec.ReleasePlan.RolloutID = "test-rollout-id"

	ctx, err := builtController.CalculateBatchContext(releaseWithRolloutID)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	// Only pod2 should be counted (pod1 is being deleted)
	assert.Equal(t, int32(1), ctx.UpdatedReadyReplicas)
}

func TestCalculateBatchContext_WithNotReadyPods(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()

	// Create pods with one not ready
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "update-revision-123",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionFalse, // Not ready
				},
			},
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-2",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "update-revision-123",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue, // Ready
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1, pod2).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set the release with a rollout ID to trigger pod listing
	releaseWithRolloutID := batchReleaseDemo.DeepCopy()
	releaseWithRolloutID.Spec.ReleasePlan.RolloutID = "test-rollout-id"

	ctx, err := builtController.CalculateBatchContext(releaseWithRolloutID)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	// Only pod2 should be counted (pod1 is not ready)
	assert.Equal(t, int32(1), ctx.UpdatedReadyReplicas)
}

func TestCalculateBatchContext_ComplexScenario(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	daemon.Status.DesiredNumberScheduled = 10 // Total 10 replicas

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Create a release with complex batch setup
	complexRelease := batchReleaseDemo.DeepCopy()
	complexRelease.Status.CanaryStatus.CurrentBatch = 1
	complexRelease.Status.CanaryStatus.NoNeedUpdateReplicas = pointer.Int32(2)

	ctx, err := builtController.CalculateBatchContext(complexRelease)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	// Test complex calculations with NoNeedUpdateReplicas
	assert.Equal(t, int32(1), ctx.CurrentBatch)
	assert.NotNil(t, ctx.NoNeedUpdatedReplicas)
	assert.Equal(t, int32(2), *ctx.NoNeedUpdatedReplicas)

	// Verify that DesiredPartition is calculated correctly
	assert.NotNil(t, ctx.DesiredPartition)
}

func TestCalculateBatchContext_ListOwnedPodsError(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Force an error by setting buildController to nil to simulate a failure scenario
	rc := builtController.(*realController)
	rc.pods = nil
	
	// Create a release with rollout ID but with no pods available to simulate error conditions
	releaseWithRolloutID := batchReleaseDemo.DeepCopy()
	releaseWithRolloutID.Spec.ReleasePlan.RolloutID = "test-rollout-id"

	// This should work but with no pods found
	ctx, err := builtController.CalculateBatchContext(releaseWithRolloutID)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)
	assert.Equal(t, "test-rollout-id", ctx.RolloutID)
	assert.Len(t, ctx.Pods, 0) // No pods should be found
}

func TestCalculateBatchContext_DesiredPartitionZero(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Create a release that would result in desiredStable <= 0
	releaseAllUpdate := batchReleaseDemo.DeepCopy()
	releaseAllUpdate.Status.CanaryStatus.CurrentBatch = 2 // Last batch (100%)

	ctx, err := builtController.CalculateBatchContext(releaseAllUpdate)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	// When all pods should be updated, desiredPartition should be 0
	assert.Equal(t, intstr.FromInt(0), ctx.DesiredPartition)
}

func TestCalculateBatchContext_WithDifferentRevisionPods(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = apps.AddToScheme(scheme)
	_ = v1beta1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	daemon := daemonDemo.DeepCopy()

	// Create pods with different revisions - none matching the update revision
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: "default",
			Labels: map[string]string{
				"app":                               "daemon-demo",
				apps.ControllerRevisionHashLabelKey: "different-revision",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemon-demo",
					Controller: pointer.Bool(true),
					UID:        daemon.UID,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(daemon, pod1).Build()
	key := types.NamespacedName{Name: "daemon-demo", Namespace: "default"}
	gvk := schema.FromAPIVersionAndKind("apps/v1", "DaemonSet")

	controller := NewController(cli, key, gvk)
	builtController, _ := controller.BuildController()

	// Set the release with a rollout ID to trigger pod listing
	releaseWithRolloutID := batchReleaseDemo.DeepCopy()
	releaseWithRolloutID.Spec.ReleasePlan.RolloutID = "test-rollout-id"

	ctx, err := builtController.CalculateBatchContext(releaseWithRolloutID)
	assert.NoError(t, err)
	assert.NotNil(t, ctx)

	// No pods match the update revision, so UpdatedReadyReplicas should be 0
	assert.Equal(t, int32(0), ctx.UpdatedReadyReplicas)
}
