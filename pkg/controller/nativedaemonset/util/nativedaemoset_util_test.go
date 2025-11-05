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

package util

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestHasPodsBeingDeleted(t *testing.T) {
	tests := []struct {
		name     string
		pods     []*corev1.Pod
		expected bool
	}{
		{
			name:     "empty pod list",
			pods:     []*corev1.Pod{},
			expected: false,
		},
		{
			name: "no pods being deleted",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-1",
						Namespace: "default",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-2",
						Namespace: "default",
					},
				},
			},
			expected: false,
		},
		{
			name: "single pod being deleted",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "pod-1",
						Namespace:         "default",
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-2",
						Namespace: "default",
					},
				},
			},
			expected: true,
		},
		{
			name: "multiple pods being deleted",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "pod-1",
						Namespace:         "default",
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "pod-2",
						Namespace:         "default",
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
				},
			},
			expected: true,
		},
		{
			name: "all pods being deleted",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "pod-1",
						Namespace:         "default",
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
				},
			},
			expected: true,
		},
		{
			name: "pods with zero deletion timestamp (not being deleted)",
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "pod-1",
						Namespace:         "default",
						DeletionTimestamp: &metav1.Time{},
					},
				},
			},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := HasPodsBeingDeleted(test.pods)
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestCalculateDesiredUpdatedReplicas(t *testing.T) {
	tests := []struct {
		name         string
		partitionStr string
		totalPods    int
		expected     int32
		expectError  bool
	}{
		{
			name:         "valid partition - normal case",
			partitionStr: "2",
			totalPods:    5,
			expected:     3, // 5 - 2 = 3
			expectError:  false,
		},
		{
			name:         "zero partition",
			partitionStr: "0",
			totalPods:    5,
			expected:     5, // 5 - 0 = 5
			expectError:  false,
		},
		{
			name:         "partition equals total",
			partitionStr: "5",
			totalPods:    5,
			expected:     0, // 5 - 5 = 0
			expectError:  false,
		},
		{
			name:         "partition greater than total",
			partitionStr: "10",
			totalPods:    5,
			expected:     0, // max(5 - 10, 0) = 0
			expectError:  false,
		},
		{
			name:         "negative partition",
			partitionStr: "-1",
			totalPods:    5,
			expected:     6, // 5 - (-1) = 6
			expectError:  false,
		},
		{
			name:         "large partition value",
			partitionStr: "1000",
			totalPods:    10,
			expected:     0, // max(10 - 1000, 0) = 0
			expectError:  false,
		},
		{
			name:         "zero total pods",
			partitionStr: "0",
			totalPods:    0,
			expected:     0, // 0 - 0 = 0
			expectError:  false,
		},
		{
			name:         "zero total pods with partition",
			partitionStr: "5",
			totalPods:    0,
			expected:     0, // max(0 - 5, 0) = 0
			expectError:  false,
		},
		{
			name:         "invalid partition string - alphabetic",
			partitionStr: "abc",
			totalPods:    5,
			expected:     0,
			expectError:  true,
		},
		{
			name:         "invalid partition string - empty",
			partitionStr: "",
			totalPods:    5,
			expected:     0,
			expectError:  true,
		},
		{
			name:         "invalid partition string - mixed characters",
			partitionStr: "2abc",
			totalPods:    5,
			expected:     3, // fmt.Sscanf parses "2abc" as "2", so 5-2=3
			expectError:  false,
		},
		{
			name:         "partition with spaces",
			partitionStr: " 3 ",
			totalPods:    8,
			expected:     5, // fmt.Sscanf handles spaces, parses as "3", so 8-3=5
			expectError:  false,
		},
		{
			name:         "float partition value",
			partitionStr: "2.5",
			totalPods:    7,
			expected:     5, // fmt.Sscanf parses "2.5" as "2", so 7-2=5
			expectError:  false,
		},
		{
			name:         "hex format partition",
			partitionStr: "0x5",
			totalPods:    10,
			expected:     10, // fmt.Sscanf parses "0x5" as "0", so 10-0=10
			expectError:  false,
		},
		{
			name:         "very large negative partition",
			partitionStr: "-999999",
			totalPods:    5,
			expected:     1000004, // 5 - (-999999) = 1000004
			expectError:  false,
		},
		{
			name:         "special characters",
			partitionStr: "@#$",
			totalPods:    5,
			expected:     0,
			expectError:  true,
		},
		{
			name:         "single character number",
			partitionStr: "7",
			totalPods:    10,
			expected:     3, // 10 - 7 = 3
			expectError:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := CalculateDesiredUpdatedReplicas(test.partitionStr, test.totalPods)
			if test.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "invalid partition value")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, test.expected, result)
			}
		})
	}
}

