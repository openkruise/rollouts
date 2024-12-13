/*
Copyright 2023 The Kruise Authors.

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

package v1alpha1

import (
	"fmt"

	"strings"

	"github.com/openkruise/rollouts/api/v1beta1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

func (src *Rollout) ConvertTo(dst conversion.Hub) error {
	switch t := dst.(type) {
	case *v1beta1.Rollout:
		obj := dst.(*v1beta1.Rollout)
		obj.ObjectMeta = src.ObjectMeta
		obj.Spec = v1beta1.RolloutSpec{}
		srcSpec := src.Spec
		obj.Spec.WorkloadRef = v1beta1.ObjectRef{
			APIVersion: srcSpec.ObjectRef.WorkloadRef.APIVersion,
			Kind:       srcSpec.ObjectRef.WorkloadRef.Kind,
			Name:       srcSpec.ObjectRef.WorkloadRef.Name,
		}
		obj.Spec.Disabled = srcSpec.Disabled
		obj.Spec.Strategy = v1beta1.RolloutStrategy{
			Paused: srcSpec.Strategy.Paused,
			Canary: &v1beta1.CanaryStrategy{
				FailureThreshold: srcSpec.Strategy.Canary.FailureThreshold,
			},
		}
		for _, step := range srcSpec.Strategy.Canary.Steps {
			o := v1beta1.CanaryStep{
				TrafficRoutingStrategy: ConversionToV1beta1TrafficRoutingStrategy(step.TrafficRoutingStrategy),
				Replicas:               step.Replicas,
				Pause:                  v1beta1.RolloutPause{Duration: step.Pause.Duration},
			}
			if step.Replicas == nil && step.Weight != nil {
				o.Replicas = &intstr.IntOrString{
					Type:   intstr.String,
					StrVal: fmt.Sprintf("%d", *step.Weight) + "%",
				}
			}
			obj.Spec.Strategy.Canary.Steps = append(obj.Spec.Strategy.Canary.Steps, o)
		}
		for _, ref := range srcSpec.Strategy.Canary.TrafficRoutings {
			o := ConversionToV1beta1TrafficRoutingRef(ref)
			obj.Spec.Strategy.Canary.TrafficRoutings = append(obj.Spec.Strategy.Canary.TrafficRoutings, o)
		}
		if srcSpec.Strategy.Canary.PatchPodTemplateMetadata != nil {
			obj.Spec.Strategy.Canary.PatchPodTemplateMetadata = &v1beta1.PatchPodTemplateMetadata{
				Annotations: map[string]string{},
				Labels:      map[string]string{},
			}
			for k, v := range srcSpec.Strategy.Canary.PatchPodTemplateMetadata.Annotations {
				obj.Spec.Strategy.Canary.PatchPodTemplateMetadata.Annotations[k] = v
			}
			for k, v := range srcSpec.Strategy.Canary.PatchPodTemplateMetadata.Labels {
				obj.Spec.Strategy.Canary.PatchPodTemplateMetadata.Labels[k] = v
			}
		}
		if !strings.EqualFold(src.Annotations[RolloutStyleAnnotation], string(PartitionRollingStyle)) {
			obj.Spec.Strategy.Canary.EnableExtraWorkloadForCanary = true
		}
		if src.Annotations[TrafficRoutingAnnotation] != "" {
			obj.Spec.Strategy.Canary.TrafficRoutingRef = src.Annotations[TrafficRoutingAnnotation]
		}

		// status
		obj.Status = v1beta1.RolloutStatus{
			ObservedGeneration: src.Status.ObservedGeneration,
			Phase:              v1beta1.RolloutPhase(src.Status.Phase),
			Message:            src.Status.Message,
		}
		for _, cond := range src.Status.Conditions {
			o := v1beta1.RolloutCondition{
				Type:               v1beta1.RolloutConditionType(cond.Type),
				Status:             cond.Status,
				LastUpdateTime:     cond.LastUpdateTime,
				LastTransitionTime: cond.LastTransitionTime,
				Reason:             cond.Reason,
				Message:            cond.Message,
			}
			obj.Status.Conditions = append(obj.Status.Conditions, o)
		}
		if src.Status.CanaryStatus == nil {
			return nil
		}
		obj.Status.CanaryStatus = &v1beta1.CanaryStatus{
			CommonStatus: v1beta1.CommonStatus{
				ObservedWorkloadGeneration: src.Status.CanaryStatus.ObservedWorkloadGeneration,
				ObservedRolloutID:          src.Status.CanaryStatus.ObservedRolloutID,
				RolloutHash:                src.Status.CanaryStatus.RolloutHash,
				StableRevision:             src.Status.CanaryStatus.StableRevision,
				PodTemplateHash:            src.Status.CanaryStatus.PodTemplateHash,
				CurrentStepIndex:           src.Status.CanaryStatus.CurrentStepIndex,
				CurrentStepState:           v1beta1.CanaryStepState(src.Status.CanaryStatus.CurrentStepState),
				Message:                    src.Status.CanaryStatus.Message,
				LastUpdateTime:             src.Status.CanaryStatus.LastUpdateTime,
				FinalisingStep:             v1beta1.FinalisingStepType(src.Status.CanaryStatus.FinalisingStep),
				NextStepIndex:              src.Status.CanaryStatus.NextStepIndex,
			},
			CanaryRevision:      src.Status.CanaryStatus.CanaryRevision,
			CanaryReplicas:      src.Status.CanaryStatus.CanaryReplicas,
			CanaryReadyReplicas: src.Status.CanaryStatus.CanaryReadyReplicas,
		}
		return nil
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
}

func ConversionToV1beta1TrafficRoutingRef(src TrafficRoutingRef) (dst v1beta1.TrafficRoutingRef) {
	dst.Service = src.Service
	dst.GracePeriodSeconds = src.GracePeriodSeconds
	if src.Ingress != nil {
		dst.Ingress = &v1beta1.IngressTrafficRouting{
			ClassType: src.Ingress.ClassType,
			Name:      src.Ingress.Name,
		}
	}
	if src.Gateway != nil {
		dst.Gateway = &v1beta1.GatewayTrafficRouting{
			HTTPRouteName: src.Gateway.HTTPRouteName,
		}
	}
	for _, ref := range src.CustomNetworkRefs {
		obj := v1beta1.ObjectRef{
			APIVersion: ref.APIVersion,
			Kind:       ref.Kind,
			Name:       ref.Name,
		}
		dst.CustomNetworkRefs = append(dst.CustomNetworkRefs, obj)
	}
	return dst
}

func ConversionToV1beta1TrafficRoutingStrategy(src TrafficRoutingStrategy) (dst v1beta1.TrafficRoutingStrategy) {
	if src.Weight != nil {
		dst.Traffic = utilpointer.String(fmt.Sprintf("%d", *src.Weight) + "%")
	}
	dst.RequestHeaderModifier = src.RequestHeaderModifier
	for _, match := range src.Matches {
		obj := v1beta1.HttpRouteMatch{
			Headers: match.Headers,
		}
		dst.Matches = append(dst.Matches, obj)
	}
	return dst
}

func (dst *Rollout) ConvertFrom(src conversion.Hub) error {
	switch t := src.(type) {
	case *v1beta1.Rollout:
		srcV1beta1 := src.(*v1beta1.Rollout)
		dst.ObjectMeta = srcV1beta1.ObjectMeta
		if !srcV1beta1.Spec.Strategy.IsCanaryStragegy() {
			// only v1beta1 supports bluegreen strategy
			// Don't log the message because it will print too often
			return nil
		}
		// spec
		dst.Spec = RolloutSpec{
			ObjectRef: ObjectRef{
				WorkloadRef: &WorkloadRef{
					APIVersion: srcV1beta1.Spec.WorkloadRef.APIVersion,
					Kind:       srcV1beta1.Spec.WorkloadRef.Kind,
					Name:       srcV1beta1.Spec.WorkloadRef.Name,
				},
			},
			Strategy: RolloutStrategy{
				Paused: srcV1beta1.Spec.Strategy.Paused,
				Canary: &CanaryStrategy{
					FailureThreshold: srcV1beta1.Spec.Strategy.Canary.FailureThreshold,
				},
			},
			Disabled: srcV1beta1.Spec.Disabled,
		}
		for _, step := range srcV1beta1.Spec.Strategy.Canary.Steps {
			obj := CanaryStep{
				TrafficRoutingStrategy: ConversionToV1alpha1TrafficRoutingStrategy(step.TrafficRoutingStrategy),
				Replicas:               step.Replicas,
				Pause:                  RolloutPause{Duration: step.Pause.Duration},
			}
			dst.Spec.Strategy.Canary.Steps = append(dst.Spec.Strategy.Canary.Steps, obj)
		}
		for _, ref := range srcV1beta1.Spec.Strategy.Canary.TrafficRoutings {
			obj := ConversionToV1alpha1TrafficRoutingRef(ref)
			dst.Spec.Strategy.Canary.TrafficRoutings = append(dst.Spec.Strategy.Canary.TrafficRoutings, obj)
		}
		if srcV1beta1.Spec.Strategy.Canary.PatchPodTemplateMetadata != nil {
			dst.Spec.Strategy.Canary.PatchPodTemplateMetadata = &PatchPodTemplateMetadata{
				Annotations: map[string]string{},
				Labels:      map[string]string{},
			}
			for k, v := range srcV1beta1.Spec.Strategy.Canary.PatchPodTemplateMetadata.Annotations {
				dst.Spec.Strategy.Canary.PatchPodTemplateMetadata.Annotations[k] = v
			}
			for k, v := range srcV1beta1.Spec.Strategy.Canary.PatchPodTemplateMetadata.Labels {
				dst.Spec.Strategy.Canary.PatchPodTemplateMetadata.Labels[k] = v
			}
		}
		if dst.Annotations == nil {
			dst.Annotations = map[string]string{}
		}
		if srcV1beta1.Spec.Strategy.Canary.EnableExtraWorkloadForCanary {
			dst.Annotations[RolloutStyleAnnotation] = strings.ToLower(string(CanaryRollingStyle))
		} else {
			dst.Annotations[RolloutStyleAnnotation] = strings.ToLower(string(PartitionRollingStyle))
		}
		if srcV1beta1.Spec.Strategy.Canary.TrafficRoutingRef != "" {
			dst.Annotations[TrafficRoutingAnnotation] = srcV1beta1.Spec.Strategy.Canary.TrafficRoutingRef
		}

		// status
		dst.Status = RolloutStatus{
			ObservedGeneration: srcV1beta1.Status.ObservedGeneration,
			Phase:              RolloutPhase(srcV1beta1.Status.Phase),
			Message:            srcV1beta1.Status.Message,
		}
		for _, cond := range srcV1beta1.Status.Conditions {
			obj := RolloutCondition{
				Type:               RolloutConditionType(cond.Type),
				Status:             cond.Status,
				LastUpdateTime:     cond.LastUpdateTime,
				LastTransitionTime: cond.LastTransitionTime,
				Reason:             cond.Reason,
				Message:            cond.Message,
			}
			dst.Status.Conditions = append(dst.Status.Conditions, obj)
		}
		if srcV1beta1.Status.CanaryStatus == nil {
			return nil
		}
		dst.Status.CanaryStatus = &CanaryStatus{
			ObservedWorkloadGeneration: srcV1beta1.Status.CanaryStatus.ObservedWorkloadGeneration,
			ObservedRolloutID:          srcV1beta1.Status.CanaryStatus.ObservedRolloutID,
			RolloutHash:                srcV1beta1.Status.CanaryStatus.RolloutHash,
			StableRevision:             srcV1beta1.Status.CanaryStatus.StableRevision,
			CanaryRevision:             srcV1beta1.Status.CanaryStatus.CanaryRevision,
			PodTemplateHash:            srcV1beta1.Status.CanaryStatus.PodTemplateHash,
			CanaryReplicas:             srcV1beta1.Status.CanaryStatus.CanaryReplicas,
			CanaryReadyReplicas:        srcV1beta1.Status.CanaryStatus.CanaryReadyReplicas,
			CurrentStepIndex:           srcV1beta1.Status.CanaryStatus.CurrentStepIndex,
			CurrentStepState:           CanaryStepState(srcV1beta1.Status.CanaryStatus.CurrentStepState),
			Message:                    srcV1beta1.Status.CanaryStatus.Message,
			LastUpdateTime:             srcV1beta1.Status.CanaryStatus.LastUpdateTime,
			FinalisingStep:             FinalizeStateType(srcV1beta1.Status.CanaryStatus.FinalisingStep),
			NextStepIndex:              srcV1beta1.Status.CanaryStatus.NextStepIndex,
		}
		return nil
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
}

func ConversionToV1alpha1TrafficRoutingStrategy(src v1beta1.TrafficRoutingStrategy) (dst TrafficRoutingStrategy) {
	if src.Traffic != nil {
		is := intstr.FromString(*src.Traffic)
		weight, _ := intstr.GetScaledValueFromIntOrPercent(&is, 100, true)
		dst.Weight = utilpointer.Int32(int32(weight))
	}
	dst.RequestHeaderModifier = src.RequestHeaderModifier
	for _, match := range src.Matches {
		obj := HttpRouteMatch{
			Headers: match.Headers,
		}
		dst.Matches = append(dst.Matches, obj)
	}
	return dst
}

func ConversionToV1alpha1TrafficRoutingRef(src v1beta1.TrafficRoutingRef) (dst TrafficRoutingRef) {
	dst.Service = src.Service
	dst.GracePeriodSeconds = src.GracePeriodSeconds
	if src.Ingress != nil {
		dst.Ingress = &IngressTrafficRouting{
			ClassType: src.Ingress.ClassType,
			Name:      src.Ingress.Name,
		}
	}
	if src.Gateway != nil {
		dst.Gateway = &GatewayTrafficRouting{
			HTTPRouteName: src.Gateway.HTTPRouteName,
		}
	}
	for _, ref := range src.CustomNetworkRefs {
		obj := CustomNetworkRef{
			APIVersion: ref.APIVersion,
			Kind:       ref.Kind,
			Name:       ref.Name,
		}
		dst.CustomNetworkRefs = append(dst.CustomNetworkRefs, obj)
	}
	return dst
}

func (src *BatchRelease) ConvertTo(dst conversion.Hub) error {
	switch t := dst.(type) {
	case *v1beta1.BatchRelease:
		obj := dst.(*v1beta1.BatchRelease)
		obj.ObjectMeta = src.ObjectMeta
		obj.Spec = v1beta1.BatchReleaseSpec{}
		srcSpec := src.Spec
		obj.Spec.WorkloadRef = v1beta1.ObjectRef{
			APIVersion: srcSpec.TargetRef.WorkloadRef.APIVersion,
			Kind:       srcSpec.TargetRef.WorkloadRef.Kind,
			Name:       srcSpec.TargetRef.WorkloadRef.Name,
		}
		obj.Spec.ReleasePlan = v1beta1.ReleasePlan{
			BatchPartition:   srcSpec.ReleasePlan.BatchPartition,
			RolloutID:        srcSpec.ReleasePlan.RolloutID,
			FailureThreshold: srcSpec.ReleasePlan.FailureThreshold,
			FinalizingPolicy: v1beta1.FinalizingPolicyType(srcSpec.ReleasePlan.FinalizingPolicy),
		}
		for _, batch := range srcSpec.ReleasePlan.Batches {
			o := v1beta1.ReleaseBatch{
				CanaryReplicas: batch.CanaryReplicas,
			}
			obj.Spec.ReleasePlan.Batches = append(obj.Spec.ReleasePlan.Batches, o)
		}
		if srcSpec.ReleasePlan.PatchPodTemplateMetadata != nil {
			obj.Spec.ReleasePlan.PatchPodTemplateMetadata = &v1beta1.PatchPodTemplateMetadata{
				Annotations: map[string]string{},
				Labels:      map[string]string{},
			}
			for k, v := range srcSpec.ReleasePlan.PatchPodTemplateMetadata.Annotations {
				obj.Spec.ReleasePlan.PatchPodTemplateMetadata.Annotations[k] = v
			}
			for k, v := range srcSpec.ReleasePlan.PatchPodTemplateMetadata.Labels {
				obj.Spec.ReleasePlan.PatchPodTemplateMetadata.Labels[k] = v
			}
		}

		if strings.EqualFold(src.Annotations[RolloutStyleAnnotation], string(PartitionRollingStyle)) {
			obj.Spec.ReleasePlan.RollingStyle = v1beta1.PartitionRollingStyle
		}
		if strings.EqualFold(src.Annotations[RolloutStyleAnnotation], string(CanaryRollingStyle)) {
			obj.Spec.ReleasePlan.RollingStyle = v1beta1.CanaryRollingStyle
		}
		if strings.EqualFold(src.Annotations[RolloutStyleAnnotation], string(BlueGreenRollingStyle)) {
			obj.Spec.ReleasePlan.RollingStyle = v1beta1.BlueGreenRollingStyle
		}

		obj.Spec.ReleasePlan.EnableExtraWorkloadForCanary = srcSpec.ReleasePlan.EnableExtraWorkloadForCanary

		// status
		obj.Status = v1beta1.BatchReleaseStatus{
			StableRevision:           src.Status.StableRevision,
			UpdateRevision:           src.Status.UpdateRevision,
			ObservedGeneration:       src.Status.ObservedGeneration,
			ObservedRolloutID:        src.Status.ObservedRolloutID,
			ObservedWorkloadReplicas: src.Status.ObservedWorkloadReplicas,
			ObservedReleasePlanHash:  src.Status.ObservedReleasePlanHash,
			CollisionCount:           src.Status.CollisionCount,
			Phase:                    v1beta1.RolloutPhase(src.Status.Phase),
		}
		for _, cond := range src.Status.Conditions {
			o := v1beta1.RolloutCondition{
				Type:               v1beta1.RolloutConditionType(cond.Type),
				Status:             cond.Status,
				LastUpdateTime:     cond.LastUpdateTime,
				LastTransitionTime: cond.LastTransitionTime,
				Reason:             cond.Reason,
				Message:            cond.Message,
			}
			obj.Status.Conditions = append(obj.Status.Conditions, o)
		}
		obj.Status.CanaryStatus = v1beta1.BatchReleaseCanaryStatus{
			CurrentBatchState:    v1beta1.BatchReleaseBatchStateType(src.Status.CanaryStatus.CurrentBatchState),
			CurrentBatch:         src.Status.CanaryStatus.CurrentBatch,
			BatchReadyTime:       src.Status.CanaryStatus.BatchReadyTime,
			UpdatedReplicas:      src.Status.CanaryStatus.UpdatedReplicas,
			UpdatedReadyReplicas: src.Status.CanaryStatus.UpdatedReadyReplicas,
			NoNeedUpdateReplicas: src.Status.CanaryStatus.NoNeedUpdateReplicas,
		}
		return nil
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
}

func (dst *BatchRelease) ConvertFrom(src conversion.Hub) error {
	switch t := src.(type) {
	case *v1beta1.BatchRelease:
		srcV1beta1 := src.(*v1beta1.BatchRelease)
		dst.ObjectMeta = srcV1beta1.ObjectMeta
		dst.Spec = BatchReleaseSpec{}
		srcSpec := srcV1beta1.Spec
		dst.Spec.TargetRef.WorkloadRef = &WorkloadRef{
			APIVersion: srcSpec.WorkloadRef.APIVersion,
			Kind:       srcSpec.WorkloadRef.Kind,
			Name:       srcSpec.WorkloadRef.Name,
		}
		dst.Spec.ReleasePlan = ReleasePlan{
			BatchPartition:   srcSpec.ReleasePlan.BatchPartition,
			RolloutID:        srcSpec.ReleasePlan.RolloutID,
			FailureThreshold: srcSpec.ReleasePlan.FailureThreshold,
			FinalizingPolicy: FinalizingPolicyType(srcSpec.ReleasePlan.FinalizingPolicy),
		}
		for _, batch := range srcSpec.ReleasePlan.Batches {
			obj := ReleaseBatch{
				CanaryReplicas: batch.CanaryReplicas,
			}
			dst.Spec.ReleasePlan.Batches = append(dst.Spec.ReleasePlan.Batches, obj)
		}
		if srcSpec.ReleasePlan.PatchPodTemplateMetadata != nil {
			dst.Spec.ReleasePlan.PatchPodTemplateMetadata = &PatchPodTemplateMetadata{
				Annotations: map[string]string{},
				Labels:      map[string]string{},
			}
			for k, v := range srcSpec.ReleasePlan.PatchPodTemplateMetadata.Annotations {
				dst.Spec.ReleasePlan.PatchPodTemplateMetadata.Annotations[k] = v
			}
			for k, v := range srcSpec.ReleasePlan.PatchPodTemplateMetadata.Labels {
				dst.Spec.ReleasePlan.PatchPodTemplateMetadata.Labels[k] = v
			}
		}
		if dst.Annotations == nil {
			dst.Annotations = map[string]string{}
		}
		dst.Annotations[RolloutStyleAnnotation] = strings.ToLower(string(srcV1beta1.Spec.ReleasePlan.RollingStyle))
		dst.Spec.ReleasePlan.RollingStyle = RollingStyleType(srcV1beta1.Spec.ReleasePlan.RollingStyle)
		dst.Spec.ReleasePlan.EnableExtraWorkloadForCanary = srcV1beta1.Spec.ReleasePlan.EnableExtraWorkloadForCanary

		// status
		dst.Status = BatchReleaseStatus{
			StableRevision:           srcV1beta1.Status.StableRevision,
			UpdateRevision:           srcV1beta1.Status.UpdateRevision,
			ObservedGeneration:       srcV1beta1.Status.ObservedGeneration,
			ObservedRolloutID:        srcV1beta1.Status.ObservedRolloutID,
			ObservedWorkloadReplicas: srcV1beta1.Status.ObservedWorkloadReplicas,
			ObservedReleasePlanHash:  srcV1beta1.Status.ObservedReleasePlanHash,
			CollisionCount:           srcV1beta1.Status.CollisionCount,
			Phase:                    RolloutPhase(srcV1beta1.Status.Phase),
		}
		for _, cond := range srcV1beta1.Status.Conditions {
			obj := RolloutCondition{
				Type:               RolloutConditionType(cond.Type),
				Status:             cond.Status,
				LastUpdateTime:     cond.LastUpdateTime,
				LastTransitionTime: cond.LastTransitionTime,
				Reason:             cond.Reason,
				Message:            cond.Message,
			}
			dst.Status.Conditions = append(dst.Status.Conditions, obj)
		}
		dst.Status.CanaryStatus = BatchReleaseCanaryStatus{
			CurrentBatchState:    BatchReleaseBatchStateType(srcV1beta1.Status.CanaryStatus.CurrentBatchState),
			CurrentBatch:         srcV1beta1.Status.CanaryStatus.CurrentBatch,
			BatchReadyTime:       srcV1beta1.Status.CanaryStatus.BatchReadyTime,
			UpdatedReplicas:      srcV1beta1.Status.CanaryStatus.UpdatedReplicas,
			UpdatedReadyReplicas: srcV1beta1.Status.CanaryStatus.UpdatedReadyReplicas,
			NoNeedUpdateReplicas: srcV1beta1.Status.CanaryStatus.NoNeedUpdateReplicas,
		}
		return nil
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
}
