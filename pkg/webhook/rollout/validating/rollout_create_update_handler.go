/*
Copyright 2019 The Kruise Authors.

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

package validating

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	appsv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	addmissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// RolloutCreateUpdateHandler handles Rollout
type RolloutCreateUpdateHandler struct {
	// To use the client, you need to do the following:
	// - uncomment it
	// - import sigs.k8s.io/controller-runtime/pkg/client
	// - uncomment the InjectClient method at the bottom of this file.
	Client client.Client

	// Decoder decodes objects
	Decoder *admission.Decoder
}

var _ admission.Handler = &RolloutCreateUpdateHandler{}

// Handle handles admission requests.
func (h *RolloutCreateUpdateHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	switch req.Operation {
	case addmissionv1.Create:
		// v1alpha1
		if req.Kind.Version == appsv1alpha1.GroupVersion.Version {
			obj := &appsv1alpha1.Rollout{}
			if err := h.Decoder.Decode(req, obj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			errList := h.validateV1alpha1Rollout(obj)
			if len(errList) != 0 {
				return admission.Errored(http.StatusUnprocessableEntity, errList.ToAggregate())
			}
			// v1beta1
		} else {
			obj := &appsv1beta1.Rollout{}
			if err := h.Decoder.Decode(req, obj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			errList := h.validateRollout(obj)
			if len(errList) != 0 {
				return admission.Errored(http.StatusUnprocessableEntity, errList.ToAggregate())
			}
		}

	case addmissionv1.Update:
		// v1alpha1
		if req.Kind.Version == appsv1alpha1.GroupVersion.Version {
			obj := &appsv1alpha1.Rollout{}
			if err := h.Decoder.Decode(req, obj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			errList := h.validateV1alpha1Rollout(obj)
			if len(errList) != 0 {
				return admission.Errored(http.StatusUnprocessableEntity, errList.ToAggregate())
			}
			oldObj := &appsv1alpha1.Rollout{}
			if err := h.Decoder.DecodeRaw(req.AdmissionRequest.OldObject, oldObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			errList = h.validateV1alpha1RolloutUpdate(oldObj, obj)
			if len(errList) != 0 {
				return admission.Errored(http.StatusUnprocessableEntity, errList.ToAggregate())
			}
		} else {
			obj := &appsv1beta1.Rollout{}
			if err := h.Decoder.Decode(req, obj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			errList := h.validateRollout(obj)
			if len(errList) != 0 {
				return admission.Errored(http.StatusUnprocessableEntity, errList.ToAggregate())
			}
			oldObj := &appsv1beta1.Rollout{}
			if err := h.Decoder.DecodeRaw(req.AdmissionRequest.OldObject, oldObj); err != nil {
				return admission.Errored(http.StatusBadRequest, err)
			}
			errList = h.validateRolloutUpdate(oldObj, obj)
			if len(errList) != 0 {
				return admission.Errored(http.StatusUnprocessableEntity, errList.ToAggregate())
			}
		}
	}

	return admission.ValidationResponse(true, "")
}

func (h *RolloutCreateUpdateHandler) validateRolloutUpdate(oldObj, newObj *appsv1beta1.Rollout) field.ErrorList {
	latestObject := &appsv1beta1.Rollout{}
	err := h.Client.Get(context.TODO(), client.ObjectKeyFromObject(newObj), latestObject)
	if err != nil {
		return field.ErrorList{field.InternalError(field.NewPath("Rollout"), err)}
	}
	if errorList := h.validateRollout(newObj); errorList != nil {
		return errorList
	}

	switch latestObject.Status.Phase {
	// The workloadRef and TrafficRouting are not allowed to be modified in the Progressing, Terminating state
	case appsv1beta1.RolloutPhaseProgressing, appsv1beta1.RolloutPhaseTerminating:
		if !reflect.DeepEqual(oldObj.Spec.WorkloadRef, newObj.Spec.WorkloadRef) {
			return field.ErrorList{field.Forbidden(field.NewPath("Spec.ObjectRef"), "Rollout 'ObjectRef' field is immutable")}
		}
		if !reflect.DeepEqual(oldObj.Spec.Strategy.GetTrafficRouting(), newObj.Spec.Strategy.GetTrafficRouting()) {
			return field.ErrorList{field.Forbidden(field.NewPath("Spec.Strategy.Canary|BlueGreen.TrafficRoutings"), "Rollout 'Strategy.Canary|BlueGreen.TrafficRoutings' field is immutable")}
		}
		if oldObj.Spec.Strategy.GetRollingStyle() != newObj.Spec.Strategy.GetRollingStyle() {
			return field.ErrorList{field.Forbidden(field.NewPath("Spec.Strategy.Canary|BlueGreen"), "Rollout style and enableExtraWorkloadForCanary are immutable")}
		}
	}

	/*if newObj.Status.CanaryStatus != nil && newObj.Status.CanaryStatus.CurrentStepState == appsv1beta1.CanaryStepStateReady {
		if oldObj.Status.CanaryStatus != nil {
			switch oldObj.Status.CanaryStatus.CurrentStepState {
			case appsv1beta1.CanaryStepStateCompleted, appsv1beta1.CanaryStepStatePaused:
			default:
				return field.ErrorList{field.Forbidden(field.NewPath("Status"), "CanaryStatus.CurrentStepState only allow to translate to 'StepInCompleted' from 'StepInPaused'")}
			}
		}
	}*/

	return nil
}