func TestGetMaxUnavailable(t *testing.T) {
	tests := []struct {
		name     string
		daemon   *appsv1.DaemonSet
		expected int32
	}{
		{
			name: "default value when no rolling update config",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						// RollingUpdate is nil
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 5,
				},
			},
			expected: 1,
		},
		{
			name: "default value when rolling update is nil",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type:          appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: nil,
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 10,
				},
			},
			expected: 1,
		},
		{
			name: "default value when maxUnavailable is nil",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: nil,
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 8,
				},
			},
			expected: 1,
		},
		{
			name: "integer maxUnavailable value",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 3},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 10,
				},
			},
			expected: 3,
		},
		{
			name: "percentage maxUnavailable - 50%",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 10,
				},
			},
			expected: 5,
		},
		{
			name: "percentage maxUnavailable - 25%",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 8,
				},
			},
			expected: 2,
		},
		{
			name: "percentage maxUnavailable - 100%",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 4,
				},
			},
			expected: 4,
		},
		{
			name: "invalid percentage - fallback to default",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "invalid%"},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 10,
				},
			},
			expected: 1,
		},
		{
			name: "zero maxUnavailable - fallback to default",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 0},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 10,
				},
			},
			expected: 1,
		},
		{
			name: "negative maxUnavailable - fallback to default",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: -1},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 10,
				},
			},
			expected: 1,
		},
		{
			name: "OnDelete strategy - fallback to default",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.OnDeleteDaemonSetStrategyType,
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 10,
				},
			},
			expected: 1,
		},
		{
			name: "zero desired number scheduled",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 0,
				},
			},
			expected: 1, // fallback to default when calculation results in 0
		},
		{
			name: "percentage with odd number - rounding",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "33%"},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 7,
				},
			},
			expected: 2, // 33% of 7 = 2.31, rounds down to 2
		},
		{
			name: "large integer maxUnavailable",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1000},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 5,
				},
			},
			expected: 1000,
		},
		{
			name: "single replica with percentage",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 1,
				},
			},
			expected: 1, // 50% of 1 = 0.5, but fallback to default since result would be 0
		},
		{
			name: "empty percentage string - invalid",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: ""},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 10,
				},
			},
			expected: 1,
		},
		{
			name: "percentage without % sign - invalid",
			daemon: &appsv1.DaemonSet{
				Spec: appsv1.DaemonSetSpec{
					UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
						Type: appsv1.RollingUpdateDaemonSetStrategyType,
						RollingUpdate: &appsv1.RollingUpdateDaemonSet{
							MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "50"},
						},
					},
				},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 10,
				},
			},
			expected: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetMaxUnavailable(test.daemon)
			assert.Equal(t, test.expected, result)
		})
	}
}

// Test edge cases with different DaemonSet configurations
func TestGetMaxUnavailable_EdgeCases(t *testing.T) {
	// Test with nil daemon
	t.Run("nil daemon", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				// This is expected behavior
				t.Log("Function correctly panics with nil daemon")
			}
		}()
		GetMaxUnavailable(nil)
		t.Error("Expected panic with nil daemon")
	})

	// Test with fractional percentage results
	t.Run("fractional percentage results", func(t *testing.T) {
		daemon := &appsv1.DaemonSet{
			Spec: appsv1.DaemonSetSpec{
				UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
					Type: appsv1.RollingUpdateDaemonSetStrategyType,
					RollingUpdate: &appsv1.RollingUpdateDaemonSet{
						MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "15%"},
					},
				},
			},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 7, // 15% of 7 = 1.05, should round down to 1
			},
		}
		result := GetMaxUnavailable(daemon)
		assert.Equal(t, int32(1), result)
	})
}

