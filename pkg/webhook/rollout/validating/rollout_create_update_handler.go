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
	addmissionv1 "k8s.io/api/admission/v1"
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
		obj := &appsv1alpha1.Rollout{}
		if err := h.Decoder.Decode(req, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		errList := h.validateRollout(obj)
		if len(errList) != 0 {
			return admission.Errored(http.StatusUnprocessableEntity, errList.ToAggregate())
		}

	case addmissionv1.Update:
		obj := &appsv1alpha1.Rollout{}
		if err := h.Decoder.Decode(req, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		oldObj := &appsv1alpha1.Rollout{}
		if err := h.Decoder.DecodeRaw(req.AdmissionRequest.OldObject, oldObj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		errList := h.validateRolloutUpdate(oldObj, obj)
		if len(errList) != 0 {
			return admission.Errored(http.StatusUnprocessableEntity, errList.ToAggregate())
		}
	}

	return admission.ValidationResponse(true, "")
}

func (h *RolloutCreateUpdateHandler) validateRolloutUpdate(oldObj, newObj *appsv1alpha1.Rollout) field.ErrorList {
	latestObject := &appsv1alpha1.Rollout{}
	err := h.Client.Get(context.TODO(), client.ObjectKeyFromObject(newObj), latestObject)
	if err != nil {
		return field.ErrorList{field.InternalError(field.NewPath("Rollout"), err)}
	}
	if errorList := h.validateRollout(newObj); errorList != nil {
		return errorList
	}

	switch latestObject.Status.Phase {
	// The workloadRef and TrafficRouting are not allowed to be modified in the Progressing, Terminating state
	case appsv1alpha1.RolloutPhaseProgressing, appsv1alpha1.RolloutPhaseTerminating:
		if !reflect.DeepEqual(oldObj.Spec.ObjectRef, newObj.Spec.ObjectRef) {
			return field.ErrorList{field.Forbidden(field.NewPath("Spec.ObjectRef"), "Rollout 'ObjectRef' field is immutable")}
		}
		// canary strategy
		if !reflect.DeepEqual(oldObj.Spec.Strategy.Canary.TrafficRoutings, newObj.Spec.Strategy.Canary.TrafficRoutings) {
			return field.ErrorList{field.Forbidden(field.NewPath("Spec.Strategy.Canary.TrafficRoutings"), "Rollout 'Strategy.Canary.TrafficRoutings' field is immutable")}
		}
	}

	/*if newObj.Status.CanaryStatus != nil && newObj.Status.CanaryStatus.CurrentStepState == appsv1alpha1.CanaryStepStateReady {
		if oldObj.Status.CanaryStatus != nil {
			switch oldObj.Status.CanaryStatus.CurrentStepState {
			case appsv1alpha1.CanaryStepStateCompleted, appsv1alpha1.CanaryStepStatePaused:
			default:
				return field.ErrorList{field.Forbidden(field.NewPath("Status"), "CanaryStatus.CurrentStepState only allow to translate to 'StepInCompleted' from 'StepInPaused'")}
			}
		}
	}*/

	return nil
}

func (h *RolloutCreateUpdateHandler) validateRollout(rollout *appsv1alpha1.Rollout) field.ErrorList {
	errList := validateRolloutSpec(rollout, field.NewPath("Spec"))
	errList = append(errList, h.validateRolloutConflict(rollout, field.NewPath("Conflict Checker"))...)
	return errList
}

func (h *RolloutCreateUpdateHandler) validateRolloutConflict(rollout *appsv1alpha1.Rollout, path *field.Path) field.ErrorList {
	errList := field.ErrorList{}
	rolloutList := &appsv1alpha1.RolloutList{}
	err := h.Client.List(context.TODO(), rolloutList, client.InNamespace(rollout.Namespace))
	if err != nil {
		return append(errList, field.InternalError(path, err))
	}
	for i := range rolloutList.Items {
		r := &rolloutList.Items[i]
		if r.Name == rollout.Name || !IsSameWorkloadRefGVKName(r.Spec.ObjectRef.WorkloadRef, rollout.Spec.ObjectRef.WorkloadRef) {
			continue
		}
		return field.ErrorList{field.Invalid(path, rollout.Name,
			fmt.Sprintf("This rollout conflict with Rollout(%v), one workload only have less than one Rollout", client.ObjectKeyFromObject(r)))}
	}
	return nil
}

func validateRolloutSpec(rollout *appsv1alpha1.Rollout, fldPath *field.Path) field.ErrorList {
	errList := validateRolloutSpecObjectRef(&rollout.Spec.ObjectRef, fldPath.Child("ObjectRef"))
	errList = append(errList, validateRolloutSpecStrategy(&rollout.Spec.Strategy, fldPath.Child("Strategy"))...)
	return errList
}

func validateRolloutSpecObjectRef(objectRef *appsv1alpha1.ObjectRef, fldPath *field.Path) field.ErrorList {
	if objectRef.WorkloadRef == nil || (objectRef.WorkloadRef.Kind != "Deployment" && objectRef.WorkloadRef.Kind != "CloneSet") {
		return field.ErrorList{field.Invalid(fldPath.Child("WorkloadRef"), objectRef.WorkloadRef, "WorkloadRef only support 'Deployments', 'CloneSet'")}
	}
	return nil
}

func validateRolloutSpecStrategy(strategy *appsv1alpha1.RolloutStrategy, fldPath *field.Path) field.ErrorList {
	return validateRolloutSpecCanaryStrategy(strategy.Canary, fldPath.Child("Canary"))
}

func validateRolloutSpecCanaryStrategy(canary *appsv1alpha1.CanaryStrategy, fldPath *field.Path) field.ErrorList {
	if canary == nil {
		return field.ErrorList{field.Invalid(fldPath, nil, "Canary cannot be empty")}
	}

	errList := validateRolloutSpecCanarySteps(canary.Steps, fldPath.Child("Steps"))
	if len(canary.TrafficRoutings) > 1 {
		errList = append(errList, field.Invalid(fldPath, canary.TrafficRoutings, "Rollout currently only support single TrafficRouting."))
	}
	for _, traffic := range canary.TrafficRoutings {
		errList = append(errList, validateRolloutSpecCanaryTraffic(traffic, fldPath.Child("TrafficRouting"))...)
	}
	return errList
}

func validateRolloutSpecCanaryTraffic(traffic *appsv1alpha1.TrafficRouting, fldPath *field.Path) field.ErrorList {
	if traffic == nil {
		return field.ErrorList{field.Invalid(fldPath, nil, "Canary.TrafficRoutings cannot be empty")}
	}

	errList := field.ErrorList{}
	if len(traffic.Service) == 0 {
		errList = append(errList, field.Invalid(fldPath.Child("Service"), traffic.Service, "TrafficRouting.Service cannot be empty"))
	}

	switch traffic.Type {
	case "", "nginx":
		if traffic.Ingress == nil ||
			len(traffic.Ingress.Name) == 0 {
			errList = append(errList, field.Invalid(fldPath.Child("Ingress"), traffic.Ingress, "TrafficRouting.Ingress.Ingress cannot be empty"))
		}
	default:

		errList = append(errList, field.Invalid(fldPath.Child("Type"), traffic.Type, "TrafficRouting only support 'nginx' type"))
	}

	return errList
}

func validateRolloutSpecCanarySteps(steps []appsv1alpha1.CanaryStep, fldPath *field.Path) field.ErrorList {
	stepCount := len(steps)
	if stepCount == 0 {
		return field.ErrorList{field.Invalid(fldPath, steps, "The number of Canary.Steps cannot be empty")}
	}

	for i := range steps {
		s := &steps[i]
		if s.Weight <= 0 || s.Weight > 100 {
			return field.ErrorList{field.Invalid(fldPath.Index(i).Child("Weight"),
				s.Weight, `Weight must be a positive number with "0" < weight <= "100"`)}
		}
		if s.Replicas != nil {
			canaryReplicas, err := intstr.GetScaledValueFromIntOrPercent(s.Replicas, 100, true)
			if err != nil || canaryReplicas <= 0 || canaryReplicas > 100 {
				return field.ErrorList{field.Invalid(fldPath.Index(i).Child("CanaryReplicas"),
					s.Replicas, `canaryReplicas must be positive number with with "0" < canaryReplicas <= "100", or a percentage with "0%" < canaryReplicas <= "100%"`)}
			}
		}
	}

	for i := 1; i < stepCount; i++ {
		prev := &steps[i-1]
		curr := &steps[i]
		if curr.Weight < prev.Weight {
			return field.ErrorList{field.Invalid(fldPath.Child("Weight"), steps, `Steps.Weight must be a non decreasing sequence`)}
		}

		// if they are comparable, then compare them
		if IsPercentageCanaryReplicasType(prev.Replicas) != IsPercentageCanaryReplicasType(curr.Replicas) {
			continue
		}

		prevCanaryReplicas, _ := intstr.GetScaledValueFromIntOrPercent(prev.Replicas, 100, true)
		currCanaryReplicas, _ := intstr.GetScaledValueFromIntOrPercent(curr.Replicas, 100, true)
		if prev.Replicas == nil {
			prevCanaryReplicas = int(prev.Weight)
		}
		if curr.Replicas == nil {
			currCanaryReplicas = int(curr.Weight)
		}
		if currCanaryReplicas < prevCanaryReplicas {
			return field.ErrorList{field.Invalid(fldPath.Child("CanaryReplicas"), steps, `Steps.CanaryReplicas must be a non decreasing sequence`)}
		}
	}

	return nil
}

func IsPercentageCanaryReplicasType(replicas *intstr.IntOrString) bool {
	return replicas == nil || replicas.Type == intstr.String
}

func IsSameWorkloadRefGVKName(a, b *appsv1alpha1.WorkloadRef) bool {
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
