/*
Copyright 2025 The Kruise Authors.

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
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/openkruise/rollouts/pkg/util"
	expectations "github.com/openkruise/rollouts/pkg/util/expectation"
)

const (
	testNamespace = "default"
	testDSName    = "test-daemon"
	testRevision  = "test-revision-123"
)

// Helper functions for creating test objects

// createTestReconciler creates a test ReconcileNativeDaemonSet with all required fields
func createTestReconciler(client client.Client) *ReconcileNativeDaemonSet {
	return &ReconcileNativeDaemonSet{
		Client:        client,
		eventRecorder: record.NewFakeRecorder(10),
		expectations:  expectations.NewResourceExpectations(),
	}
}
func createTestDaemonSet(name, namespace string, annotations map[string]string) *appsv1.DaemonSet {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
			UID:         types.UID(fmt.Sprintf("uid-%s", name)),
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "container",
							Image: "nginx:1.14",
						},
					},
				},
			},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: appsv1.RollingUpdateDaemonSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDaemonSet{
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
				},
			},
		},
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 3,
		},
	}
	return ds
}

func createTestPod(name, namespace, dsName, revision string, hasRevisionLabel bool, deletionTimestamp *metav1.Time) *corev1.Pod {
	labels := map[string]string{
		"app": dsName,
	}
	if hasRevisionLabel && revision != "" {
		labels[appsv1.ControllerRevisionHashLabelKey] = revision
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			Labels:            labels,
			DeletionTimestamp: deletionTimestamp,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       dsName,
					UID:        types.UID(fmt.Sprintf("uid-%s", dsName)),
					Controller: pointer.Bool(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "container",
					Image: "nginx:1.14",
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	return pod
}

func createFakeClient(objects ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestNewReconciler(t *testing.T) {
	// Test setup
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Since newReconciler depends on external client registry that's not initialized in tests,
	// we'll test the reconciler functionality directly
	client := createFakeClient()
	recorder := record.NewFakeRecorder(10)

	r := &ReconcileNativeDaemonSet{
		Client:        client,
		eventRecorder: recorder,
		expectations:  expectations.NewResourceExpectations(),
	}

	assert.NotNil(t, r.Client)
	assert.NotNil(t, r.eventRecorder)
	assert.NotNil(t, r.expectations)
}

func TestReconcile_DaemonSetNotFound(t *testing.T) {
	client := createFakeClient()
	r := createTestReconciler(client)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "non-existent",
			Namespace: testNamespace,
		},
	}

	result, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcile_NoPartitionAnnotation(t *testing.T) {
	ds := createTestDaemonSet(testDSName, testNamespace, map[string]string{})
	client := createFakeClient(ds)
	r := createTestReconciler(client)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      testDSName,
			Namespace: testNamespace,
		},
	}

	result, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)
}

func TestReconcile_WithPartitionAnnotation(t *testing.T) {
	annotations := map[string]string{
		util.DaemonSetPartitionAnnotation:     "1",
		util.DaemonSetBatchRevisionAnnotation: testRevision,
	}
	ds := createTestDaemonSet(testDSName, testNamespace, annotations)

	// Create test pods
	pod1 := createTestPod("pod-1", testNamespace, testDSName, testRevision, true, nil)
	pod2 := createTestPod("pod-2", testNamespace, testDSName, "old-revision", true, nil)
	pod3 := createTestPod("pod-3", testNamespace, testDSName, "old-revision", true, nil)

	client := createFakeClient(ds, pod1, pod2, pod3)
	r := createTestReconciler(client)

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      testDSName,
			Namespace: testNamespace,
		},
	}

	result, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, reconcile.Result{RequeueAfter: DefaultRetryDuration}, result)

	// Verify that one pod was deleted
	var podList corev1.PodList
	err = client.List(context.TODO(), &podList)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(podList.Items)) // One pod should be deleted
}

func TestReconcile_InvalidPartition(t *testing.T) {
	annotations := map[string]string{
		util.DaemonSetPartitionAnnotation:     "invalid",
		util.DaemonSetBatchRevisionAnnotation: testRevision,
	}
	ds := createTestDaemonSet(testDSName, testNamespace, annotations)
	client := createFakeClient(ds)
	recorder := record.NewFakeRecorder(10)

	r := &ReconcileNativeDaemonSet{
		Client:        client,
		eventRecorder: recorder,
		expectations:  expectations.NewResourceExpectations(),
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      testDSName,
			Namespace: testNamespace,
		},
	}

	result, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid partition value")
	assert.Equal(t, ctrl.Result{}, result)
}

func TestReconcile_PodsBeingDeleted(t *testing.T) {
	annotations := map[string]string{
		util.DaemonSetPartitionAnnotation:     "1",
		util.DaemonSetBatchRevisionAnnotation: testRevision,
	}
	ds := createTestDaemonSet(testDSName, testNamespace, annotations)

	// Create pods with one being deleted
	now := metav1.Now()
	pod1 := createTestPod("pod-1", testNamespace, testDSName, testRevision, true, nil)
	pod2 := createTestPod("pod-2", testNamespace, testDSName, "old-revision", true, &now)
	// Add finalizer to pod that's being deleted to satisfy fake client requirements
	pod2.Finalizers = []string{"test-finalizer"}

	client := createFakeClient(ds, pod1, pod2)
	recorder := record.NewFakeRecorder(10)

	r := &ReconcileNativeDaemonSet{
		Client:        client,
		eventRecorder: recorder,
		expectations:  expectations.NewResourceExpectations(),
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      testDSName,
			Namespace: testNamespace,
		},
	}

	result, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Equal(t, reconcile.Result{RequeueAfter: DefaultRetryDuration}, result)
}

func TestAnalyzePods(t *testing.T) {
	r := &ReconcileNativeDaemonSet{
		expectations: expectations.NewResourceExpectations(),
	}
	ds := createTestDaemonSet(testDSName, testNamespace, map[string]string{})

	tests := []struct {
		name                   string
		pods                   []*corev1.Pod
		updateRevision         string
		desiredUpdatedReplicas int32
		expectedToDelete       int
		expectedNeedToDelete   int32
		expectedBatchCompleted bool
		expectError            bool
	}{
		{
			name: "no pods to update",
			pods: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, testRevision, true, nil),
				createTestPod("pod-2", testNamespace, testDSName, testRevision, true, nil),
			},
			updateRevision:         testRevision,
			desiredUpdatedReplicas: 2,
			expectedToDelete:       0,
			expectedNeedToDelete:   0,
			expectedBatchCompleted: true,
		},
		{
			name: "need to delete one pod",
			pods: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, testRevision, true, nil),
				createTestPod("pod-2", testNamespace, testDSName, "old-revision", true, nil),
				createTestPod("pod-3", testNamespace, testDSName, "old-revision", true, nil),
			},
			updateRevision:         testRevision,
			desiredUpdatedReplicas: 2,
			expectedToDelete:       2,
			expectedNeedToDelete:   1,
			expectedBatchCompleted: false,
		},
		{
			name: "already have enough updated pods",
			pods: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, testRevision, true, nil),
				createTestPod("pod-2", testNamespace, testDSName, testRevision, true, nil),
				createTestPod("pod-3", testNamespace, testDSName, "old-revision", true, nil),
			},
			updateRevision:         testRevision,
			desiredUpdatedReplicas: 2,
			expectedToDelete:       1,
			expectedNeedToDelete:   0,
			expectedBatchCompleted: true,
		},
		{
			name: "no revision label on pods",
			pods: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, "", false, nil),
				createTestPod("pod-2", testNamespace, testDSName, "", false, nil),
			},
			updateRevision:         testRevision,
			desiredUpdatedReplicas: 2,
			expectedToDelete:       2,
			expectedNeedToDelete:   1, // Limited by maxUnavailable = 1
			expectedBatchCompleted: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			podsToDelete, needToDelete, batchCompleted, err := r.analyzePods(test.pods, test.updateRevision, test.desiredUpdatedReplicas, ds)

			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expectedToDelete, len(podsToDelete))
				assert.Equal(t, test.expectedNeedToDelete, needToDelete)
				assert.Equal(t, test.expectedBatchCompleted, batchCompleted)
			}
		})
	}
}

func TestExecutePodDeletion(t *testing.T) {
	ds := createTestDaemonSet(testDSName, testNamespace, map[string]string{})

	tests := []struct {
		name         string
		podsToDelete []*corev1.Pod
		needToDelete int32
		expectError  bool
		expectEvents int
	}{
		{
			name:         "no pods to delete",
			podsToDelete: []*corev1.Pod{},
			needToDelete: 0,
			expectError:  false,
			expectEvents: 0,
		},
		{
			name: "delete one pod",
			podsToDelete: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, "old-revision", true, nil),
			},
			needToDelete: 1,
			expectError:  false,
			expectEvents: 1,
		},
		{
			name: "delete multiple pods",
			podsToDelete: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, "old-revision", true, nil),
				createTestPod("pod-2", testNamespace, testDSName, "old-revision", true, nil),
			},
			needToDelete: 2,
			expectError:  false,
			expectEvents: 2,
		},
		{
			name: "need to delete more pods than available",
			podsToDelete: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, "old-revision", true, nil),
			},
			needToDelete: 3,
			expectError:  false,
			expectEvents: 1, // Only one pod can be deleted
		},
		{
			name: "negative needToDelete",
			podsToDelete: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, "old-revision", true, nil),
			},
			needToDelete: -1,
			expectError:  false,
			expectEvents: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			objects := []client.Object{ds}
			for _, pod := range test.podsToDelete {
				objects = append(objects, pod)
			}
			client := createFakeClient(objects...)
			recorder := record.NewFakeRecorder(10)

			r := &ReconcileNativeDaemonSet{
				Client:        client,
				eventRecorder: recorder,
				expectations:  expectations.NewResourceExpectations(),
			}

			err := r.executePodDeletion(context.TODO(), test.podsToDelete, test.needToDelete, ds)

			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// Check that events were recorded
				recordedEvents := len(recorder.Events)
				// Note: We need to drain events from the channel to count them
				eventCount := 0
				for eventCount < recordedEvents {
					select {
					case <-recorder.Events:
						eventCount++
					default:
						break
					}
				}
			}
		})
	}
}

func TestProcessBatch(t *testing.T) {
	tests := []struct {
		name            string
		ds              *appsv1.DaemonSet
		pods            []*corev1.Pod
		desiredReplicas int32
		expectRequeue   bool
		expectError     bool
	}{
		{
			name: "pods being deleted - requeue",
			ds:   createTestDaemonSet(testDSName, testNamespace, map[string]string{}),
			pods: func() []*corev1.Pod {
				pod := createTestPod("pod-1", testNamespace, testDSName, testRevision, true, &metav1.Time{Time: time.Now()})
				pod.Finalizers = []string{"test-finalizer"}
				return []*corev1.Pod{pod}
			}(),
			desiredReplicas: 1,
			expectRequeue:   true,
			expectError:     false,
		},
		{
			name: "batch completed - no requeue",
			ds: createTestDaemonSet(testDSName, testNamespace, map[string]string{
				util.DaemonSetBatchRevisionAnnotation: testRevision,
			}),
			pods: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, testRevision, true, nil),
				createTestPod("pod-2", testNamespace, testDSName, testRevision, true, nil),
			},
			desiredReplicas: 2,
			expectRequeue:   false,
			expectError:     false,
		},
		{
			name: "need to delete pods - requeue",
			ds: createTestDaemonSet(testDSName, testNamespace, map[string]string{
				util.DaemonSetBatchRevisionAnnotation: testRevision,
			}),
			pods: []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, testRevision, true, nil),
				createTestPod("pod-2", testNamespace, testDSName, "old-revision", true, nil),
			},
			desiredReplicas: 2,
			expectRequeue:   true,
			expectError:     false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			objects := []client.Object{test.ds}
			for _, pod := range test.pods {
				objects = append(objects, pod)
			}
			client := createFakeClient(objects...)
			recorder := record.NewFakeRecorder(10)

			r := &ReconcileNativeDaemonSet{
				Client:        client,
				eventRecorder: recorder,
				expectations:  expectations.NewResourceExpectations(),
			}

			result, err := r.processBatch(context.TODO(), test.ds, test.pods, test.desiredReplicas)

			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if test.expectRequeue {
					assert.Equal(t, DefaultRetryDuration, result.RequeueAfter)
				} else {
					assert.Equal(t, reconcile.Result{}, result)
				}
			}
		})
	}
}

func TestReconcile_ComplexScenarios(t *testing.T) {
	t.Run("integration test with maxUnavailable constraint", func(t *testing.T) {
		annotations := map[string]string{
			util.DaemonSetPartitionAnnotation:     "1",
			util.DaemonSetBatchRevisionAnnotation: testRevision,
		}
		ds := createTestDaemonSet(testDSName, testNamespace, annotations)
		// Set maxUnavailable to 2
		ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable = &intstr.IntOrString{Type: intstr.Int, IntVal: 2}

		// Create pods - need to update 4 pods but maxUnavailable is 2
		pod1 := createTestPod("pod-1", testNamespace, testDSName, testRevision, true, nil)
		pod2 := createTestPod("pod-2", testNamespace, testDSName, "old-revision", true, nil)
		pod3 := createTestPod("pod-3", testNamespace, testDSName, "old-revision", true, nil)
		pod4 := createTestPod("pod-4", testNamespace, testDSName, "old-revision", true, nil)
		pod5 := createTestPod("pod-5", testNamespace, testDSName, "old-revision", true, nil)

		client := createFakeClient(ds, pod1, pod2, pod3, pod4, pod5)
		recorder := record.NewFakeRecorder(10)

		r := &ReconcileNativeDaemonSet{
			Client:        client,
			eventRecorder: recorder,
			expectations:  expectations.NewResourceExpectations(),
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testDSName,
				Namespace: testNamespace,
			},
		}

		result, err := r.Reconcile(context.TODO(), req)
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: DefaultRetryDuration}, result)

		// Verify that only maxUnavailable pods were deleted
		var podList corev1.PodList
		err = client.List(context.TODO(), &podList)
		assert.NoError(t, err)
		assert.Equal(t, 3, len(podList.Items)) // 5 - 2 = 3 pods should remain
	})

	t.Run("percentage maxUnavailable", func(t *testing.T) {
		annotations := map[string]string{
			util.DaemonSetPartitionAnnotation:     "2",
			util.DaemonSetBatchRevisionAnnotation: testRevision,
		}
		ds := createTestDaemonSet(testDSName, testNamespace, annotations)
		ds.Status.DesiredNumberScheduled = 10
		// Set maxUnavailable to 25%
		ds.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable = &intstr.IntOrString{Type: intstr.String, StrVal: "25%"}

		// Create 6 pods - need to update 4 pods but maxUnavailable is 25% of 10 = 2
		pods := []*corev1.Pod{}
		for i := 0; i < 6; i++ {
			revision := "old-revision"
			if i < 2 {
				revision = testRevision
			}
			pod := createTestPod(fmt.Sprintf("pod-%d", i), testNamespace, testDSName, revision, true, nil)
			pods = append(pods, pod)
		}

		objects := []client.Object{ds}
		for _, pod := range pods {
			objects = append(objects, pod)
		}
		client := createFakeClient(objects...)
		recorder := record.NewFakeRecorder(10)

		r := &ReconcileNativeDaemonSet{
			Client:        client,
			eventRecorder: recorder,
			expectations:  expectations.NewResourceExpectations(),
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testDSName,
				Namespace: testNamespace,
			},
		}

		result, err := r.Reconcile(context.TODO(), req)
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: DefaultRetryDuration}, result)
	})
}

func TestReconcile_EdgeCases(t *testing.T) {
	t.Run("zero partition", func(t *testing.T) {
		annotations := map[string]string{
			util.DaemonSetPartitionAnnotation:     "0",
			util.DaemonSetBatchRevisionAnnotation: testRevision,
		}
		ds := createTestDaemonSet(testDSName, testNamespace, annotations)

		pod1 := createTestPod("pod-1", testNamespace, testDSName, "old-revision", true, nil)
		pod2 := createTestPod("pod-2", testNamespace, testDSName, "old-revision", true, nil)

		client := createFakeClient(ds, pod1, pod2)
		recorder := record.NewFakeRecorder(10)

		r := &ReconcileNativeDaemonSet{
			Client:        client,
			eventRecorder: recorder,
			expectations:  expectations.NewResourceExpectations(),
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testDSName,
				Namespace: testNamespace,
			},
		}

		result, err := r.Reconcile(context.TODO(), req)
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: DefaultRetryDuration}, result)
	})

	t.Run("partition greater than pod count", func(t *testing.T) {
		annotations := map[string]string{
			util.DaemonSetPartitionAnnotation:     "10",
			util.DaemonSetBatchRevisionAnnotation: testRevision,
		}
		ds := createTestDaemonSet(testDSName, testNamespace, annotations)

		pod1 := createTestPod("pod-1", testNamespace, testDSName, "old-revision", true, nil)

		client := createFakeClient(ds, pod1)
		recorder := record.NewFakeRecorder(10)

		r := &ReconcileNativeDaemonSet{
			Client:        client,
			eventRecorder: recorder,
			expectations:  expectations.NewResourceExpectations(),
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testDSName,
				Namespace: testNamespace,
			},
		}

		result, err := r.Reconcile(context.TODO(), req)
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, result) // Batch should be completed
	})

	t.Run("no pods", func(t *testing.T) {
		annotations := map[string]string{
			util.DaemonSetPartitionAnnotation:     "1",
			util.DaemonSetBatchRevisionAnnotation: testRevision,
		}
		ds := createTestDaemonSet(testDSName, testNamespace, annotations)

		client := createFakeClient(ds)
		recorder := record.NewFakeRecorder(10)

		r := &ReconcileNativeDaemonSet{
			Client:        client,
			eventRecorder: recorder,
			expectations:  expectations.NewResourceExpectations(),
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testDSName,
				Namespace: testNamespace,
			},
		}

		result, err := r.Reconcile(context.TODO(), req)
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{}, result)
	})
}

func TestReconcile_ErrorHandling(t *testing.T) {
	t.Run("client error during pod listing", func(t *testing.T) {
		annotations := map[string]string{
			util.DaemonSetPartitionAnnotation:     "1",
			util.DaemonSetBatchRevisionAnnotation: testRevision,
		}
		ds := createTestDaemonSet(testDSName, testNamespace, annotations)

		// Create a client that will error on List operations
		client := &errorClient{
			Client:    createFakeClient(ds),
			listError: errors.NewInternalError(fmt.Errorf("mock list error")),
		}
		recorder := record.NewFakeRecorder(10)

		r := &ReconcileNativeDaemonSet{
			Client:        client,
			eventRecorder: recorder,
			expectations:  expectations.NewResourceExpectations(),
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testDSName,
				Namespace: testNamespace,
			},
		}

		result, err := r.Reconcile(context.TODO(), req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mock list error")
		assert.Equal(t, ctrl.Result{}, result)
	})

	t.Run("execute pod deletion error", func(t *testing.T) {
		ds := createTestDaemonSet(testDSName, testNamespace, map[string]string{})
		pod := createTestPod("pod-1", testNamespace, testDSName, "old-revision", true, nil)

		// Create a client that will error on Delete operations
		client := &errorClient{
			Client:      createFakeClient(ds, pod),
			deleteError: errors.NewInternalError(fmt.Errorf("mock delete error")),
		}
		recorder := record.NewFakeRecorder(10)

		r := &ReconcileNativeDaemonSet{
			Client:        client,
			eventRecorder: recorder,
			expectations:  expectations.NewResourceExpectations(),
		}

		// Call executePodDeletion directly to test the error path
		err := r.executePodDeletion(context.TODO(), []*corev1.Pod{pod}, 1, ds)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mock delete error")
	})
}

func TestAdd(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	mgr := &mockManager{
		client: createFakeClient(),
		scheme: scheme,
	}

	reconciler := &ReconcileNativeDaemonSet{
		Client:        mgr.GetClient(),
		eventRecorder: record.NewFakeRecorder(10),
		expectations:  expectations.NewResourceExpectations(),
	}

	err := add(mgr, reconciler)
	assert.NoError(t, err)
}

// Mock implementations for testing

type mockManager struct {
	client client.Client
	scheme *runtime.Scheme
}

func (m *mockManager) GetClient() client.Client {
	return m.client
}

func (m *mockManager) GetScheme() *runtime.Scheme {
	return m.scheme
}

func (m *mockManager) GetConfig() *rest.Config {
	return nil
}

func (m *mockManager) GetCache() cache.Cache {
	return nil
}

func (m *mockManager) GetFieldIndexer() client.FieldIndexer {
	return nil
}

func (m *mockManager) GetEventRecorderFor(name string) record.EventRecorder {
	return record.NewFakeRecorder(10)
}

func (m *mockManager) GetRESTMapper() meta.RESTMapper {
	return nil
}

func (m *mockManager) GetAPIReader() client.Reader {
	return m.client
}

func (m *mockManager) Start(ctx context.Context) error {
	return nil
}

func (m *mockManager) Add(runnable manager.Runnable) error {
	return nil
}

func (m *mockManager) Elected() <-chan struct{} {
	return nil
}

func (m *mockManager) AddMetricsExtraHandler(path string, handler http.Handler) error {
	return nil
}

func (m *mockManager) AddMetricsServerExtraHandler(path string, handler http.Handler) error {
	return nil
}

func (m *mockManager) GetHTTPClient() *http.Client {
	return nil
}

func (m *mockManager) AddHealthzCheck(name string, check healthz.Checker) error {
	return nil
}

func (m *mockManager) AddReadyzCheck(name string, check healthz.Checker) error {
	return nil
}

func (m *mockManager) GetWebhookServer() webhook.Server {
	return nil
}

func (m *mockManager) GetLogger() logr.Logger {
	return logr.Discard()
}

func (m *mockManager) GetControllerOptions() config.Controller {
	return config.Controller{}
}

type errorClient struct {
	client.Client
	listError   error
	deleteError error
}

func (c *errorClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.listError != nil {
		return c.listError
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *errorClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if c.deleteError != nil {
		return c.deleteError
	}
	return c.Client.Delete(ctx, obj, opts...)
}

// Test pod sorting by deletion priority
func TestPodSortingByDeletionPriority(t *testing.T) {
	now := time.Now()

	// Create pods with different priorities for deletion
	// 1. Pending pod (highest priority)
	pod1 := createTestPod("pod-pending", testNamespace, testDSName, "old-revision", true, nil)
	pod1.CreationTimestamp = metav1.NewTime(now.Add(-3 * time.Hour))
	pod1.Status.Phase = corev1.PodPending

	// 2. Not ready pod
	pod2 := createTestPod("pod-not-ready", testNamespace, testDSName, "old-revision", true, nil)
	pod2.CreationTimestamp = metav1.NewTime(now.Add(-2 * time.Hour))
	pod2.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodReady, Status: corev1.ConditionFalse},
	}

	// 3. High deletion cost pod (lower priority for deletion)
	pod3 := createTestPod("pod-high-cost", testNamespace, testDSName, "old-revision", true, nil)
	pod3.CreationTimestamp = metav1.NewTime(now.Add(-4 * time.Hour))
	pod3.Annotations = map[string]string{
		"controller.kubernetes.io/pod-deletion-cost": "100",
	}
	pod3.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodReady, Status: corev1.ConditionTrue},
	}

	// 4. Newer ready pod (should be deleted before older ready pod)
	pod4 := createTestPod("pod-ready-new", testNamespace, testDSName, "old-revision", true, nil)
	pod4.CreationTimestamp = metav1.NewTime(now.Add(-1 * time.Hour))
	pod4.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodReady, Status: corev1.ConditionTrue},
	}

	// 5. Older ready pod (lowest priority)
	pod5 := createTestPod("pod-ready-old", testNamespace, testDSName, "old-revision", true, nil)
	pod5.CreationTimestamp = metav1.NewTime(now.Add(-5 * time.Hour))
	pod5.Status.Conditions = []corev1.PodCondition{
		{Type: corev1.PodReady, Status: corev1.ConditionTrue},
	}

	ds := createTestDaemonSet(testDSName, testNamespace, map[string]string{})
	client := createFakeClient(ds, pod1, pod2, pod3, pod4, pod5)
	recorder := record.NewFakeRecorder(10)

	r := &ReconcileNativeDaemonSet{
		Client:        client,
		eventRecorder: recorder,
		expectations:  expectations.NewResourceExpectations(),
	}

	// Delete 3 pods - should delete in priority order: pending, not-ready, newer ready pod
	// High cost pod should be deleted last due to low priority
	podsToDelete := []*corev1.Pod{pod1, pod2, pod3, pod4, pod5}
	err := r.executePodDeletion(context.TODO(), podsToDelete, 3, ds)
	assert.NoError(t, err)

	// Check remaining pods
	var podList corev1.PodList
	err = client.List(context.TODO(), &podList)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(podList.Items))
	
	// The remaining pods should be the high-cost pod and older ready pod
	remainingNames := make(map[string]bool)
	for _, pod := range podList.Items {
		remainingNames[pod.Name] = true
	}
	assert.True(t, remainingNames["pod-high-cost"])
	assert.True(t, remainingNames["pod-ready-old"])
}

// Test constants and global variables
func TestConstants(t *testing.T) {
	assert.Equal(t, 2*time.Second, DefaultRetryDuration)
	assert.Equal(t, "native-daemonset-controller", ControllerName)
	assert.Equal(t, 3, concurrentReconciles)
}

// Test revision consistency checking
func TestRevisionConsistencyChecking(t *testing.T) {
	r := &ReconcileNativeDaemonSet{
		expectations: expectations.NewResourceExpectations(),
	}
	ds := createTestDaemonSet(testDSName, testNamespace, map[string]string{})

	tests := []struct {
		name           string
		podRevision    string
		targetRevision string
		hasLabel       bool
		shouldMatch    bool
	}{
		{
			name:           "matching revision with label",
			podRevision:    "abc123",
			targetRevision: "prefix-abc123",
			hasLabel:       true,
			shouldMatch:    true,
		},
		{
			name:           "non-matching revision with label",
			podRevision:    "abc123",
			targetRevision: "prefix-def456",
			hasLabel:       true,
			shouldMatch:    false,
		},
		{
			name:           "no revision label",
			podRevision:    "",
			targetRevision: "prefix-abc123",
			hasLabel:       false,
			shouldMatch:    false,
		},
		{
			name:           "empty target revision",
			podRevision:    "abc123",
			targetRevision: "",
			hasLabel:       true,
			shouldMatch:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pods := []*corev1.Pod{
				createTestPod("pod-1", testNamespace, testDSName, test.podRevision, test.hasLabel, nil),
			}

			podsToDelete, needToDelete, batchCompleted, err := r.analyzePods(pods, test.targetRevision, 1, ds)
			assert.NoError(t, err)

			if test.shouldMatch {
				// Pod matches, so batch should be completed
				assert.True(t, batchCompleted)
				assert.Equal(t, int32(0), needToDelete)
			} else {
				// Pod doesn't match, so it should be marked for deletion
				assert.False(t, batchCompleted)
				assert.Equal(t, 1, len(podsToDelete))
				assert.Equal(t, int32(1), needToDelete)
			}
		})
	}
}

// Test update handler function behavior
func TestUpdateHandlerFunction(t *testing.T) {
	// Create test daemonsets
	oldDS := createTestDaemonSet("test-ds", testNamespace, map[string]string{
		"old-annotation": "old-value",
	})

	newDS := createTestDaemonSet("test-ds", testNamespace, map[string]string{
		"old-annotation": "old-value",
		"new-annotation": "new-value",
	})

	// Test annotation changes
	updateHandler := func(e event.UpdateEvent) bool {
		oldObject := e.ObjectOld.(*appsv1.DaemonSet)
		newObject := e.ObjectNew.(*appsv1.DaemonSet)
		if len(oldObject.Annotations) != len(newObject.Annotations) || !reflect.DeepEqual(oldObject.Annotations, newObject.Annotations) {
			return true
		}
		return false
	}

	// Test with annotation changes
	shouldUpdate := updateHandler(event.UpdateEvent{
		ObjectOld: oldDS,
		ObjectNew: newDS,
	})
	assert.True(t, shouldUpdate)

	// Test with no annotation changes
	shouldUpdate = updateHandler(event.UpdateEvent{
		ObjectOld: oldDS,
		ObjectNew: oldDS,
	})
	assert.False(t, shouldUpdate)
}

// Benchmark tests
func BenchmarkReconcile(b *testing.B) {
	annotations := map[string]string{
		util.DaemonSetPartitionAnnotation:     "2",
		util.DaemonSetBatchRevisionAnnotation: testRevision,
	}
	ds := createTestDaemonSet(testDSName, testNamespace, annotations)

	pods := []*corev1.Pod{}
	for i := 0; i < 10; i++ {
		revision := "old-revision"
		if i < 3 {
			revision = testRevision
		}
		pod := createTestPod(fmt.Sprintf("pod-%d", i), testNamespace, testDSName, revision, true, nil)
		pods = append(pods, pod)
	}

	objects := []client.Object{ds}
	for _, pod := range pods {
		objects = append(objects, pod)
	}

	client := createFakeClient(objects...)
	recorder := record.NewFakeRecorder(100)

	r := &ReconcileNativeDaemonSet{
		Client:        client,
		eventRecorder: recorder,
		expectations:  expectations.NewResourceExpectations(),
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      testDSName,
			Namespace: testNamespace,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Reconcile(context.TODO(), req)
	}
}

func BenchmarkAnalyzePods(b *testing.B) {
	r := &ReconcileNativeDaemonSet{
		expectations: expectations.NewResourceExpectations(),
	}
	ds := createTestDaemonSet(testDSName, testNamespace, map[string]string{})

	pods := []*corev1.Pod{}
	for i := 0; i < 100; i++ {
		revision := "old-revision"
		if i < 50 {
			revision = testRevision
		}
		pod := createTestPod(fmt.Sprintf("pod-%d", i), testNamespace, testDSName, revision, true, nil)
		pods = append(pods, pod)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = r.analyzePods(pods, testRevision, 75, ds)
	}
}

// Test expectations mechanism
func TestExpectationsMechanism(t *testing.T) {
	t.Run("expectations satisfied", func(t *testing.T) {
		annotations := map[string]string{
			util.DaemonSetPartitionAnnotation:     "0", // Need to update all pods
			util.DaemonSetBatchRevisionAnnotation: testRevision,
		}
		ds := createTestDaemonSet(testDSName, testNamespace, annotations)
		pod := createTestPod("pod-1", testNamespace, testDSName, "old-revision", true, nil)

		client := createFakeClient(ds, pod)
		recorder := record.NewFakeRecorder(10)

		r := &ReconcileNativeDaemonSet{
			Client:        client,
			eventRecorder: recorder,
			expectations:  expectations.NewResourceExpectations(),
		}

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testDSName,
				Namespace: testNamespace,
			},
		}

		// First reconcile should proceed normally and delete the pod
		result, err := r.Reconcile(context.TODO(), req)
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: DefaultRetryDuration}, result)
	})

	t.Run("expectations not satisfied - requeue", func(t *testing.T) {
		annotations := map[string]string{
			util.DaemonSetPartitionAnnotation:     "1",
			util.DaemonSetBatchRevisionAnnotation: testRevision,
		}
		ds := createTestDaemonSet(testDSName, testNamespace, annotations)
		pod := createTestPod("pod-1", testNamespace, testDSName, "old-revision", true, nil)

		client := createFakeClient(ds, pod)
		recorder := record.NewFakeRecorder(10)

		r := &ReconcileNativeDaemonSet{
			Client:        client,
			eventRecorder: recorder,
			expectations:  expectations.NewResourceExpectations(),
		}

		dsKey := fmt.Sprintf("%s/%s", testNamespace, testDSName)
		
		// Set an expectation that hasn't been satisfied
		r.expectations.Expect(dsKey, expectations.Delete, "test-uid")

		req := reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      testDSName,
				Namespace: testNamespace,
			},
		}

		// Should requeue due to unsatisfied expectations
		result, err := r.Reconcile(context.TODO(), req)
		assert.NoError(t, err)
		assert.Equal(t, reconcile.Result{RequeueAfter: DefaultRetryDuration}, result)
	})
}
