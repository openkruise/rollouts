package v1alpha1

import (
	"fmt"

	"github.com/openkruise/rollouts/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
)

func (src *RolloutHistory) ConvertTo(dstRaw conversion.Hub) error {
	switch t := dstRaw.(type) {
	case *v1beta1.RolloutHistory:
		dst := dstRaw.(*v1beta1.RolloutHistory)
		dst.ObjectMeta = src.ObjectMeta

		// rollouthistory spec conversion
		dst.Spec.Rollout.RolloutID = src.Spec.Rollout.RolloutID
		dst.Spec.Rollout.NameAndSpecData = v1beta1.NameAndSpecData(src.Spec.Rollout.NameAndSpecData)
		dst.Spec.Workload.TypeMeta = src.Spec.Workload.TypeMeta
		dst.Spec.Workload.NameAndSpecData = v1beta1.NameAndSpecData(src.Spec.Workload.NameAndSpecData)
		dst.Spec.Service.NameAndSpecData = v1beta1.NameAndSpecData(src.Spec.Service.NameAndSpecData)
		if src.Spec.TrafficRouting.HTTPRoute != nil {
			dst.Spec.TrafficRouting.HTTPRoute.NameAndSpecData = v1beta1.NameAndSpecData(src.Spec.TrafficRouting.HTTPRoute.NameAndSpecData)
		}
		if src.Spec.TrafficRouting.Ingress != nil {
			dst.Spec.TrafficRouting.Ingress.NameAndSpecData = v1beta1.NameAndSpecData(src.Spec.TrafficRouting.Ingress.NameAndSpecData)
		}

		// rollouthistory status conversion
		steps := make([]v1beta1.CanaryStepInfo, len(src.Status.CanarySteps))
		for i, v := range src.Status.CanarySteps {
			pods := make([]v1beta1.Pod, len(v.Pods))
			for j, u := range v.Pods {
				pods[j].Name = u.Name
				pods[j].IP = u.IP
				pods[j].NodeName = u.NodeName
			}
			steps[i].CanaryStepIndex = v.CanaryStepIndex
			steps[i].Pods = pods
		}
		dst.Status.Phase = src.Status.Phase
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
	return nil
}

func (dst *RolloutHistory) ConvertFrom(srcRaw conversion.Hub) error {
	switch t := srcRaw.(type) {
	case *v1beta1.RolloutHistory:
		src := srcRaw.(*v1beta1.RolloutHistory)
		dst.ObjectMeta = src.ObjectMeta

		// rollouthistory spec conversion
		dst.Spec.Rollout.RolloutID = src.Spec.Rollout.RolloutID
		dst.Spec.Rollout.NameAndSpecData = NameAndSpecData(src.Spec.Rollout.NameAndSpecData)
		dst.Spec.Workload.TypeMeta = src.Spec.Workload.TypeMeta
		dst.Spec.Workload.NameAndSpecData = NameAndSpecData(src.Spec.Workload.NameAndSpecData)
		dst.Spec.Service.NameAndSpecData = NameAndSpecData(src.Spec.Service.NameAndSpecData)
		if src.Spec.TrafficRouting.HTTPRoute != nil {
			dst.Spec.TrafficRouting.HTTPRoute.NameAndSpecData = NameAndSpecData(src.Spec.TrafficRouting.HTTPRoute.NameAndSpecData)
		}
		if src.Spec.TrafficRouting.Ingress != nil {
			dst.Spec.TrafficRouting.Ingress.NameAndSpecData = NameAndSpecData(src.Spec.TrafficRouting.Ingress.NameAndSpecData)
		}

		// rollouthistory status conversion
		steps := make([]CanaryStepInfo, len(src.Status.CanarySteps))
		for i, v := range src.Status.CanarySteps {
			pods := make([]Pod, len(v.Pods))
			for j, u := range v.Pods {
				pods[j].Name = u.Name
				pods[j].IP = u.IP
				pods[j].NodeName = u.NodeName
			}
			steps[i].CanaryStepIndex = v.CanaryStepIndex
			steps[i].Pods = pods
		}
		dst.Status.Phase = src.Status.Phase
	default:
		return fmt.Errorf("unsupported type %v", t)
	}
	return nil
}
