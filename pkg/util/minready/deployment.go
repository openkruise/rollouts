/*
Copyright 2026 The Kruise Authors.

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

package minready

import (
	"fmt"
	"strconv"

	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openkruise/rollouts/api/v1beta1"
)

const (
	AnnotationOriginalMinReadySeconds         = v1beta1.MinReadyOriginalMinReadySecondsAnnotation
	AnnotationOriginalProgressDeadlineSeconds = v1beta1.MinReadyOriginalProgressDeadlineSecondsAnnotation
	AnnotationOriginalMaxUnavailable          = v1beta1.MinReadyOriginalMaxUnavailableAnnotation

	DefaultProgressDeadlineSeconds int32 = v1beta1.MinReadyDefaultProgressDeadlineSeconds
	DefaultMaxUnavailable                = v1beta1.MinReadyDefaultMaxUnavailable

	InflatedMinReadySeconds         int32 = v1beta1.MaxReadySeconds
	InflatedProgressDeadlineSeconds int32 = v1beta1.MaxProgressSeconds
)

var AllOriginalAnnotations = v1beta1.MinReadyOriginalAnnotations

type OriginalDeploymentStrategy struct {
	MinReadySeconds         *int32
	ProgressDeadlineSeconds *int32
	MaxUnavailable          *intstr.IntOrString
}

func SerializeOriginalInt32(value *int32, defaultValue int32) string {
	if value == nil {
		return strconv.FormatInt(int64(defaultValue), 10)
	}
	return strconv.FormatInt(int64(*value), 10)
}

func SerializeOriginalIntOrString(value *intstr.IntOrString, defaultValue string) string {
	if value == nil {
		return defaultValue
	}
	return value.String()
}

func ParseOriginalInt32(raw string) (*int32, error) {
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return nil, err
	}
	v := int32(n)
	return &v, nil
}

func ParseOriginalIntOrString(raw string) (*intstr.IntOrString, error) {
	value := intstr.Parse(raw)
	if value.Type == intstr.String {
		if _, err := intstr.GetScaledValueFromIntOrPercent(&value, 1, true); err != nil {
			return nil, err
		}
	}
	return &value, nil
}

func ParseOriginalDeploymentStrategy(annotations map[string]string) (*OriginalDeploymentStrategy, error) {
	minReadySeconds, err := parseOriginalInt32Annotation(annotations, AnnotationOriginalMinReadySeconds)
	if err != nil {
		return nil, err
	}
	progressDeadlineSeconds, err := parseOriginalInt32Annotation(annotations, AnnotationOriginalProgressDeadlineSeconds)
	if err != nil {
		return nil, err
	}
	maxUnavailable, err := parseOriginalIntOrStringAnnotation(annotations, AnnotationOriginalMaxUnavailable)
	if err != nil {
		return nil, err
	}
	return &OriginalDeploymentStrategy{
		MinReadySeconds:         minReadySeconds,
		ProgressDeadlineSeconds: progressDeadlineSeconds,
		MaxUnavailable:          maxUnavailable,
	}, nil
}

func ValidateOriginalAnnotations(annotations map[string]string) error {
	_, err := ParseOriginalDeploymentStrategy(annotations)
	return err
}

func WriteOriginalAnnotations(original, modified *apps.Deployment) {
	WriteOriginalAvailabilityAnnotations(original, modified)
	WriteOriginalMaxUnavailableAnnotation(original, modified)
}

func WriteOriginalAvailabilityAnnotations(original, modified *apps.Deployment) {
	if modified.Annotations == nil {
		modified.Annotations = map[string]string{}
	}
	modified.Annotations[AnnotationOriginalMinReadySeconds] =
		SerializeOriginalInt32(&original.Spec.MinReadySeconds, 0)
	modified.Annotations[AnnotationOriginalProgressDeadlineSeconds] =
		SerializeOriginalInt32(original.Spec.ProgressDeadlineSeconds, DefaultProgressDeadlineSeconds)
}

func WriteOriginalMaxUnavailableAnnotation(original, modified *apps.Deployment) {
	if modified.Annotations == nil {
		modified.Annotations = map[string]string{}
	}
	modified.Annotations[AnnotationOriginalMaxUnavailable] =
		SerializeOriginalIntOrString(OriginalMaxUnavailable(original), DefaultMaxUnavailable)
}

func OriginalMaxUnavailable(deployment *apps.Deployment) *intstr.IntOrString {
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return nil
	}
	return deployment.Spec.Strategy.RollingUpdate.MaxUnavailable
}

func InflateDeploymentStrategy(deployment *apps.Deployment) {
	progressDeadlineSeconds := InflatedProgressDeadlineSeconds
	maxUnavailable := intstr.FromInt(0)
	deployment.Spec.Paused = false
	deployment.Spec.MinReadySeconds = InflatedMinReadySeconds
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadlineSeconds
	if deployment.Spec.Strategy.RollingUpdate == nil {
		deployment.Spec.Strategy.RollingUpdate = &apps.RollingUpdateDeployment{}
	}
	deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
}

func ValidateInflatedDeploymentStrategy(deployment *apps.Deployment) error {
	if err := ValidateDeploymentStrategyType(deployment); err != nil {
		return err
	}
	if deployment.Spec.Paused {
		return fmt.Errorf("deployment is paused")
	}
	if deployment.Spec.MinReadySeconds != InflatedMinReadySeconds {
		return fmt.Errorf("minReadySeconds=%d want %d", deployment.Spec.MinReadySeconds, InflatedMinReadySeconds)
	}
	if deployment.Spec.ProgressDeadlineSeconds == nil || *deployment.Spec.ProgressDeadlineSeconds != InflatedProgressDeadlineSeconds {
		return fmt.Errorf("progressDeadlineSeconds=%v want %d", deployment.Spec.ProgressDeadlineSeconds, InflatedProgressDeadlineSeconds)
	}
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return fmt.Errorf("rollingUpdate is nil")
	}
	return nil
}

func HasOriginalAvailabilityChange(deployment *apps.Deployment) bool {
	if deployment.Spec.MinReadySeconds != InflatedMinReadySeconds {
		return true
	}
	return deployment.Spec.ProgressDeadlineSeconds == nil ||
		*deployment.Spec.ProgressDeadlineSeconds != InflatedProgressDeadlineSeconds
}

func ValidateRefreshableDeployment(deployment *apps.Deployment) error {
	if deployment.Spec.Paused {
		return fmt.Errorf("deployment is paused")
	}
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return fmt.Errorf("rollingUpdate is nil")
	}
	return nil
}

func ValidateDeploymentStrategyType(deployment *apps.Deployment) error {
	if deployment.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
		return fmt.Errorf("deployment strategy type %s is not RollingUpdate", deployment.Spec.Strategy.Type)
	}
	return nil
}

func HasOriginalAnnotations(annotations map[string]string) bool {
	return v1beta1.HasMinReadyOriginalAnnotations(annotations)
}

func parseOriginalInt32Annotation(annotations map[string]string, key string) (*int32, error) {
	raw, err := originalAnnotationValue(annotations, key)
	if err != nil {
		return nil, err
	}
	value, err := ParseOriginalInt32(raw)
	if err != nil {
		return nil, fmt.Errorf("annotation %s malformed int32: %v", key, err)
	}
	return value, nil
}

func parseOriginalIntOrStringAnnotation(annotations map[string]string, key string) (*intstr.IntOrString, error) {
	raw, err := originalAnnotationValue(annotations, key)
	if err != nil {
		return nil, err
	}
	value, err := ParseOriginalIntOrString(raw)
	if err != nil {
		return nil, fmt.Errorf("annotation %s malformed IntOrString: %v", key, err)
	}
	return value, nil
}

func originalAnnotationValue(annotations map[string]string, key string) (string, error) {
	raw, ok := annotations[key]
	if !ok {
		return "", fmt.Errorf("annotation %s missing", key)
	}
	if raw == "" {
		return "", fmt.Errorf("annotation %s present but empty", key)
	}
	return raw, nil
}
