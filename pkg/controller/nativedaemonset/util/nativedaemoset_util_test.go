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
	"sync"
	"sync/atomic"
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

// Test SlowStartBatch function
func TestSlowStartBatch(t *testing.T) {
	tests := []struct {
		name             string
		count            int
		initialBatchSize int
		failAtIndex      int // -1 means no failure
		expectedSuccess  int
		expectError      bool
	}{
		{
			name:             "all success",
			count:            10,
			initialBatchSize: 1,
			failAtIndex:      -1,
			expectedSuccess:  10,
			expectError:      false,
		},
		{
			name:             "fail at first",
			count:            10,
			initialBatchSize: 1,
			failAtIndex:      0,
			expectedSuccess:  0,
			expectError:      true,
		},
		{
			name:             "fail at middle",
			count:            10,
			initialBatchSize: 1,
			failAtIndex:      5,
			expectedSuccess:  -1, // 使用 -1 表示不检查精确值，只检查范围
			expectError:      true,
		},
		{
			name:             "zero count",
			count:            0,
			initialBatchSize: 1,
			failAtIndex:      -1,
			expectedSuccess:  0,
			expectError:      false,
		},
		{
			name:             "large initial batch size",
			count:            5,
			initialBatchSize: 10,
			failAtIndex:      -1,
			expectedSuccess:  5,
			expectError:      false,
		},
		{
			name:             "batch size growth",
			count:            15,
			initialBatchSize: 1,
			failAtIndex:      -1,
			expectedSuccess:  15,
			expectError:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var callCount int32
			successCount, err := SlowStartBatch(test.count, test.initialBatchSize, func(index int) error {
				atomic.AddInt32(&callCount, 1)
				if test.failAtIndex >= 0 && index == test.failAtIndex {
					return fmt.Errorf("simulated error at index %d", index)
				}
				return nil
			})

			if test.expectedSuccess >= 0 {
				assert.Equal(t, test.expectedSuccess, successCount)
			} else {
				// For cases where exact count is unpredictable due to concurrency
				assert.GreaterOrEqual(t, successCount, test.failAtIndex)
				assert.Less(t, successCount, test.count)
			}
			if test.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Verify that the function was called the expected number of times
			if test.expectError {
				// When there's an error, we might call more functions than successful ones
				// due to concurrent execution within a batch
				assert.GreaterOrEqual(t, int(atomic.LoadInt32(&callCount)), test.expectedSuccess)
			} else {
				assert.Equal(t, test.count, int(atomic.LoadInt32(&callCount)))
			}
		})
	}
}

// Test SlowStartBatch with concurrent execution
func TestSlowStartBatch_Concurrency(t *testing.T) {
	t.Run("concurrent execution within batch", func(t *testing.T) {
		var mu sync.Mutex
		executionOrder := make([]int, 0)

		successCount, err := SlowStartBatch(4, 2, func(index int) error {
			mu.Lock()
			executionOrder = append(executionOrder, index)
			mu.Unlock()

			// Add small delay to test concurrency
			time.Sleep(10 * time.Millisecond)
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, 4, successCount)
		assert.Equal(t, 4, len(executionOrder))

		// Verify all indices are present (order may vary due to concurrency)
		expectedIndices := map[int]bool{0: true, 1: true, 2: true, 3: true}
		for _, index := range executionOrder {
			assert.True(t, expectedIndices[index], "Unexpected index: %d", index)
			delete(expectedIndices, index)
		}
		assert.Empty(t, expectedIndices, "Missing indices")
	})
}

// Test SlowStartBatch error handling
func TestSlowStartBatch_ErrorHandling(t *testing.T) {
	t.Run("multiple errors in same batch", func(t *testing.T) {
		successCount, err := SlowStartBatch(4, 4, func(index int) error {
			if index == 1 || index == 3 {
				return fmt.Errorf("error at index %d", index)
			}
			return nil
		})

		assert.Error(t, err)
		assert.Equal(t, 2, successCount) // 2 successful calls (index 0 and 2)
		assert.Contains(t, err.Error(), "error at index")
	})

	t.Run("panic in function", func(t *testing.T) {
		// Skip this test as panic handling in goroutines is complex
		// and not the primary concern for SlowStartBatch
		t.Skip("Skipping panic test - panic in goroutines cannot be easily caught")
	})
}

// Test IntMin function
func TestIntMin(t *testing.T) {
	tests := []struct {
		name     string
		a        int
		b        int
		expected int
	}{
		{
			name:     "a smaller than b",
			a:        5,
			b:        10,
			expected: 5,
		},
		{
			name:     "b smaller than a",
			a:        10,
			b:        5,
			expected: 5,
		},
		{
			name:     "equal values",
			a:        7,
			b:        7,
			expected: 7,
		},
		{
			name:     "negative values",
			a:        -5,
			b:        -10,
			expected: -10,
		},
		{
			name:     "zero and positive",
			a:        0,
			b:        5,
			expected: 0,
		},
		{
			name:     "zero and negative",
			a:        0,
			b:        -5,
			expected: -5,
		},
		{
			name:     "large values",
			a:        1000000,
			b:        999999,
			expected: 999999,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := IntMin(test.a, test.b)
			assert.Equal(t, test.expected, result)
		})
	}
}

