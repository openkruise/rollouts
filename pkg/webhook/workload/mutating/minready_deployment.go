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

package mutating

import (
	"fmt"
	"strconv"
	"strings"

	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	appsv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
)

const (
	inflatedMinReadySeconds         int32 = appsv1beta1.MaxReadySeconds
	inflatedProgressDeadlineSeconds int32 = appsv1beta1.MaxProgressSeconds
)

// enrollMinReadyDeployment snapshots the original strategy fields into
// annotations and inflates them in place. It lives in the webhook package so
// admission code does not depend on controller internals.
func enrollMinReadyDeployment(deployment *apps.Deployment) error {
	if err := validateMinReadyDeploymentStrategyType(deployment); err != nil {
		return err
	}
	snapshot := deployment.DeepCopy()
	if err := enrollMinReadyOriginalAnnotations(snapshot, deployment); err != nil {
		return err
	}
	inflateMinReadyDeploymentStrategy(deployment)
	return nil
}

func enrollMinReadyOriginalAnnotations(snapshot, target *apps.Deployment) error {
	if !appsv1beta1.HasMinReadyOriginalAnnotations(snapshot.Annotations) {
		writeMinReadyOriginalAnnotations(snapshot, target)
		return nil
	}
	if err := ensureMinReadyOriginalAnnotations(snapshot); err != nil {
		return err
	}
	if err := validateMinReadyInflatedDeploymentStrategy(snapshot); err != nil {
		if !hasMinReadyOriginalAvailabilityChange(snapshot) {
			return err
		}
		if err := validateMinReadyRefreshableDeployment(snapshot); err != nil {
			return err
		}
		writeMinReadyOriginalAvailabilityAnnotations(snapshot, target)
	}
	return nil
}

func writeMinReadyOriginalAnnotations(original, modified *apps.Deployment) {
	writeMinReadyOriginalAvailabilityAnnotations(original, modified)
	modified.Annotations[appsv1beta1.MinReadyOriginalMaxUnavailableAnnotation] =
		serializeMinReadyOriginalIntOrString(originalMinReadyMaxUnavailable(original))
}

func writeMinReadyOriginalAvailabilityAnnotations(original, modified *apps.Deployment) {
	if modified.Annotations == nil {
		modified.Annotations = map[string]string{}
	}
	modified.Annotations[appsv1beta1.MinReadyOriginalMinReadySecondsAnnotation] =
		serializeMinReadyOriginalInt32(&original.Spec.MinReadySeconds, 0)
	modified.Annotations[appsv1beta1.MinReadyOriginalProgressDeadlineSecondsAnnotation] =
		serializeMinReadyOriginalInt32(original.Spec.ProgressDeadlineSeconds, appsv1beta1.MinReadyDefaultProgressDeadlineSeconds)
}

func serializeMinReadyOriginalInt32(value *int32, defaultValue int32) string {
	if value == nil {
		return strconv.FormatInt(int64(defaultValue), 10)
	}
	return strconv.FormatInt(int64(*value), 10)
}

func serializeMinReadyOriginalIntOrString(value *intstr.IntOrString) string {
	if value == nil {
		return appsv1beta1.MinReadyDefaultMaxUnavailable
	}
	if value.Type == intstr.String {
		return value.StrVal
	}
	return strconv.FormatInt(int64(value.IntVal), 10)
}

func originalMinReadyMaxUnavailable(deployment *apps.Deployment) *intstr.IntOrString {
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return nil
	}
	return deployment.Spec.Strategy.RollingUpdate.MaxUnavailable
}

