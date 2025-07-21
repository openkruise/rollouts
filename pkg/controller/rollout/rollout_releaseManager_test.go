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

package rollout

import (
	"context"
	"testing"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestFetchBatchRelease(t *testing.T) {
	testCases := []struct {
		name            string
		existingObjects []runtime.Object
		namespace       string
		releaseName     string
		expectFound     bool
		expectError     bool
	}{
		{
			name: "BatchRelease exists",
			existingObjects: []runtime.Object{
				&v1beta1.BatchRelease{
					ObjectMeta: metav1.ObjectMeta{Name: "my-rollout", Namespace: "default"},
				},
			},
			namespace:   "default",
			releaseName: "my-rollout",
			expectFound: true,
			expectError: false,
		},
		{
			name:            "BatchRelease does not exist",
			existingObjects: []runtime.Object{},
			namespace:       "default",
			releaseName:     "my-rollout",
			expectFound:     false,
			expectError:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.existingObjects...).Build()
			br, err := fetchBatchRelease(fakeClient, tc.namespace, tc.releaseName)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			if tc.expectFound {
				assert.NotNil(t, br)
				assert.Equal(t, tc.releaseName, br.Name)
			} else {
				assert.True(t, br.Name == "" || err != nil)
			}
		})
	}
}

func TestRemoveRolloutProgressingAnnotation(t *testing.T) {
	workload := &util.Workload{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-deployment",
			Namespace: "default",
			Annotations: map[string]string{
				util.InRolloutProgressingAnnotation: "true",
			},
		},
	}
	rollout := &v1beta1.Rollout{
		Spec: v1beta1.RolloutSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "my-deployment",
			},
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-deployment",
			Namespace: "default",
			Annotations: map[string]string{
				util.InRolloutProgressingAnnotation: "true",
			},
		},
	}

	t.Run("should remove annotation when present", func(t *testing.T) {
		c := &RolloutContext{Rollout: rollout, Workload: workload}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(deployment).Build()
		err := removeRolloutProgressingAnnotation(fakeClient, c)
		assert.NoError(t, err)

		updatedDeployment := &appsv1.Deployment{}
		err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "my-deployment", Namespace: "default"}, updatedDeployment)
		assert.NoError(t, err)
		assert.NotContains(t, updatedDeployment.Annotations, util.InRolloutProgressingAnnotation)
	})

	t.Run("should do nothing if workload is nil", func(t *testing.T) {
		c := &RolloutContext{Rollout: rollout, Workload: nil}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		err := removeRolloutProgressingAnnotation(fakeClient, c)
		assert.NoError(t, err)
	})

	t.Run("should do nothing if annotation is not present", func(t *testing.T) {
		workloadWithoutAnnotation := &util.Workload{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "my-deployment",
				Namespace:   "default",
				Annotations: map[string]string{}, // No annotation
			},
		}
		deploymentWithoutAnnotation := deployment.DeepCopy()
		deploymentWithoutAnnotation.Annotations = map[string]string{}

		c := &RolloutContext{Rollout: rollout, Workload: workloadWithoutAnnotation}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(deploymentWithoutAnnotation).Build()
		err := removeRolloutProgressingAnnotation(fakeClient, c)
		assert.NoError(t, err)
	})
}

func TestRemoveBatchRelease(t *testing.T) {
	rollout := &v1beta1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "my-rollout", Namespace: "default"},
	}
	c := &RolloutContext{Rollout: rollout}

	t.Run("should return false, nil if BatchRelease not found", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		retry, err := removeBatchRelease(fakeClient, c)
		assert.NoError(t, err)
		assert.False(t, retry)
	})

	t.Run("should return true, nil if BatchRelease is being deleted", func(t *testing.T) {
		now := metav1.Now()
		br := &v1beta1.BatchRelease{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "my-rollout",
				Namespace:         "default",
				DeletionTimestamp: &now,
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(br).Build()
		retry, err := removeBatchRelease(fakeClient, c)
		assert.NoError(t, err)
		assert.True(t, retry)
	})

	t.Run("should delete BatchRelease and return true, nil", func(t *testing.T) {
		br := &v1beta1.BatchRelease{
			ObjectMeta: metav1.ObjectMeta{Name: "my-rollout", Namespace: "default"},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(br).Build()
		retry, err := removeBatchRelease(fakeClient, c)
		assert.NoError(t, err)
		assert.True(t, retry)

		err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "my-rollout", Namespace: "default"}, br)
		assert.True(t, client.IgnoreNotFound(err) == nil)
	})
}