func (h *RolloutCreateUpdateHandler) validateRollout(rollout *appsv1beta1.Rollout) field.ErrorList {
	errList := validateRolloutSpec(rollout, field.NewPath("Spec"))
	errList = append(errList, h.validateRolloutConflict(rollout, field.NewPath("Conflict Checker"))...)
	return errList
}

func (h *RolloutCreateUpdateHandler) validateRolloutConflict(rollout *appsv1beta1.Rollout, path *field.Path) field.ErrorList {
	errList := field.ErrorList{}
	rolloutList := &appsv1beta1.RolloutList{}
	err := h.Client.List(context.TODO(), rolloutList, client.InNamespace(rollout.Namespace), utilclient.DisableDeepCopy)
	if err != nil {
		return append(errList, field.InternalError(path, err))
	}
	for i := range rolloutList.Items {
		r := &rolloutList.Items[i]
		if r.Name == rollout.Name || !IsSameWorkloadRefGVKName(&r.Spec.WorkloadRef, &rollout.Spec.WorkloadRef) {
			continue
		}
		return field.ErrorList{field.Invalid(path, rollout.Name,
			fmt.Sprintf("This rollout conflict with Rollout(%v), one workload only have less than one Rollout", client.ObjectKeyFromObject(r)))}
	}
	return nil
}

func validateRolloutSpec(rollout *appsv1beta1.Rollout, fldPath *field.Path) field.ErrorList {
	errList := validateRolloutSpecObjectRef(&rollout.Spec.WorkloadRef, fldPath.Child("ObjectRef"))
	errList = append(errList, validateRolloutSpecStrategy(&rollout.Spec.Strategy, fldPath.Child("Strategy"))...)
	return errList
}

func validateRolloutSpecObjectRef(workloadRef *appsv1beta1.ObjectRef, fldPath *field.Path) field.ErrorList {
	if workloadRef == nil {
		return field.ErrorList{field.Invalid(fldPath.Child("WorkloadRef"), workloadRef, "WorkloadRef is required")}
	}

	gvk := schema.FromAPIVersionAndKind(workloadRef.APIVersion, workloadRef.Kind)
	if !util.IsSupportedWorkload(gvk) {
		return field.ErrorList{field.Invalid(fldPath.Child("WorkloadRef"), workloadRef, "WorkloadRef kind is not supported")}
	}
	return nil
}

func validateRolloutSpecStrategy(strategy *appsv1beta1.RolloutStrategy, fldPath *field.Path) field.ErrorList {
	if strategy.Canary == nil && strategy.BlueGreen == nil {
		return field.ErrorList{field.Invalid(fldPath, nil, "Canary and BlueGreen cannot both be empty")}
	}
	if strategy.Canary != nil && strategy.BlueGreen != nil {
		return field.ErrorList{field.Invalid(fldPath, nil, "Canary and BlueGreen cannot both be set")}
	}
	if strategy.BlueGreen != nil {
		return validateRolloutSpecBlueGreenStrategy(strategy.BlueGreen, fldPath.Child("BlueGreen"))
	}
	return validateRolloutSpecCanaryStrategy(strategy.Canary, fldPath.Child("Canary"))
}

type TrafficRule string

const (
	TrafficRuleCanary    TrafficRule = "Canary"
	TrafficRuleBlueGreen TrafficRule = "BlueGreen"
	NoTraffic            TrafficRule = "NoTraffic"
)

func validateRolloutSpecCanaryStrategy(canary *appsv1beta1.CanaryStrategy, fldPath *field.Path) field.ErrorList {
	trafficRule := NoTraffic
	if len(canary.TrafficRoutings) > 0 {
		trafficRule = TrafficRuleCanary
	}
	errList := validateRolloutSpecCanarySteps(canary.Steps, fldPath.Child("Steps"), trafficRule)
	if len(canary.TrafficRoutings) > 1 {
		errList = append(errList, field.Invalid(fldPath, canary.TrafficRoutings, "Rollout currently only support single TrafficRouting."))
	}
	for _, traffic := range canary.TrafficRoutings {
		errList = append(errList, validateRolloutSpecCanaryTraffic(traffic, fldPath.Child("TrafficRouting"))...)
	}
	return errList
}

func validateRolloutSpecBlueGreenStrategy(blueGreen *appsv1beta1.BlueGreenStrategy, fldPath *field.Path) field.ErrorList {
	trafficRule := NoTraffic
	if len(blueGreen.TrafficRoutings) > 0 {
		trafficRule = TrafficRuleBlueGreen
	}
	errList := validateRolloutSpecCanarySteps(blueGreen.Steps, fldPath.Child("Steps"), trafficRule)
	if len(blueGreen.TrafficRoutings) > 1 {
		errList = append(errList, field.Invalid(fldPath, blueGreen.TrafficRoutings, "Rollout currently only support single TrafficRouting."))
	}
	for _, traffic := range blueGreen.TrafficRoutings {
		errList = append(errList, validateRolloutSpecCanaryTraffic(traffic, fldPath.Child("TrafficRouting"))...)
	}
	return errList
}

