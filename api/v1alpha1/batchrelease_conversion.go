package v1alpha1

import (
	"fmt"

	"github.com/openkruise/rollouts/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

func (src *BatchRelease) ConvertTo(dstRaw conversion.Hub) error {
	switch t := dstRaw.(type) {
	case *v1beta1.BatchRelease:
		dst := dstRaw.(*v1beta1.BatchRelease)
		dst.ObjectMeta = src.ObjectMeta

		// batchrelease spec conversion
		dst.Spec.TargetRef.WorkloadRef = &v1beta1.WorkloadRef{
			APIVersion: src.Spec.TargetRef.WorkloadRef.APIVersion,
			Kind:       src.Spec.TargetRef.WorkloadRef.Kind,
			Name:       src.Spec.TargetRef.WorkloadRef.Name,
		}
		batches := make([]v1beta1.ReleaseBatch, len(src.Spec.ReleasePlan.Batches))
		for i, v := range src.Spec.ReleasePlan.Batches {
			batches[i].CanaryReplicas = v.CanaryReplicas
		}
		dst.Spec.ReleasePlan = v1beta1.ReleasePlan{
			Batches:                  batches,
			BatchPartition:           src.Spec.ReleasePlan.BatchPartition,
			RolloutID:                src.Spec.ReleasePlan.RolloutID,
			FailureThreshold:         src.Spec.ReleasePlan.FailureThreshold,
			FinalizingPolicy:         v1beta1.FinalizingPolicyType(src.Spec.ReleasePlan.FinalizingPolicy),
			PatchPodTemplateMetadata: (*v1beta1.PatchPodTemplateMetadata)(src.Spec.ReleasePlan.PatchPodTemplateMetadata),
		}

		// batchrelease status conversion
		conditions := make([]v1beta1.RolloutCondition, len(src.Status.Conditions))
		for i, v := range src.Status.Conditions {
			conditions[i] = v1beta1.RolloutCondition{
				Type:               v1beta1.RolloutConditionType(v.Type),
				Status:             v.Status,
				LastUpdateTime:     v.LastUpdateTime,
				LastTransitionTime: v.LastTransitionTime,
				Reason:             v.Reason,
				Message:            v.Message,
			}
		}
		dst.Status = v1beta1.BatchReleaseStatus{
			Conditions: conditions,
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
				CurrentBatchState:    v1beta1.BatchReleaseBatchStateType(src.Status.CanaryStatus.CurrentBatchState),
				CurrentBatch:         src.Status.CanaryStatus.CurrentBatch,
				BatchReadyTime:       src.Status.CanaryStatus.BatchReadyTime,
				UpdatedReplicas:      src.Status.CanaryStatus.UpdatedReplicas,
				UpdatedReadyReplicas: src.Status.CanaryStatus.UpdatedReadyReplicas,
				NoNeedUpdateReplicas: src.Status.CanaryStatus.NoNeedUpdateReplicas,
			},
			StableRevision:           src.Status.StableRevision,
			UpdateRevision:           src.Status.UpdateRevision,
			ObservedGeneration:       src.Status.ObservedGeneration,
			ObservedRolloutID:        src.Status.ObservedRolloutID,
			ObservedWorkloadReplicas: src.Status.ObservedWorkloadReplicas,
			CollisionCount:           src.Status.CollisionCount,
			ObservedReleasePlanHash:  src.Status.ObservedReleasePlanHash,
			Phase:                    v1beta1.RolloutPhase(src.Status.Phase),
		}
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
	return nil
}

func (dst *BatchRelease) ConvertFrom(srcRaw conversion.Hub) error {
	switch t := srcRaw.(type) {
	case *v1beta1.BatchRelease:
		src := srcRaw.(*v1beta1.BatchRelease)
		dst.ObjectMeta = src.ObjectMeta

		// batchrelease spec conversion
		dst.Spec.TargetRef.WorkloadRef = &WorkloadRef{
			APIVersion: src.Spec.TargetRef.WorkloadRef.APIVersion,
			Kind:       src.Spec.TargetRef.WorkloadRef.Kind,
			Name:       src.Spec.TargetRef.WorkloadRef.Name,
		}
		batches := make([]ReleaseBatch, len(src.Spec.ReleasePlan.Batches))
		for i, v := range src.Spec.ReleasePlan.Batches {
			batches[i].CanaryReplicas = v.CanaryReplicas
		}
		dst.Spec.ReleasePlan = ReleasePlan{
			Batches:                  batches,
			BatchPartition:           src.Spec.ReleasePlan.BatchPartition,
			RolloutID:                src.Spec.ReleasePlan.RolloutID,
			FailureThreshold:         src.Spec.ReleasePlan.FailureThreshold,
			FinalizingPolicy:         FinalizingPolicyType(src.Spec.ReleasePlan.FinalizingPolicy),
			PatchPodTemplateMetadata: (*PatchPodTemplateMetadata)(src.Spec.ReleasePlan.PatchPodTemplateMetadata),
		}

		// batchrelease status conversion
		conditions := make([]RolloutCondition, len(src.Status.Conditions))
		for i, v := range src.Status.Conditions {
			conditions[i] = RolloutCondition{
				Type:               RolloutConditionType(v.Type),
				Status:             v.Status,
				LastUpdateTime:     v.LastUpdateTime,
				LastTransitionTime: v.LastTransitionTime,
				Reason:             v.Reason,
				Message:            v.Message,
			}
		}
		dst.Status = BatchReleaseStatus{
			Conditions: conditions,
			CanaryStatus: BatchReleaseCanaryStatus{
				CurrentBatchState:    BatchReleaseBatchStateType(src.Status.CanaryStatus.CurrentBatchState),
				CurrentBatch:         src.Status.CanaryStatus.CurrentBatch,
				BatchReadyTime:       src.Status.CanaryStatus.BatchReadyTime,
				UpdatedReplicas:      src.Status.CanaryStatus.UpdatedReplicas,
				UpdatedReadyReplicas: src.Status.CanaryStatus.UpdatedReadyReplicas,
				NoNeedUpdateReplicas: src.Status.CanaryStatus.NoNeedUpdateReplicas,
			},
			StableRevision:           src.Status.StableRevision,
			UpdateRevision:           src.Status.UpdateRevision,
			ObservedGeneration:       src.Status.ObservedGeneration,
			ObservedRolloutID:        src.Status.ObservedRolloutID,
			ObservedWorkloadReplicas: src.Status.ObservedWorkloadReplicas,
			CollisionCount:           src.Status.CollisionCount,
			ObservedReleasePlanHash:  src.Status.ObservedReleasePlanHash,
			Phase:                    RolloutPhase(src.Status.Phase),
		}
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
	return nil
}
