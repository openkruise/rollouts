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
	apps "k8s.io/api/apps/v1"

	appsv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
	minreadyutil "github.com/openkruise/rollouts/pkg/util/minready"
)

const (
	inflatedMinReadySeconds         int32 = minreadyutil.InflatedMinReadySeconds
	inflatedProgressDeadlineSeconds int32 = minreadyutil.InflatedProgressDeadlineSeconds
)

// enrollMinReadyDeployment snapshots the original strategy fields into
// annotations and inflates them in place. It lives in the webhook package so
// admission code does not depend on controller internals.
func enrollMinReadyDeployment(deployment *apps.Deployment) error {
	return enrollMinReadyDeploymentWithPrevious(deployment, nil)
}

func enrollMinReadyDeploymentWithPrevious(deployment, previous *apps.Deployment) error {
	if err := validateMinReadyDeploymentStrategyType(deployment); err != nil {
		return err
	}
	snapshot := deployment.DeepCopy()
	if err := enrollMinReadyOriginalAnnotations(snapshot, deployment, previous); err != nil {
		return err
	}
	inflateMinReadyDeploymentStrategy(deployment)
	return nil
}

func enrollMinReadyOriginalAnnotations(snapshot, target, previous *apps.Deployment) error {
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
	if hasMinReadyOriginalMaxUnavailableChange(snapshot, previous) {
		if err := validateMinReadyRefreshableDeployment(snapshot); err != nil {
			return err
		}
		writeMinReadyOriginalMaxUnavailableAnnotation(snapshot, target)
	}
	return nil
}

func writeMinReadyOriginalAnnotations(original, modified *apps.Deployment) {
	minreadyutil.WriteOriginalAnnotations(original, modified)
}

func writeMinReadyOriginalAvailabilityAnnotations(original, modified *apps.Deployment) {
	minreadyutil.WriteOriginalAvailabilityAnnotations(original, modified)
}

func writeMinReadyOriginalMaxUnavailableAnnotation(original, modified *apps.Deployment) {
	minreadyutil.WriteOriginalMaxUnavailableAnnotation(original, modified)
}

func ensureMinReadyOriginalAnnotations(deployment *apps.Deployment) error {
	return minreadyutil.ValidateOriginalAnnotations(deployment.Annotations)
}

func inflateMinReadyDeploymentStrategy(deployment *apps.Deployment) {
	minreadyutil.InflateDeploymentStrategy(deployment)
}

func validateMinReadyInflatedDeploymentStrategy(deployment *apps.Deployment) error {
	return minreadyutil.ValidateInflatedDeploymentStrategy(deployment)
}

func hasMinReadyOriginalAvailabilityChange(deployment *apps.Deployment) bool {
	return minreadyutil.HasOriginalAvailabilityChange(deployment)
}

func hasMinReadyOriginalMaxUnavailableChange(deployment, previous *apps.Deployment) bool {
	if previous == nil {
		return false
	}
	current := minreadyutil.SerializeOriginalIntOrString(
		minreadyutil.OriginalMaxUnavailable(deployment), minreadyutil.DefaultMaxUnavailable)
	old := minreadyutil.SerializeOriginalIntOrString(
		minreadyutil.OriginalMaxUnavailable(previous), minreadyutil.DefaultMaxUnavailable)
	return current != old
}

func validateMinReadyRefreshableDeployment(deployment *apps.Deployment) error {
	return minreadyutil.ValidateRefreshableDeployment(deployment)
}

func validateMinReadyDeploymentStrategyType(deployment *apps.Deployment) error {
	return minreadyutil.ValidateDeploymentStrategyType(deployment)
}