func validateRolloutSpecCanaryTraffic(traffic appsv1beta1.TrafficRoutingRef, fldPath *field.Path) field.ErrorList {
	errList := field.ErrorList{}
	if len(traffic.Service) == 0 {
		errList = append(errList, field.Invalid(fldPath.Child("Service"), traffic.Service, "TrafficRouting.Service cannot be empty"))
	}

	if traffic.Gateway == nil && traffic.Ingress == nil && traffic.CustomNetworkRefs == nil {
		errList = append(errList, field.Invalid(fldPath.Child("TrafficRoutings"), traffic.Ingress, "TrafficRoutings are not set"))
	}

	if traffic.Ingress != nil {
		if traffic.Ingress.Name == "" {
			errList = append(errList, field.Invalid(fldPath.Child("Ingress"), traffic.Ingress, "TrafficRouting.Ingress.Ingress cannot be empty"))
		}
	}
	if traffic.Gateway != nil {
		if traffic.Gateway.HTTPRouteName == nil || *traffic.Gateway.HTTPRouteName == "" {
			errList = append(errList, field.Invalid(fldPath.Child("Gateway"), traffic.Gateway, "TrafficRouting.Gateway must set the name of HTTPRoute or HTTPsRoute"))
		}
	}

	return errList
}

func validateRolloutSpecCanarySteps(steps []appsv1beta1.CanaryStep, fldPath *field.Path, trafficRule TrafficRule) field.ErrorList {
	stepCount := len(steps)
	if stepCount == 0 {
		return field.ErrorList{field.Invalid(fldPath, steps, "The number of Canary.Steps cannot be empty")}
	}

	for i := range steps {
		s := &steps[i]
		if s.Replicas == nil {
			return field.ErrorList{field.Invalid(fldPath.Index(i).Child("steps"), steps, `replicas cannot be empty`)}
		}
		canaryReplicas, err := intstr.GetScaledValueFromIntOrPercent(s.Replicas, 100, true)
		if err != nil ||
			canaryReplicas <= 0 ||
			(canaryReplicas > 100 && s.Replicas.Type == intstr.String) {
			return field.ErrorList{field.Invalid(fldPath.Index(i).Child("Replicas"),
				s.Replicas, `replicas must be positive number, or a percentage with "0%" < canaryReplicas <= "100%"`)}
		}
		if trafficRule == NoTraffic || s.Traffic == nil {
			continue
		}
		is := intstr.FromString(*s.Traffic)
		weight, err := intstr.GetScaledValueFromIntOrPercent(&is, 100, true)
		switch trafficRule {
		case TrafficRuleBlueGreen:
			// traffic "0%" is allowed in blueGreen strategy
			if err != nil || weight < 0 || weight > 100 {
				return field.ErrorList{field.Invalid(fldPath.Index(i).Child("steps"), steps, `traffic must be percentage with "0%" <= traffic <= "100%" in blueGreen strategy`)}
			}
		default:
			// traffic "0%" is not allowed in canary strategy
			if err != nil || weight <= 0 || weight > 100 {
				return field.ErrorList{field.Invalid(fldPath.Index(i).Child("steps"), steps, `traffic must be percentage with "0%" < traffic <= "100%" in canary strategy`)}
			}
		}
	}

	for i := 1; i < stepCount; i++ {
		prev := &steps[i-1]
		curr := &steps[i]
		// if they are comparable, then compare them
		if IsPercentageCanaryReplicasType(prev.Replicas) != IsPercentageCanaryReplicasType(curr.Replicas) {
			continue
		}
		prevCanaryReplicas, _ := intstr.GetScaledValueFromIntOrPercent(prev.Replicas, 100, true)
		currCanaryReplicas, _ := intstr.GetScaledValueFromIntOrPercent(curr.Replicas, 100, true)
		if currCanaryReplicas < prevCanaryReplicas {
			return field.ErrorList{field.Invalid(fldPath.Child("CanaryReplicas"), steps, `Steps.CanaryReplicas must be a non decreasing sequence`)}
		}
	}

	return nil
}

func IsPercentageCanaryReplicasType(replicas *intstr.IntOrString) bool {
	return replicas == nil || replicas.Type == intstr.String
}

func IsSameWorkloadRefGVKName(a, b *appsv1beta1.ObjectRef) bool {
	if a == nil || b == nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

var _ inject.Client = &RolloutCreateUpdateHandler{}

// InjectClient injects the client into the RolloutCreateUpdateHandler
func (h *RolloutCreateUpdateHandler) InjectClient(c client.Client) error {
	h.Client = c
	return nil
}

var _ admission.DecoderInjector = &RolloutCreateUpdateHandler{}

// InjectDecoder injects the decoder into the RolloutCreateUpdateHandler
func (h *RolloutCreateUpdateHandler) InjectDecoder(d *admission.Decoder) error {
	h.Decoder = d
	return nil
}