func TestFinalizingBatchRelease(t *testing.T) {
	rollout := &v1beta1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "my-rollout", Namespace: "default"},
	}
	baseBR := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "my-rollout", Namespace: "default"},
		Spec: v1beta1.BatchReleaseSpec{
			ReleasePlan: v1beta1.ReleasePlan{
				BatchPartition: int32p(1),
			},
		},
	}

	testCases := []struct {
		name           string
		c              *RolloutContext
		existingBR     *v1beta1.BatchRelease
		expectRetry    bool
		expectError    bool
		expectedPolicy v1beta1.FinalizingPolicyType
		expectPatch    bool
	}{
		{
			name:        "BatchRelease not found",
			c:           &RolloutContext{Rollout: rollout},
			existingBR:  nil, // Not in the client
			expectRetry: false,
			expectError: false,
		},
		{
			name: "BatchRelease already completed",
			c:    &RolloutContext{Rollout: rollout},
			existingBR: &v1beta1.BatchRelease{
				ObjectMeta: metav1.ObjectMeta{Name: "my-rollout", Namespace: "default"},
				Spec:       v1beta1.BatchReleaseSpec{ReleasePlan: v1beta1.ReleasePlan{BatchPartition: nil}},
				Status:     v1beta1.BatchReleaseStatus{Phase: v1beta1.RolloutPhaseCompleted},
			},
			expectRetry: false,
			expectError: false,
		},
		{
			name: "Patch to WaitResume policy",
			c:    &RolloutContext{Rollout: rollout, WaitReady: true},
			existingBR: func() *v1beta1.BatchRelease {
				br := baseBR.DeepCopy()
				br.Spec.ReleasePlan.FinalizingPolicy = v1beta1.ImmediateFinalizingPolicyType
				return br
			}(),
			expectRetry:    true,
			expectError:    false,
			expectedPolicy: v1beta1.WaitResumeFinalizingPolicyType,
			expectPatch:    true,
		},
		{
			name: "Patch to Immediate policy",
			c:    &RolloutContext{Rollout: rollout, WaitReady: false},
			existingBR: func() *v1beta1.BatchRelease {
				br := baseBR.DeepCopy()
				br.Spec.ReleasePlan.FinalizingPolicy = v1beta1.WaitResumeFinalizingPolicyType
				return br
			}(),
			expectRetry:    true,
			expectError:    false,
			expectedPolicy: v1beta1.ImmediateFinalizingPolicyType,
			expectPatch:    true,
		},
		{
			name: "Policy already correct (WaitResume), no patch needed",
			c:    &RolloutContext{Rollout: rollout, WaitReady: true},
			existingBR: &v1beta1.BatchRelease{
				ObjectMeta: metav1.ObjectMeta{Name: "my-rollout", Namespace: "default"},
				Spec: v1beta1.BatchReleaseSpec{
					ReleasePlan: v1beta1.ReleasePlan{
						BatchPartition:   nil,
						FinalizingPolicy: v1beta1.WaitResumeFinalizingPolicyType,
					},
				},
			},
			expectRetry: true,
			expectError: false,
			expectPatch: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []runtime.Object
			if tc.existingBR != nil {
				objs = append(objs, tc.existingBR)
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

			retry, err := finalizingBatchRelease(fakeClient, tc.c)
			assert.Equal(t, tc.expectRetry, retry)
			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tc.expectPatch {
				updatedBR := &v1beta1.BatchRelease{}
				getErr := fakeClient.Get(context.TODO(), types.NamespacedName{Name: rollout.Name, Namespace: rollout.Namespace}, updatedBR)
				assert.NoError(t, getErr)
				assert.Nil(t, updatedBR.Spec.ReleasePlan.BatchPartition)
				assert.Equal(t, tc.expectedPolicy, updatedBR.Spec.ReleasePlan.FinalizingPolicy)
			}
		})
	}
}

type mockReleaseManager struct {
	client client.Client
}

