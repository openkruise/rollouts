package v1alpha1

import (
	"fmt"

	"github.com/openkruise/rollouts/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

func (src *Rollout) ConvertTo(dstRaw conversion.Hub) error {
	switch t := dstRaw.(type) {
	case *v1beta1.Rollout:
		dst := dstRaw.(*v1beta1.Rollout)
		dst.ObjectMeta = src.ObjectMeta

		// rollout spec conversion
		dst.Spec.DeprecatedRolloutID = src.Spec.DeprecatedRolloutID
		dst.Spec.Disabled = src.Spec.Disabled
		dst.Spec.ObjectRef = v1beta1.ObjectRef{
			WorkloadRef: &v1beta1.WorkloadRef{
				APIVersion: src.Spec.ObjectRef.WorkloadRef.APIVersion,
				Kind:       src.Spec.ObjectRef.WorkloadRef.Kind,
				Name:       src.Spec.ObjectRef.WorkloadRef.Name,
			},
		}
		dst.Spec.Strategy.Paused = src.Spec.Strategy.Paused
		if src.Spec.Strategy.Canary != nil {
			dst.Spec.Strategy.Canary = &v1beta1.CanaryStrategy{}
			dst.Spec.Strategy.Canary.FailureThreshold = src.Spec.Strategy.Canary.FailureThreshold
			dst.Spec.Strategy.Canary.PatchPodTemplateMetadata = (*v1beta1.PatchPodTemplateMetadata)(src.Spec.Strategy.Canary.PatchPodTemplateMetadata)
			trafficRoutings := make([]v1beta1.TrafficRoutingRef, len(src.Spec.Strategy.Canary.TrafficRoutings))
			for i, v := range src.Spec.Strategy.Canary.TrafficRoutings {
				trafficRoutings[i] = v1beta1.TrafficRoutingRef{
					GracePeriodSeconds: v.GracePeriodSeconds,
					Service:            v.Service,
				}
				if v.Gateway != nil {
					trafficRoutings[i].Gateway.HTTPRouteName = v.Gateway.HTTPRouteName
				}
				if v.Ingress != nil {
					trafficRoutings[i].Ingress = (*v1beta1.IngressTrafficRouting)(v.Ingress)
				}
			}
			dst.Spec.Strategy.Canary.TrafficRoutings = trafficRoutings
			steps := make([]v1beta1.CanaryStep, len(src.Spec.Strategy.Canary.Steps))
			for i, v := range src.Spec.Strategy.Canary.Steps {
				step := v1beta1.CanaryStep{
					Pause:    v1beta1.RolloutPause(v.Pause),
					Replicas: v.Replicas,
					TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
						Weight:                v.Weight,
						RequestHeaderModifier: v.RequestHeaderModifier,
						Matches:               make([]v1beta1.HttpRouteMatch, len(v.Matches)),
					},
				}
				for j, u := range v.Matches {
					step.Matches[j] = v1beta1.HttpRouteMatch{
						Headers: u.Headers,
					}
				}
				steps[i] = step
			}
			dst.Spec.Strategy.Canary.Steps = steps
		}

		// rollout status conversion
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
		dst.Status = v1beta1.RolloutStatus{
			ObservedGeneration: src.Status.ObservedGeneration,
			Phase:              v1beta1.RolloutPhase(src.Status.Phase),
			Message:            src.Status.Message,
			Conditions:         conditions,
		}
		if src.Status.CanaryStatus != nil {
			dst.Status.CanaryStatus = &v1beta1.CanaryStatus{
				ObservedWorkloadGeneration: src.Status.CanaryStatus.ObservedWorkloadGeneration,
				ObservedRolloutID:          src.Status.CanaryStatus.ObservedRolloutID,
				RolloutHash:                src.Status.CanaryStatus.RolloutHash,
				StableRevision:             src.Status.CanaryStatus.StableRevision,
				CanaryRevision:             src.Status.CanaryStatus.CanaryRevision,
				PodTemplateHash:            src.Status.CanaryStatus.PodTemplateHash,
				CanaryReplicas:             src.Status.CanaryStatus.CanaryReadyReplicas,
				CanaryReadyReplicas:        src.Status.CanaryStatus.CanaryReadyReplicas,
				CurrentStepIndex:           src.Status.CanaryStatus.CurrentStepIndex,
				CurrentStepState:           v1beta1.CanaryStepState(src.Status.CanaryStatus.CurrentStepState),
				Message:                    src.Status.CanaryStatus.Message,
				LastUpdateTime:             src.Status.CanaryStatus.LastUpdateTime,
			}
		}
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
	return nil
}

func (dst *Rollout) ConvertFrom(srcRaw conversion.Hub) error {
	switch t := srcRaw.(type) {
	case *v1beta1.Rollout:
		src := srcRaw.(*v1beta1.Rollout)
		dst.ObjectMeta = src.ObjectMeta

		// rollout spec conversion
		dst.Spec.DeprecatedRolloutID = src.Spec.DeprecatedRolloutID
		dst.Spec.Disabled = src.Spec.Disabled
		dst.Spec.ObjectRef = ObjectRef{
			WorkloadRef: &WorkloadRef{
				APIVersion: src.Spec.ObjectRef.WorkloadRef.APIVersion,
				Kind:       src.Spec.ObjectRef.WorkloadRef.Kind,
				Name:       src.Spec.ObjectRef.WorkloadRef.Name,
			},
		}
		dst.Spec.Strategy.Paused = src.Spec.Strategy.Paused
		if src.Spec.Strategy.Canary != nil {
			dst.Spec.Strategy.Canary = &CanaryStrategy{}
			dst.Spec.Strategy.Canary.FailureThreshold = src.Spec.Strategy.Canary.FailureThreshold
			dst.Spec.Strategy.Canary.PatchPodTemplateMetadata = (*PatchPodTemplateMetadata)(src.Spec.Strategy.Canary.PatchPodTemplateMetadata)
			trafficRoutings := make([]TrafficRoutingRef, len(src.Spec.Strategy.Canary.TrafficRoutings))
			for i, v := range src.Spec.Strategy.Canary.TrafficRoutings {
				trafficRoutings[i] = TrafficRoutingRef{
					GracePeriodSeconds: v.GracePeriodSeconds,
					Service:            v.Service,
				}
				if v.Gateway != nil {
					trafficRoutings[i].Gateway.HTTPRouteName = v.Gateway.HTTPRouteName
				}
				if v.Ingress != nil {
					trafficRoutings[i].Ingress = (*IngressTrafficRouting)(v.Ingress)
				}
			}
			dst.Spec.Strategy.Canary.TrafficRoutings = trafficRoutings
			steps := make([]CanaryStep, len(src.Spec.Strategy.Canary.Steps))
			for i, v := range src.Spec.Strategy.Canary.Steps {
				step := CanaryStep{
					Pause:    RolloutPause(v.Pause),
					Replicas: v.Replicas,
					TrafficRoutingStrategy: TrafficRoutingStrategy{
						Weight:                v.Weight,
						RequestHeaderModifier: v.RequestHeaderModifier,
						Matches:               make([]HttpRouteMatch, len(v.Matches)),
					},
				}
				for j, u := range v.Matches {
					step.Matches[j] = HttpRouteMatch{
						Headers: u.Headers,
					}
				}
				steps[i] = step
			}
			dst.Spec.Strategy.Canary.Steps = steps
		}

		// rollout spec conversion
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
		dst.Status = RolloutStatus{
			ObservedGeneration: src.Status.ObservedGeneration,
			Phase:              RolloutPhase(src.Status.Phase),
			Message:            src.Status.Message,
			Conditions:         conditions,
		}
		if src.Status.CanaryStatus != nil {
			dst.Status.CanaryStatus = &CanaryStatus{
				ObservedWorkloadGeneration: src.Status.CanaryStatus.ObservedWorkloadGeneration,
				ObservedRolloutID:          src.Status.CanaryStatus.ObservedRolloutID,
				RolloutHash:                src.Status.CanaryStatus.RolloutHash,
				StableRevision:             src.Status.CanaryStatus.StableRevision,
				CanaryRevision:             src.Status.CanaryStatus.CanaryRevision,
				PodTemplateHash:            src.Status.CanaryStatus.PodTemplateHash,
				CanaryReplicas:             src.Status.CanaryStatus.CanaryReadyReplicas,
				CanaryReadyReplicas:        src.Status.CanaryStatus.CanaryReadyReplicas,
				CurrentStepIndex:           src.Status.CanaryStatus.CurrentStepIndex,
				CurrentStepState:           CanaryStepState(src.Status.CanaryStatus.CurrentStepState),
				Message:                    src.Status.CanaryStatus.Message,
				LastUpdateTime:             src.Status.CanaryStatus.LastUpdateTime,
			}
		}
	default:
		return fmt.Errorf("unsupported type %v", t)
	}

	return nil
}