func ensureMinReadyOriginalAnnotations(deployment *apps.Deployment) error {
	if _, err := parseMinReadyOriginalInt32(deployment.Annotations, appsv1beta1.MinReadyOriginalMinReadySecondsAnnotation); err != nil {
		return err
	}
	if _, err := parseMinReadyOriginalInt32(deployment.Annotations, appsv1beta1.MinReadyOriginalProgressDeadlineSecondsAnnotation); err != nil {
		return err
	}
	if _, err := parseMinReadyOriginalIntOrString(deployment.Annotations, appsv1beta1.MinReadyOriginalMaxUnavailableAnnotation); err != nil {
		return err
	}
	return nil
}

func parseMinReadyOriginalInt32(annotations map[string]string, key string) (*int32, error) {
	raw, ok := annotations[key]
	if !ok {
		return nil, fmt.Errorf("annotation %s missing", key)
	}
	if raw == "" {
		return nil, fmt.Errorf("annotation %s present but empty", key)
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("annotation %s malformed int32: %v", key, err)
	}
	v := int32(n)
	return &v, nil
}

func parseMinReadyOriginalIntOrString(annotations map[string]string, key string) (*intstr.IntOrString, error) {
	raw, ok := annotations[key]
	if !ok {
		return nil, fmt.Errorf("annotation %s missing", key)
	}
	if raw == "" {
		return nil, fmt.Errorf("annotation %s present but empty", key)
	}
	if strings.HasSuffix(raw, "%") {
		if _, err := strconv.Atoi(strings.TrimSuffix(raw, "%")); err != nil {
			return nil, fmt.Errorf("annotation %s malformed percent: %v", key, err)
		}
		v := intstr.FromString(raw)
		return &v, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("annotation %s malformed int: %v", key, err)
	}
	v := intstr.FromInt(n)
	return &v, nil
}

func inflateMinReadyDeploymentStrategy(deployment *apps.Deployment) {
	progressDeadlineSeconds := inflatedProgressDeadlineSeconds
	maxUnavailable := intstr.FromInt(0)
	deployment.Spec.Paused = false
	deployment.Spec.MinReadySeconds = inflatedMinReadySeconds
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadlineSeconds
	if deployment.Spec.Strategy.RollingUpdate == nil {
		deployment.Spec.Strategy.RollingUpdate = &apps.RollingUpdateDeployment{}
	}
	deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
}

func validateMinReadyInflatedDeploymentStrategy(deployment *apps.Deployment) error {
	if err := validateMinReadyDeploymentStrategyType(deployment); err != nil {
		return err
	}
	if deployment.Spec.Paused {
		return fmt.Errorf("deployment is paused")
	}
	if deployment.Spec.MinReadySeconds != inflatedMinReadySeconds {
		return fmt.Errorf("minReadySeconds=%d want %d", deployment.Spec.MinReadySeconds, inflatedMinReadySeconds)
	}
	if deployment.Spec.ProgressDeadlineSeconds == nil || *deployment.Spec.ProgressDeadlineSeconds != inflatedProgressDeadlineSeconds {
		return fmt.Errorf("progressDeadlineSeconds=%v want %d", deployment.Spec.ProgressDeadlineSeconds, inflatedProgressDeadlineSeconds)
	}
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return fmt.Errorf("rollingUpdate is nil")
	}
	return nil
}

func hasMinReadyOriginalAvailabilityChange(deployment *apps.Deployment) bool {
	if deployment.Spec.MinReadySeconds != inflatedMinReadySeconds {
		return true
	}
	return deployment.Spec.ProgressDeadlineSeconds == nil ||
		*deployment.Spec.ProgressDeadlineSeconds != inflatedProgressDeadlineSeconds
}

func validateMinReadyRefreshableDeployment(deployment *apps.Deployment) error {
	if deployment.Spec.Paused {
		return fmt.Errorf("deployment is paused")
	}
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return fmt.Errorf("rollingUpdate is nil")
	}
	return nil
}

func validateMinReadyDeploymentStrategyType(deployment *apps.Deployment) error {
	if deployment.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
		return fmt.Errorf("deployment strategy type %s is not RollingUpdate", deployment.Spec.Strategy.Type)
	}
	return nil
}