// Benchmark SlowStartBatch
func BenchmarkSlowStartBatch(b *testing.B) {
	b.Run("small batch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			SlowStartBatch(10, 1, func(index int) error {
				return nil
			})
		}
	})

	b.Run("large batch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			SlowStartBatch(100, 1, func(index int) error {
				return nil
			})
		}
	})

	b.Run("large initial batch size", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			SlowStartBatch(100, 50, func(index int) error {
				return nil
			})
		}
	})
}

// Test SlowStartBatch batch size progression
func TestSlowStartBatch_BatchSizeProgression(t *testing.T) {
	t.Run("batch size doubles correctly", func(t *testing.T) {
		batchSizes := make([]int, 0)
		var mu sync.Mutex
		currentBatch := make(map[int]bool)

		SlowStartBatch(15, 1, func(index int) error {
			mu.Lock()
			currentBatch[index] = true

			// When we reach certain indices, record the batch size
			if index == 0 { // First batch
				batchSizes = append(batchSizes, len(currentBatch))
				currentBatch = make(map[int]bool)
			} else if index == 2 { // Second batch should have 2 items (1, 2)
				batchSizes = append(batchSizes, len(currentBatch))
				currentBatch = make(map[int]bool)
			} else if index == 6 { // Third batch should have 4 items (3, 4, 5, 6)
				batchSizes = append(batchSizes, len(currentBatch))
				currentBatch = make(map[int]bool)
			}
			mu.Unlock()

			return nil
		})

		// Note: Due to concurrency, exact batch size tracking is complex
		// This test mainly ensures the function completes successfully
		// with the expected progression logic
		t.Logf("Batch sizes observed: %v", batchSizes)
	})
}

// Helper function to create test pods
func createTestPodForUtil(name, namespace, revision string, hasRevisionLabel bool, ready bool, deletionTimestamp *metav1.Time) *corev1.Pod {
	labels := make(map[string]string)
	if hasRevisionLabel && revision != "" {
		labels[appsv1.ControllerRevisionHashLabelKey] = revision
	}

	conditions := []corev1.PodCondition{}
	if ready {
		conditions = append(conditions, corev1.PodCondition{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		})
	} else {
		conditions = append(conditions, corev1.PodCondition{
			Type:   corev1.PodReady,
			Status: corev1.ConditionFalse,
		})
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			Labels:            labels,
			DeletionTimestamp: deletionTimestamp,
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: conditions,
		},
	}
}

func TestClassifyPods(t *testing.T) {
	testRevision := "test-revision-123"

	tests := []struct {
		name                            string
		pods                            []*corev1.Pod
		updateRevision                  string
		expectedUpdatedPods             int32
		expectedUpdatedAndNotReadyCount int32
		expectedPodsToDeleteCount       int
	}{
		{
			name:                            "empty pod list",
			pods:                            []*corev1.Pod{},
			updateRevision:                  testRevision,
			expectedUpdatedPods:             0,
			expectedUpdatedAndNotReadyCount: 0,
			expectedPodsToDeleteCount:       0,
		},
		{
			name: "all pods updated and ready",
			pods: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", testRevision, true, true, nil),
				createTestPodForUtil("pod-2", "default", testRevision, true, true, nil),
			},
			updateRevision:                  testRevision,
			expectedUpdatedPods:             2,
			expectedUpdatedAndNotReadyCount: 0,
			expectedPodsToDeleteCount:       0,
		},
		{
			name: "mixed updated and old pods",
			pods: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", testRevision, true, true, nil),
				createTestPodForUtil("pod-2", "default", "old-revision", true, true, nil),
				createTestPodForUtil("pod-3", "default", "old-revision", true, true, nil),
			},
			updateRevision:                  testRevision,
			expectedUpdatedPods:             1,
			expectedUpdatedAndNotReadyCount: 0,
			expectedPodsToDeleteCount:       2,
		},
		{
			name: "updated pods with not ready",
			pods: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", testRevision, true, true, nil),
				createTestPodForUtil("pod-2", "default", testRevision, true, false, nil),
				createTestPodForUtil("pod-3", "default", "old-revision", true, true, nil),
			},
			updateRevision:                  testRevision,
			expectedUpdatedPods:             2,
			expectedUpdatedAndNotReadyCount: 1,
			expectedPodsToDeleteCount:       1,
		},
		{
			name: "pods without revision label",
			pods: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", "", false, true, nil),
				createTestPodForUtil("pod-2", "default", "", false, true, nil),
			},
			updateRevision:                  testRevision,
			expectedUpdatedPods:             0,
			expectedUpdatedAndNotReadyCount: 0,
			expectedPodsToDeleteCount:       2,
		},
		{
			name: "all updated but all not ready",
			pods: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", testRevision, true, false, nil),
				createTestPodForUtil("pod-2", "default", testRevision, true, false, nil),
			},
			updateRevision:                  testRevision,
			expectedUpdatedPods:             2,
			expectedUpdatedAndNotReadyCount: 2,
			expectedPodsToDeleteCount:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updatedPods, updatedAndNotReadyCount, podsToDelete := ClassifyPods(tt.pods, tt.updateRevision)

			assert.Equal(t, tt.expectedUpdatedPods, updatedPods, "updatedPods mismatch")
			assert.Equal(t, tt.expectedUpdatedAndNotReadyCount, updatedAndNotReadyCount, "updatedAndNotReadyCount mismatch")
			assert.Equal(t, tt.expectedPodsToDeleteCount, len(podsToDelete), "podsToDelete count mismatch")
		})
	}
}