// Test ApplyDeletionConstraints function
func TestApplyDeletionConstraints(t *testing.T) {
	tests := []struct {
		name              string
		needToDelete      int32
		maxUnavailable    int32
		availablePodCount int
		expected          int32
		description       string
	}{
		{
			name:              "no constraints",
			needToDelete:      5,
			maxUnavailable:    10,
			availablePodCount: 20,
			expected:          5,
			description:       "When needToDelete is less than both maxUnavailable and availablePodCount, it should return needToDelete unchanged",
		},
		{
			name:              "limited by maxUnavailable",
			needToDelete:      15,
			maxUnavailable:    10,
			availablePodCount: 20,
			expected:          10,
			description:       "When needToDelete exceeds maxUnavailable, it should return maxUnavailable",
		},
		{
			name:              "limited by available pods",
			needToDelete:      15,
			maxUnavailable:    20,
			availablePodCount: 10,
			expected:          10,
			description:       "When needToDelete exceeds availablePodCount, it should return availablePodCount",
		},
		{
			name:              "limited by both constraints",
			needToDelete:      15,
			maxUnavailable:    8,
			availablePodCount: 10,
			expected:          8,
			description:       "When needToDelete exceeds both constraints, it should return the smaller constraint",
		},
		{
			name:              "needToDelete is zero",
			needToDelete:      0,
			maxUnavailable:    10,
			availablePodCount: 20,
			expected:          0,
			description:       "When needToDelete is zero, it should return zero",
		},
		{
			name:              "maxUnavailable is zero",
			needToDelete:      5,
			maxUnavailable:    0,
			availablePodCount: 20,
			expected:          0,
			description:       "When maxUnavailable is zero, it should return zero",
		},
		{
			name:              "availablePodCount is zero",
			needToDelete:      5,
			maxUnavailable:    10,
			availablePodCount: 0,
			expected:          0,
			description:       "When availablePodCount is zero, it should return zero",
		},
		{
			name:              "all values are zero",
			needToDelete:      0,
			maxUnavailable:    0,
			availablePodCount: 0,
			expected:          0,
			description:       "When all values are zero, it should return zero",
		},
		{
			name:              "needToDelete equals maxUnavailable",
			needToDelete:      5,
			maxUnavailable:    5,
			availablePodCount: 10,
			expected:          5,
			description:       "When needToDelete equals maxUnavailable, it should return the value unchanged",
		},
		{
			name:              "needToDelete equals availablePodCount",
			needToDelete:      5,
			maxUnavailable:    10,
			availablePodCount: 5,
			expected:          5,
			description:       "When needToDelete equals availablePodCount, it should return the value unchanged",
		},
		{
			name:              "negative needToDelete",
			needToDelete:      -5,
			maxUnavailable:    10,
			availablePodCount: 20,
			expected:          -5,
			description:       "When needToDelete is negative, it should return the value unchanged (no constraints applied)",
		},
		{
			name:              "large values",
			needToDelete:      1000,
			maxUnavailable:    500,
			availablePodCount: 750,
			expected:          500,
			description:       "With large values, it should still apply constraints correctly",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ApplyDeletionConstraints(test.needToDelete, test.maxUnavailable, test.availablePodCount)
			assert.Equal(t, test.expected, result, test.description)
		})
	}
}

