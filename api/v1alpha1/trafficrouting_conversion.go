package v1alpha1

import (
	"fmt"

	"github.com/openkruise/rollouts/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

func (src *TrafficRouting) ConvertTo(dstRaw conversion.Hub) error {
	switch t := dstRaw.(type) {
	case *v1beta1.TrafficRouting:
		dst := dstRaw.(*v1beta1.TrafficRouting)
		dst.ObjectMeta = src.ObjectMeta

		// trafficRouting spec conversion
		objectRef := make([]v1beta1.TrafficRoutingRef, len(src.Spec.ObjectRef))
		for i, v := range src.Spec.ObjectRef {
			objectRef[i] = v1beta1.TrafficRoutingRef{
				GracePeriodSeconds: v.GracePeriodSeconds,
				Service:            v.Service,
			}
			if v.Ingress != nil {
				objectRef[i].Ingress = &v1beta1.IngressTrafficRouting{
					Name:      v.Ingress.Name,
					ClassType: v.Ingress.ClassType,
				}
			}
			if v.Gateway != nil {
				objectRef[i].Gateway = &v1beta1.GatewayTrafficRouting{
					HTTPRouteName: v.Gateway.HTTPRouteName,
				}
			}
		}
		dst.Spec.ObjectRef = objectRef
		matches := make([]v1beta1.HttpRouteMatch, len(src.Spec.Strategy.Matches))
		for i, v := range src.Spec.Strategy.Matches {
			matches[i].Headers = v.Headers
		}
		dst.Spec.Strategy = v1beta1.TrafficRoutingStrategy{
			Weight:                src.Spec.Strategy.Weight,
			RequestHeaderModifier: src.Spec.Strategy.RequestHeaderModifier,
			Matches:               matches,
		}

		// trafficRouting status conversion
		dst.Status.ObservedGeneration = src.Status.ObservedGeneration
		dst.Status.Phase = v1beta1.TrafficRoutingPhase(src.Status.Phase)
		dst.Status.Message = src.Status.Message
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
	return nil
}

func (dst *TrafficRouting) ConvertFrom(srcRaw conversion.Hub) error {
	switch t := srcRaw.(type) {
	case *v1beta1.TrafficRouting:
		src := srcRaw.(*v1beta1.TrafficRouting)
		dst.ObjectMeta = src.ObjectMeta

		// trafficRouting spec conversion
		objectRef := make([]TrafficRoutingRef, len(src.Spec.ObjectRef))
		for i, v := range src.Spec.ObjectRef {
			objectRef[i] = TrafficRoutingRef{
				GracePeriodSeconds: v.GracePeriodSeconds,
				Service:            v.Service,
			}
			if v.Ingress != nil {
				objectRef[i].Ingress = &IngressTrafficRouting{
					Name:      v.Ingress.Name,
					ClassType: v.Ingress.ClassType,
				}
			}
			if v.Gateway != nil {
				objectRef[i].Gateway = &GatewayTrafficRouting{
					HTTPRouteName: v.Gateway.HTTPRouteName,
				}
			}
		}
		dst.Spec.ObjectRef = objectRef
		matches := make([]HttpRouteMatch, len(src.Spec.Strategy.Matches))
		for i, v := range src.Spec.Strategy.Matches {
			matches[i].Headers = v.Headers
		}
		dst.Spec.Strategy = TrafficRoutingStrategy{
			Weight:                src.Spec.Strategy.Weight,
			RequestHeaderModifier: src.Spec.Strategy.RequestHeaderModifier,
			Matches:               matches,
		}

		// trafficRouting status conversion
		dst.Status.ObservedGeneration = src.Status.ObservedGeneration
		dst.Status.Phase = TrafficRoutingPhase(src.Status.Phase)
		dst.Status.Message = src.Status.Message
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
	return nil
}