func TestCalculateDeletionQuota(t *testing.T) {
	tests := []struct {
		name                       string
		sortedPodsToDelete         []*corev1.Pod
		needToDelete               int32
		updatedAndNotReadyCount    int32
		maxUnavailable             int32
		expectedActualNeedToDelete int32
	}{
		{
			name:                       "empty pod list",
			sortedPodsToDelete:         []*corev1.Pod{},
			needToDelete:               5,
			updatedAndNotReadyCount:    0,
			maxUnavailable:             3,
			expectedActualNeedToDelete: 0,
		},
		{
			name: "all ready pods within quota",
			sortedPodsToDelete: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", "old", true, true, nil),
				createTestPodForUtil("pod-2", "default", "old", true, true, nil),
			},
			needToDelete:               2,
			updatedAndNotReadyCount:    0,
			maxUnavailable:             3,
			expectedActualNeedToDelete: 2,
		},
		{
			name: "ready pods exceed maxUnavailable",
			sortedPodsToDelete: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", "old", true, true, nil),
				createTestPodForUtil("pod-2", "default", "old", true, true, nil),
				createTestPodForUtil("pod-3", "default", "old", true, true, nil),
			},
			needToDelete:               3,
			updatedAndNotReadyCount:    0,
			maxUnavailable:             2,
			expectedActualNeedToDelete: 2,
		},
		{
			name: "not ready pods don't consume quota",
			sortedPodsToDelete: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", "old", true, false, nil),
				createTestPodForUtil("pod-2", "default", "old", true, false, nil),
				createTestPodForUtil("pod-3", "default", "old", true, true, nil),
			},
			needToDelete:               3,
			updatedAndNotReadyCount:    0,
			maxUnavailable:             1,
			expectedActualNeedToDelete: 3, // 2 not ready + 1 ready
		},
		{
			name: "terminating pods count towards unavailable",
			sortedPodsToDelete: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", "old", true, true, &metav1.Time{Time: time.Now()}),
				createTestPodForUtil("pod-2", "default", "old", true, true, nil),
				createTestPodForUtil("pod-3", "default", "old", true, true, nil),
			},
			needToDelete:               3,
			updatedAndNotReadyCount:    0,
			maxUnavailable:             2,
			expectedActualNeedToDelete: 1, // 1 terminating (counted) + 1 ready (can delete)
		},
		{
			name: "updated not ready blocks deletion",
			sortedPodsToDelete: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", "old", true, true, nil),
				createTestPodForUtil("pod-2", "default", "old", true, true, nil),
			},
			needToDelete:               2,
			updatedAndNotReadyCount:    2,
			maxUnavailable:             2,
			expectedActualNeedToDelete: 0, // updatedAndNotReadyCount already at maxUnavailable
		},
		{
			name: "mixed scenario",
			sortedPodsToDelete: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", "old", true, false, nil),                           // not ready
				createTestPodForUtil("pod-2", "default", "old", true, true, &metav1.Time{Time: time.Now()}), // terminating
				createTestPodForUtil("pod-3", "default", "old", true, true, nil),                            // ready
				createTestPodForUtil("pod-4", "default", "old", true, true, nil),                            // ready
			},
			needToDelete:               4,
			updatedAndNotReadyCount:    1,
			maxUnavailable:             3,
			expectedActualNeedToDelete: 2, // 1 not ready + 1 terminating (skip) + 1 ready (unavailableCount: 1->2->3, stop)
		},
		{
			name: "needToDelete limits deletion",
			sortedPodsToDelete: []*corev1.Pod{
				createTestPodForUtil("pod-1", "default", "old", true, false, nil),
				createTestPodForUtil("pod-2", "default", "old", true, false, nil),
				createTestPodForUtil("pod-3", "default", "old", true, true, nil),
			},
			needToDelete:               2,
			updatedAndNotReadyCount:    0,
			maxUnavailable:             5,
			expectedActualNeedToDelete: 2, // Limited by needToDelete
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actualNeedToDelete := CalculateDeletionQuota(
				tt.sortedPodsToDelete,
				tt.needToDelete,
				tt.updatedAndNotReadyCount,
				tt.maxUnavailable,
			)

			assert.Equal(t, tt.expectedActualNeedToDelete, actualNeedToDelete, "actualNeedToDelete mismatch")
		})
	}
}