// Test complex scenarios
func TestComplexScenarios(t *testing.T) {
	t.Run("integration test - all functions together", func(t *testing.T) {
		// Create a daemon set with specific configuration
		daemon := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-daemon",
				Namespace: "default",
			},
			Spec: appsv1.DaemonSetSpec{
				UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
					Type: appsv1.RollingUpdateDaemonSetStrategyType,
					RollingUpdate: &appsv1.RollingUpdateDaemonSet{
						MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
					},
				},
			},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 8,
			},
		}

		// Create pods with some being deleted
		now := metav1.Now()
		pods := []*corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-1",
					Namespace: "default",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "pod-2",
					Namespace:         "default",
					DeletionTimestamp: &now,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-3",
					Namespace: "default",
				},
			},
		}

		// Test HasPodsBeingDeleted
		hasDeleting := HasPodsBeingDeleted(pods)
		assert.True(t, hasDeleting, "Should detect pods being deleted")

		// Test CalculateDesiredUpdatedReplicas
		desiredReplicas, err := CalculateDesiredUpdatedReplicas("2", len(pods))
		assert.NoError(t, err)
		assert.Equal(t, int32(1), desiredReplicas, "Should calculate correct desired replicas")

		// Test GetMaxUnavailable
		maxUnavailable := GetMaxUnavailable(daemon)
		assert.Equal(t, int32(2), maxUnavailable, "Should calculate 25% of 8 = 2")

		// Test ApplyDeletionConstraints with realistic values
		needToDelete := int32(3)
		availablePods := 5
		constrainedValue := ApplyDeletionConstraints(needToDelete, maxUnavailable, availablePods)
		assert.Equal(t, int32(2), constrainedValue, "Should be limited by maxUnavailable")
	})

	t.Run("boundary conditions", func(t *testing.T) {
		// Test with minimum values
		minDaemon := &appsv1.DaemonSet{
			Spec: appsv1.DaemonSetSpec{
				UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
					Type: appsv1.RollingUpdateDaemonSetStrategyType,
					RollingUpdate: &appsv1.RollingUpdateDaemonSet{
						MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
					},
				},
			},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 1,
			},
		}

		result := GetMaxUnavailable(minDaemon)
		assert.Equal(t, int32(1), result)

		// Test CalculateDesiredUpdatedReplicas with edge cases
		testCases := []struct {
			partition   string
			totalPods   int
			expected    int32
			shouldError bool
		}{
			{"0", 1, 1, false},
			{"1", 1, 0, false},
			{"999", 1, 0, false},
			{"", 1, 0, true},
		}

		for _, tc := range testCases {
			result, err := CalculateDesiredUpdatedReplicas(tc.partition, tc.totalPods)
			if tc.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		}
	})
}

// Test with various pod states
func TestHasPodsBeingDeleted_VariousPodStates(t *testing.T) {
	t.Run("pods with different timestamps", func(t *testing.T) {
		now := time.Now()
		pastTime := metav1.NewTime(now.Add(-1 * time.Hour))
		futureTime := metav1.NewTime(now.Add(1 * time.Hour))

		pods := []*corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "pod-past",
					Namespace:         "default",
					DeletionTimestamp: &pastTime,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "pod-future",
					Namespace:         "default",
					DeletionTimestamp: &futureTime,
				},
			},
		}

		result := HasPodsBeingDeleted(pods)
		assert.True(t, result, "Should detect pods being deleted regardless of timestamp")
	})

	t.Run("pod with finalizers and deletion timestamp", func(t *testing.T) {
		now := metav1.Now()
		pods := []*corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "pod-with-finalizers",
					Namespace:         "default",
					DeletionTimestamp: &now,
					Finalizers:        []string{"example.com/finalizer"},
				},
			},
		}

		result := HasPodsBeingDeleted(pods)
		assert.True(t, result, "Should detect pods being deleted even with finalizers")
	})
}

// Test error handling and logging behavior
func TestErrorHandlingAndLogging(t *testing.T) {
	t.Run("CalculateDesiredUpdatedReplicas error formatting", func(t *testing.T) {
		_, err := CalculateDesiredUpdatedReplicas("invalid", 5)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid partition value invalid")
		assert.Contains(t, err.Error(), "expected integer")
	})

	t.Run("various invalid partition formats", func(t *testing.T) {
		invalidPartitions := []string{
			"abc123",
			"12.34.56",
			"infinity",
			"NaN",
			"null",
			"undefined",
			"true",
			"false",
		}

		for _, partition := range invalidPartitions {
			t.Run("partition_"+partition, func(t *testing.T) {
				result, err := CalculateDesiredUpdatedReplicas(partition, 10)
				// Some of these may actually parse partially (like "abc123" -> 0)
				// We check that either it errors or returns a reasonable value
				if err == nil {
					assert.GreaterOrEqual(t, result, int32(0), "Result should be non-negative")
					assert.LessOrEqual(t, result, int32(10), "Result should not exceed total pods")
				} else {
					assert.Contains(t, err.Error(), "invalid partition value")
				}
			})
		}
	})
}

// Benchmark tests to ensure performance
func BenchmarkHasPodsBeingDeleted(b *testing.B) {
	// Create a large slice of pods
	pods := make([]*corev1.Pod, 1000)
	now := metav1.Now()

	for i := 0; i < 1000; i++ {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-" + string(rune(i)),
				Namespace: "default",
			},
		}
		// Make some pods have deletion timestamp
		if i%10 == 0 {
			pod.DeletionTimestamp = &now
		}
		pods[i] = pod
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		HasPodsBeingDeleted(pods)
	}
}

