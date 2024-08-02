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

package validating

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (h *RolloutCreateUpdateHandler) validateV1alpha1RolloutUpdate(oldObj, newObj *appsv1alpha1.Rollout) field.ErrorList {
	latestObject := &appsv1alpha1.Rollout{}
	err := h.Client.Get(context.TODO(), client.ObjectKeyFromObject(newObj), latestObject)
	if err != nil {
		return field.ErrorList{field.InternalError(field.NewPath("Rollout"), err)}
	}
	if errorList := h.validateV1alpha1Rollout(newObj); errorList != nil {
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
		if !strings.EqualFold(oldObj.Annotations[appsv1alpha1.RolloutStyleAnnotation], newObj.Annotations[appsv1alpha1.RolloutStyleAnnotation]) {
			return field.ErrorList{field.Forbidden(field.NewPath("Metadata.Annotation"), "Rollout 'Rolling-Style' annotation is immutable")}
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

func (h *RolloutCreateUpdateHandler) validateV1alpha1Rollout(rollout *appsv1alpha1.Rollout) field.ErrorList {
	errList := validateV1alpha1RolloutSpec(GetContextFromv1alpha1Rollout(rollout), rollout, field.NewPath("Spec"))
	errList = append(errList, h.validateV1alpha1RolloutConflict(rollout, field.NewPath("Conflict Checker"))...)
	return errList
}

func (h *RolloutCreateUpdateHandler) validateV1alpha1RolloutConflict(rollout *appsv1alpha1.Rollout, path *field.Path) field.ErrorList {
	errList := field.ErrorList{}
	rolloutList := &appsv1alpha1.RolloutList{}
	err := h.Client.List(context.TODO(), rolloutList, client.InNamespace(rollout.Namespace), utilclient.DisableDeepCopy)
	if err != nil {
		return append(errList, field.InternalError(path, err))
	}
	for i := range rolloutList.Items {
		r := &rolloutList.Items[i]
		if r.Name == rollout.Name || !IsSameV1alpha1WorkloadRefGVKName(r.Spec.ObjectRef.WorkloadRef, rollout.Spec.ObjectRef.WorkloadRef) {
			continue
		}
		return field.ErrorList{field.Invalid(path, rollout.Name,
			fmt.Sprintf("This rollout conflict with Rollout(%v), one workload only have less than one Rollout", client.ObjectKeyFromObject(r)))}
	}
	return nil
}

func validateV1alpha1RolloutSpec(c *validateContext, rollout *appsv1alpha1.Rollout, fldPath *field.Path) field.ErrorList {
	errList := validateV1alpha1RolloutSpecObjectRef(&rollout.Spec.ObjectRef, fldPath.Child("ObjectRef"))
	errList = append(errList, validateV1alpha1RolloutRollingStyle(rollout, field.NewPath("RollingStyle"))...)
	errList = append(errList, validateV1alpha1RolloutSpecStrategy(c, &rollout.Spec.Strategy, fldPath.Child("Strategy"))...)
	return errList
}

func validateV1alpha1RolloutRollingStyle(rollout *appsv1alpha1.Rollout, fldPath *field.Path) field.ErrorList {
	switch strings.ToLower(rollout.Annotations[appsv1alpha1.RolloutStyleAnnotation]) {
	case "", strings.ToLower(string(appsv1alpha1.CanaryRollingStyle)), strings.ToLower(string(appsv1alpha1.PartitionRollingStyle)):
	default:
		return field.ErrorList{field.Invalid(fldPath, rollout.Annotations[appsv1alpha1.RolloutStyleAnnotation],
			"Rolling style must be 'Canary', 'Partition' or empty")}
	}
	return nil
}

func validateV1alpha1RolloutSpecObjectRef(objectRef *appsv1alpha1.ObjectRef, fldPath *field.Path) field.ErrorList {
	if objectRef.WorkloadRef == nil {
		return field.ErrorList{field.Invalid(fldPath.Child("WorkloadRef"), objectRef.WorkloadRef, "WorkloadRef is required")}
	}

	gvk := schema.FromAPIVersionAndKind(objectRef.WorkloadRef.APIVersion, objectRef.WorkloadRef.Kind)
	if !util.IsSupportedWorkload(gvk) {
		return field.ErrorList{field.Invalid(fldPath.Child("WorkloadRef"), objectRef.WorkloadRef, "WorkloadRef kind is not supported")}
	}
	return nil
}

func validateV1alpha1RolloutSpecStrategy(c *validateContext, strategy *appsv1alpha1.RolloutStrategy, fldPath *field.Path) field.ErrorList {
	return validateV1alpha1RolloutSpecCanaryStrategy(c, strategy.Canary, fldPath.Child("Canary"))
}

func validateV1alpha1RolloutSpecCanaryStrategy(c *validateContext, canary *appsv1alpha1.CanaryStrategy, fldPath *field.Path) field.ErrorList {
	if canary == nil {
		return field.ErrorList{field.Invalid(fldPath, nil, "Canary cannot be empty")}
	}

	errList := validateV1alpha1RolloutSpecCanarySteps(c, canary.Steps, fldPath.Child("Steps"), len(canary.TrafficRoutings) > 0)
	if len(canary.TrafficRoutings) > 1 {
		errList = append(errList, field.Invalid(fldPath, canary.TrafficRoutings, "Rollout currently only support single TrafficRouting."))
	}
	for _, traffic := range canary.TrafficRoutings {
		errList = append(errList, validateV1alpha1RolloutSpecCanaryTraffic(traffic, fldPath.Child("TrafficRouting"))...)
	}
	return errList
}

func validateV1alpha1RolloutSpecCanaryTraffic(traffic appsv1alpha1.TrafficRoutingRef, fldPath *field.Path) field.ErrorList {
	errList := field.ErrorList{}
	if traffic.GracePeriodSeconds < 0 {
		errList = append(errList, field.Invalid(fldPath.Child("GracePeriodSeconds"), traffic.Service, "TrafficRouting.GracePeriodSeconds cannot be negative"))
	}

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

func validateV1alpha1RolloutSpecCanarySteps(c *validateContext, steps []appsv1alpha1.CanaryStep, fldPath *field.Path, isTraffic bool) field.ErrorList {
	stepCount := len(steps)
	if stepCount == 0 {
		return field.ErrorList{field.Invalid(fldPath, steps, "The number of Canary.Steps cannot be empty")}
	}

	for i := range steps {
		s := &steps[i]
		if s.Weight == nil && s.Replicas == nil {
			return field.ErrorList{field.Invalid(fldPath.Index(i).Child("steps"), steps, `weight and replicas cannot be empty at the same time`)}
		}
		if s.Replicas != nil {
			canaryReplicas, err := intstr.GetScaledValueFromIntOrPercent(s.Replicas, 100, true)
			if err != nil ||
				canaryReplicas <= 0 ||
				(canaryReplicas > 100 && s.Replicas.Type == intstr.String) {
				return field.ErrorList{field.Invalid(fldPath.Index(i).Child("Replicas"),
					s.Replicas, `replicas must be positive number, or a percentage with "0%" < canaryReplicas <= "100%"`)}
			}
			// is partiton-style release
			// and has traffic strategy for this step
			// and replicas is in percentage format and greater than replicasLimitWithTraffic
			if c.style == string(appsv1alpha1.PartitionRollingStyle) &&
				IsPercentageCanaryReplicasType(s.Replicas) && canaryReplicas > PartitionReplicasLimitWithTraffic &&
				(s.Matches != nil || s.Weight != nil) {
				return field.ErrorList{field.Invalid(fldPath.Index(i).Child("steps"), steps,
					`For patition style rollout: step[x].replicas must not greater than replicasLimitWithTraffic if traffic or matches specified`)}
			}
		} else {
			// replicas is nil, weight is not nil
			if c.style == string(appsv1alpha1.PartitionRollingStyle) && *s.Weight > int32(PartitionReplicasLimitWithTraffic) {
				return field.ErrorList{field.Invalid(fldPath.Index(i).Child("steps"), steps,
					`For patition style rollout: step[x].weight must not greater than replicasLimitWithTraffic if replicas is not specified`)}
			}
			if *s.Weight <= 0 || *s.Weight > 100 {
				return field.ErrorList{field.Invalid(fldPath.Index(i).Child("Weight"), s.Weight,
					`weight must be positive number, and less than or equal to replicasLimitWithTraffic (defaults to 30)`)}
			}
		}
	}

	for i := 1; i < stepCount; i++ {
		prev := &steps[i-1]
		curr := &steps[i]
		if isTraffic && curr.Weight != nil && prev.Weight != nil && *curr.Weight < *prev.Weight {
			return field.ErrorList{field.Invalid(fldPath.Child("Weight"), steps, `Steps.Weight must be a non decreasing sequence`)}
		}

		// if they are comparable, then compare them
		if IsPercentageCanaryReplicasType(prev.Replicas) != IsPercentageCanaryReplicasType(curr.Replicas) {
			continue
		}

		prevCanaryReplicas, _ := intstr.GetScaledValueFromIntOrPercent(prev.Replicas, 100, true)
		currCanaryReplicas, _ := intstr.GetScaledValueFromIntOrPercent(curr.Replicas, 100, true)
		if prev.Replicas == nil {
			prevCanaryReplicas = int(*prev.Weight)
		}
		if curr.Replicas == nil {
			currCanaryReplicas = int(*curr.Weight)
		}
		if currCanaryReplicas < prevCanaryReplicas {
			return field.ErrorList{field.Invalid(fldPath.Child("CanaryReplicas"), steps, `Steps.CanaryReplicas must be a non decreasing sequence`)}
		}
	}

	return nil
}

func IsSameV1alpha1WorkloadRefGVKName(a, b *appsv1alpha1.WorkloadRef) bool {
	if a == nil || b == nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func GetContextFromv1alpha1Rollout(rollout *appsv1alpha1.Rollout) *validateContext {
	if rollout.Spec.Strategy.Canary == nil {
		return nil
	}
	style := appsv1alpha1.PartitionRollingStyle
	switch strings.ToLower(rollout.Annotations[appsv1alpha1.RolloutStyleAnnotation]) {
	case "", strings.ToLower(string(appsv1alpha1.CanaryRollingStyle)):
		targetRef := rollout.Spec.ObjectRef.WorkloadRef
		if targetRef.APIVersion == apps.SchemeGroupVersion.String() && targetRef.Kind == reflect.TypeOf(apps.Deployment{}).Name() {
			style = appsv1alpha1.CanaryRollingStyle
		}
	}

	return &validateContext{style: string(style)}
}