func (m *mockReleaseManager) fetchClient() client.Client                         { return m.client }
func (m *mockReleaseManager) runCanary(c *RolloutContext) error                  { return nil }
func (m *mockReleaseManager) doCanaryJump(c *RolloutContext) bool                { return false }
func (m *mockReleaseManager) doCanaryFinalising(c *RolloutContext) (bool, error) { return false, nil }
func (m *mockReleaseManager) createBatchRelease(rollout *v1beta1.Rollout, rolloutID string, batch int32, isRollback bool) *v1beta1.BatchRelease {
	plan := rollout.Spec.Strategy.Canary.Steps[batch]

	var replicasValue intstr.IntOrString
	if plan.Replicas != nil {
		replicasValue = *plan.Replicas
	}

	br := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:        rollout.Name,
			Namespace:   rollout.Namespace,
			Annotations: map[string]string{"rollout-id": rolloutID},
		},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: rollout.Spec.WorkloadRef.APIVersion,
				Kind:       rollout.Spec.WorkloadRef.Kind,
				Name:       rollout.Spec.WorkloadRef.Name,
			},
			ReleasePlan: v1beta1.ReleasePlan{
				Batches: []v1beta1.ReleaseBatch{
					{CanaryReplicas: replicasValue},
				},
				BatchPartition: int32p(0),
			},
		},
	}
	return br
}

func TestRunBatchRelease(t *testing.T) {
	rollout := &v1beta1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "my-rollout", Namespace: "default", UID: "rollout-uid-12345"},
		Spec: v1beta1.RolloutSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: "apps/v1", Kind: "Deployment", Name: "my-app"},
			Strategy: v1beta1.RolloutStrategy{
				Canary: &v1beta1.CanaryStrategy{
					Steps: []v1beta1.CanaryStep{
						{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}}, // Index 0
						{Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: 5}},        // Index 1
					},
				},
			},
		},
	}
	rolloutID := string(rollout.UID)

	t.Run("should_create_new_BatchRelease_if_not_found", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		m := &mockReleaseManager{client: fakeClient}

		done, br, err := runBatchRelease(m, rollout, rolloutID, 2, false)

		assert.NoError(t, err)
		assert.False(t, done)
		assert.NotNil(t, br)

		createdBR := &v1beta1.BatchRelease{}
		getErr := fakeClient.Get(context.TODO(), types.NamespacedName{Name: rollout.Name, Namespace: rollout.Namespace}, createdBR)
		assert.NoError(t, getErr)
		assert.Equal(t, int32(5), createdBR.Spec.ReleasePlan.Batches[0].CanaryReplicas.IntVal)
		assert.Equal(t, int32(0), *createdBR.Spec.ReleasePlan.BatchPartition)
		assert.Equal(t, rolloutID, createdBR.Annotations["rollout-id"])
	})

	t.Run("should_update_existing_BatchRelease_if_spec_is_outdated", func(t *testing.T) {
		existingBR := &v1beta1.BatchRelease{
			ObjectMeta: metav1.ObjectMeta{Name: "my-rollout", Namespace: "default", Annotations: map[string]string{"rollout-id": "old-id"}},
			Spec:       v1beta1.BatchReleaseSpec{ReleasePlan: v1beta1.ReleasePlan{BatchPartition: int32p(99)}}, // old spec
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existingBR).Build()
		m := &mockReleaseManager{client: fakeClient}

		done, br, err := runBatchRelease(m, rollout, rolloutID, 1, false)

		assert.NoError(t, err)
		assert.False(t, done)
		assert.NotNil(t, br)

		updatedBR := &v1beta1.BatchRelease{}
		getErr := fakeClient.Get(context.TODO(), types.NamespacedName{Name: rollout.Name, Namespace: rollout.Namespace}, updatedBR)
		assert.NoError(t, getErr)
		assert.Equal(t, int32(0), *updatedBR.Spec.ReleasePlan.BatchPartition)
		assert.Equal(t, "10%", updatedBR.Spec.ReleasePlan.Batches[0].CanaryReplicas.StrVal)
		assert.Equal(t, rolloutID, updatedBR.Annotations["rollout-id"])
	})

	t.Run("should_return_done=true_if_BatchRelease_is_up-to_date", func(t *testing.T) {
		m := &mockReleaseManager{}
		// create a BR that matches what createBatchRelease would generate for batch 1 (index 0)
		existingBR := m.createBatchRelease(rollout, rolloutID, 0, false)

		m.client = fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(existingBR).Build()

		done, br, err := runBatchRelease(m, rollout, rolloutID, 1, false)
		assert.NoError(t, err)
		assert.True(t, done)
		assert.NotNil(t, br)
	})
}

func int32p(i int32) *int32 {
	return &i
}