func BenchmarkCalculateDesiredUpdatedReplicas(b *testing.B) {
	testCases := []struct {
		partition string
		totalPods int
	}{
		{"0", 100},
		{"50", 100},
		{"99", 100},
		{"10", 1000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, tc := range testCases {
			CalculateDesiredUpdatedReplicas(tc.partition, tc.totalPods)
		}
	}
}

func BenchmarkApplyDeletionConstraints(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ApplyDeletionConstraints(5, 3, 10)
		ApplyDeletionConstraints(10, 15, 7)
		ApplyDeletionConstraints(20, 5, 25)
	}
}

// Test SortPodsForDeletion function
func TestSortPodsForDeletion(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		pods     []*corev1.Pod
		expected []string // expected order of pod names after sorting
	}{
		{
			name: "pending pods first",
			pods: []*corev1.Pod{
				createPodWithStatus("pod-running", corev1.PodRunning, corev1.ConditionTrue, "", now.Add(-1*time.Hour)),
				createPodWithStatus("pod-pending", corev1.PodPending, corev1.ConditionFalse, "", now.Add(-2*time.Hour)),
				createPodWithStatus("pod-running2", corev1.PodRunning, corev1.ConditionTrue, "", now.Add(-3*time.Hour)),
			},
			expected: []string{"pod-pending", "pod-running", "pod-running2"},
		},
		{
			name: "not ready pods before ready pods",
			pods: []*corev1.Pod{
				createPodWithStatus("pod-ready", corev1.PodRunning, corev1.ConditionTrue, "", now.Add(-1*time.Hour)),
				createPodWithStatus("pod-not-ready", corev1.PodRunning, corev1.ConditionFalse, "", now.Add(-2*time.Hour)),
				createPodWithStatus("pod-unknown", corev1.PodRunning, corev1.ConditionUnknown, "", now.Add(-3*time.Hour)),
			},
			expected: []string{"pod-not-ready", "pod-unknown", "pod-ready"},
		},
		{
			name: "low deletion cost pods first",
			pods: []*corev1.Pod{
				createPodWithStatus("pod-low-cost", corev1.PodRunning, corev1.ConditionTrue, "10", now.Add(-1*time.Hour)),
				createPodWithStatus("pod-high-cost", corev1.PodRunning, corev1.ConditionTrue, "100", now.Add(-2*time.Hour)),
				createPodWithStatus("pod-no-cost", corev1.PodRunning, corev1.ConditionTrue, "", now.Add(-3*time.Hour)),
			},
			expected: []string{"pod-no-cost", "pod-low-cost", "pod-high-cost"},
		},
		{
			name: "newer pods before older pods (same readiness and cost)",
			pods: []*corev1.Pod{
				createPodWithStatus("pod-old", corev1.PodRunning, corev1.ConditionTrue, "", now.Add(-3*time.Hour)),
				createPodWithStatus("pod-new", corev1.PodRunning, corev1.ConditionTrue, "", now.Add(-1*time.Hour)),
				createPodWithStatus("pod-middle", corev1.PodRunning, corev1.ConditionTrue, "", now.Add(-2*time.Hour)),
			},
			expected: []string{"pod-new", "pod-middle", "pod-old"},
		},
		{
			name: "complex priority ordering",
			pods: []*corev1.Pod{
				createPodWithStatus("pod-ready-old", corev1.PodRunning, corev1.ConditionTrue, "", now.Add(-5*time.Hour)),
				createPodWithStatus("pod-pending-new", corev1.PodPending, corev1.ConditionFalse, "", now.Add(-1*time.Hour)),
				createPodWithStatus("pod-not-ready-middle", corev1.PodRunning, corev1.ConditionFalse, "", now.Add(-3*time.Hour)),
				createPodWithStatus("pod-high-cost-ready", corev1.PodRunning, corev1.ConditionTrue, "200", now.Add(-4*time.Hour)),
				createPodWithStatus("pod-ready-new", corev1.PodRunning, corev1.ConditionTrue, "", now.Add(-2*time.Hour)),
			},
			expected: []string{"pod-pending-new", "pod-not-ready-middle", "pod-ready-new", "pod-ready-old", "pod-high-cost-ready"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Make a copy to avoid modifying the original slice
			podsCopy := make([]*corev1.Pod, len(test.pods))
			copy(podsCopy, test.pods)

			SortPodsForDeletion(podsCopy)

			actualOrder := make([]string, len(podsCopy))
			for i, pod := range podsCopy {
				actualOrder[i] = pod.Name
			}

			assert.Equal(t, test.expected, actualOrder)
		})
	}
}

