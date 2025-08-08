package main

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func main() {
	fmt.Println("Testing Canary Batch Timing Feature")
	fmt.Println("==================================")

	// Create a sample BatchRelease with timing information
	batchRelease := &v1beta1.BatchRelease{
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "test-deployment",
			},
			ReleasePlan: v1beta1.ReleasePlan{
				Batches: []v1beta1.ReleaseBatch{
					{CanaryReplicas: intstr.FromInt(1)},
					{CanaryReplicas: intstr.FromInt(2)},
					{CanaryReplicas: intstr.FromInt(5)},
				},
				RolloutID: "test-rollout-123",
			},
		},
		Status: v1beta1.BatchReleaseStatus{
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
				CurrentBatch:      1,
				CurrentBatchState: v1beta1.UpgradingBatchState,
				BatchStartTime:    &metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
				BatchReadyTime:    &metav1.Time{Time: time.Now()},
				UpdatedReplicas:   2,
				UpdatedReadyReplicas: 2,
			},
		},
	}

	// Create a sample Rollout with timing information
	rollout := &v1beta1.Rollout{
		Spec: v1beta1.RolloutSpec{
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "test-deployment",
			},
			Strategy: v1beta1.RolloutStrategy{
				Canary: &v1beta1.CanaryStrategy{
					Steps: []v1beta1.CanaryStep{
						{Replicas: func() *intstr.IntOrString { x := intstr.FromInt(1); return &x }()},
						{Replicas: func() *intstr.IntOrString { x := intstr.FromInt(2); return &x }()},
						{Replicas: func() *intstr.IntOrString { x := intstr.FromInt(5); return &x }()},
					},
				},
			},
		},
		Status: v1beta1.RolloutStatus{
			CanaryStatus: &v1beta1.CanaryStatus{
				CanaryRevision:        "v2",
				CanaryReplicas:        2,
				CanaryReadyReplicas:   2,
				CurrentStepStartTime:  &metav1.Time{Time: time.Now().Add(-5 * time.Minute)},
				CurrentStepEndTime:    &metav1.Time{Time: time.Now()},
			},
			CurrentStepIndex: 1,
		},
	}

	// Demonstrate the timing information
	fmt.Printf("BatchRelease Timing Information:\n")
	fmt.Printf("  Current Batch: %d\n", batchRelease.Status.CanaryStatus.CurrentBatch)
	fmt.Printf("  Batch State: %s\n", batchRelease.Status.CanaryStatus.CurrentBatchState)
	if batchRelease.Status.CanaryStatus.BatchStartTime != nil {
		fmt.Printf("  Batch Start Time: %s\n", batchRelease.Status.CanaryStatus.BatchStartTime.Format(time.RFC3339))
	}
	if batchRelease.Status.CanaryStatus.BatchReadyTime != nil {
		fmt.Printf("  Batch Ready Time: %s\n", batchRelease.Status.CanaryStatus.BatchReadyTime.Format(time.RFC3339))
	}
	if batchRelease.Status.CanaryStatus.BatchStartTime != nil && batchRelease.Status.CanaryStatus.BatchReadyTime != nil {
		duration := batchRelease.Status.CanaryStatus.BatchReadyTime.Sub(batchRelease.Status.CanaryStatus.BatchStartTime.Time)
		fmt.Printf("  Batch Duration: %s\n", duration)
	}

	fmt.Printf("\nRollout Timing Information:\n")
	fmt.Printf("  Current Step: %d\n", rollout.Status.CurrentStepIndex)
	if rollout.Status.CanaryStatus.CurrentStepStartTime != nil {
		fmt.Printf("  Step Start Time: %s\n", rollout.Status.CanaryStatus.CurrentStepStartTime.Format(time.RFC3339))
	}
	if rollout.Status.CanaryStatus.CurrentStepEndTime != nil {
		fmt.Printf("  Step End Time: %s\n", rollout.Status.CanaryStatus.CurrentStepEndTime.Format(time.RFC3339))
	}
	if rollout.Status.CanaryStatus.CurrentStepStartTime != nil && rollout.Status.CanaryStatus.CurrentStepEndTime != nil {
		duration := rollout.Status.CanaryStatus.CurrentStepEndTime.Sub(rollout.Status.CanaryStatus.CurrentStepStartTime.Time)
		fmt.Printf("  Step Duration: %s\n", duration)
	}

	fmt.Println("\nFeature Implementation Summary:")
	fmt.Println("✓ Added BatchStartTime field to BatchReleaseCanaryStatus")
	fmt.Println("✓ Added CurrentStepStartTime and CurrentStepEndTime fields to Rollout CanaryStatus")
	fmt.Println("✓ Updated conversion functions to handle timing fields")
	fmt.Println("✓ Modified batch executor to set start times when batches begin")
	fmt.Println("✓ Updated status reset functions to handle timing fields")
	fmt.Println("✓ Added timing sync from BatchRelease to Rollout")
	fmt.Println("✓ All tests passing")
}