// Test GetPodReadiness function
func TestGetPodReadiness(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name: "pod ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "pod not ready",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "pod ready status unknown",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionUnknown,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "no ready condition",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodScheduled,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "no conditions",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{},
				},
			},
			expected: false,
		},
		{
			name: "multiple conditions with ready true",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodScheduled,
							Status: corev1.ConditionTrue,
						},
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
						{
							Type:   corev1.PodInitialized,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetPodReadiness(test.pod)
			assert.Equal(t, test.expected, result)
		})
	}
}

// Test GetPodDeletionCost function
func TestGetPodDeletionCost(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected int32
	}{
		{
			name: "no annotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: nil,
				},
			},
			expected: 0,
		},
		{
			name: "empty annotations",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			expected: 0,
		},
		{
			name: "no deletion cost annotation",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"other-annotation": "value",
					},
				},
			},
			expected: 0,
		},
		{
			name: "valid positive deletion cost",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PodDeletionCostAnnotation: "100",
					},
				},
			},
			expected: 100,
		},
		{
			name: "valid negative deletion cost",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PodDeletionCostAnnotation: "-50",
					},
				},
			},
			expected: -50,
		},
		{
			name: "zero deletion cost",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PodDeletionCostAnnotation: "0",
					},
				},
			},
			expected: 0,
		},
		{
			name: "invalid deletion cost - non-numeric",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						PodDeletionCostAnnotation: "invalid",
					},
				},
			},
			expected: 0,
		},
		{
			name: "invalid deletion cost - float",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						PodDeletionCostAnnotation: "10.5",
					},
				},
			},
			expected: 0, // ParseInt will fail on float, return 0
		},
		{
			name: "invalid deletion cost - empty string",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					Annotations: map[string]string{
						PodDeletionCostAnnotation: "",
					},
				},
			},
			expected: 0,
		},
		{
			name: "large deletion cost",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PodDeletionCostAnnotation: "2147483647", // max int32
					},
				},
			},
			expected: 2147483647,
		},
		{
			name: "large negative deletion cost",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						PodDeletionCostAnnotation: "-2147483648", // min int32
					},
				},
			},
			expected: -2147483648,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := GetPodDeletionCost(test.pod)
			assert.Equal(t, test.expected, result)
		})
	}
}

// Helper function to create pods with specific status for testing
func createPodWithStatus(name string, phase corev1.PodPhase, readyStatus corev1.ConditionStatus, deletionCost string, creationTime time.Time) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			CreationTimestamp: metav1.NewTime(creationTime),
			Annotations:       make(map[string]string),
		},
		Status: corev1.PodStatus{
			Phase: phase,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: readyStatus,
				},
			},
		},
	}

	if deletionCost != "" {
		pod.Annotations[PodDeletionCostAnnotation] = deletionCost
	}

	return pod
}

// Benchmark for SortPodsForDeletion
func BenchmarkSortPodsForDeletion(b *testing.B) {
	now := time.Now()
	pods := make([]*corev1.Pod, 100)

	for i := 0; i < 100; i++ {
		phase := corev1.PodRunning
		if i%10 == 0 {
			phase = corev1.PodPending
		}

		readyStatus := corev1.ConditionTrue
		if i%5 == 0 {
			readyStatus = corev1.ConditionFalse
		}

		deletionCost := ""
		if i%7 == 0 {
			deletionCost = "100"
		}

		pods[i] = createPodWithStatus(
			fmt.Sprintf("pod-%d", i),
			phase,
			readyStatus,
			deletionCost,
			now.Add(-time.Duration(i)*time.Minute),
		)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Make a copy for each iteration
		podsCopy := make([]*corev1.Pod, len(pods))
		copy(podsCopy, pods)
		SortPodsForDeletion(podsCopy)
	}
}
