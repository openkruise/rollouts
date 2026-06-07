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

package deployment

import (
	"context"
	"fmt"

	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
	"github.com/openkruise/rollouts/pkg/feature"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
)

type MinReadyControl struct {
	*realController
}

func (mc *MinReadyControl) BuildController() (partitionstyle.Interface, error) {
	if mc.realController == nil {
		return nil, fmt.Errorf("MinReadyControl.BuildController: realController is nil")
	}
	built, err := mc.realController.BuildController()
	if err != nil {
		return nil, err
	}
	rc, ok := built.(*realController)
	if !ok {
		return nil, fmt.Errorf("MinReadyControl.BuildController: expected *realController, got %T", built)
	}
	return &MinReadyControl{realController: rc}, nil
}

func (mc *MinReadyControl) Initialize(_ *v1beta1.BatchRelease) error {
	if err := mc.ensureInitializeAllowed(); err != nil {
		return fmt.Errorf("MinReadyControl.Initialize: %w", err)
	}
	original := mc.object.DeepCopy()
	modified := original.DeepCopy()
	if err := writeOriginalAnnotations(original, modified); err != nil {
		return fmt.Errorf("MinReadyControl.Initialize: %w", err)
	}
	if hasAnyOriginalAnnotation(original.Annotations) {
		if err := validateInflatedDeploymentStrategy(original); err != nil {
			return fmt.Errorf("MinReadyControl.Initialize: %w", err)
		}
	}
	inflateDeploymentStrategy(modified)
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	return mc.client.Patch(context.TODO(), modified, patch)
}

func (mc *MinReadyControl) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	if err := mc.ensureInflatedDeploymentStrategy(); err != nil {
		return fmt.Errorf("MinReadyControl.UpgradeBatch[%d]: %w", ctx.CurrentBatch, err)
	}
	current, err := intstr.GetScaledValueFromIntOrPercent(
		mc.object.Spec.Strategy.RollingUpdate.MaxUnavailable, int(ctx.Replicas), true)
	if err != nil {
		return fmt.Errorf("MinReadyControl.UpgradeBatch[%d]: %w", ctx.CurrentBatch, err)
	}
	target := ctx.DesiredUpdatedReplicas
	if int32(current) > target {
		return fmt.Errorf("MinReadyControl.UpgradeBatch[%d]: %s: maxUnavailable=%d exceeds target=%d",
			ctx.CurrentBatch, EventDegradedDriftDetected, current, target)
	}
	if int32(current) >= target {
		return nil
	}
	original := mc.object.DeepCopy()
	modified := original.DeepCopy()
	maxUnavailable := intstr.FromInt(int(target))
	modified.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	return mc.client.Patch(context.TODO(), modified, patch)
}

func (mc *MinReadyControl) Finalize(_ *v1beta1.BatchRelease) error {
	if mc.object == nil {
		return nil
	}
	if !hasAnyOriginalAnnotation(mc.object.Annotations) {
		if hasInflatedDeploymentFields(mc.object) {
			return fmt.Errorf("MinReadyControl.Finalize: annotation state missing while deployment fields are still inflated")
		}
		return nil
	}
	original := mc.object.DeepCopy()
	restored, err := parseOriginalDeploymentStrategy(original.Annotations)
	if err != nil {
		return fmt.Errorf("MinReadyControl.Finalize: %w", err)
	}
	modified := original.DeepCopy()
	applyOriginalDeploymentStrategy(modified, restored)
	for _, key := range AllOriginalAnnotations {
		delete(modified.Annotations, key)
	}
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	return mc.client.Patch(context.TODO(), modified, patch)
}

func (mc *MinReadyControl) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	rolloutID := release.Spec.ReleasePlan.RolloutID
	if rolloutID != "" {
		if _, err := mc.ListOwnedPods(); err != nil {
			return nil, fmt.Errorf("MinReadyControl.CalculateBatchContext: %w", err)
		}
	}

	currentBatch := release.Status.CanaryStatus.CurrentBatch
	desiredPartition := release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas
	desiredUpdatedReplicas, err := minReadyDesiredUpdatedReplicas(desiredPartition, mc.object)
	if err != nil {
		return nil, fmt.Errorf("MinReadyControl.CalculateBatchContext: %w", err)
	}
	updatedReadyReplicas, err := mc.minReadyUpdatedReadyReplicas(release.Status.UpdateRevision)
	if err != nil {
		return nil, fmt.Errorf("MinReadyControl.CalculateBatchContext: %w", err)
	}
	return &batchcontext.BatchContext{
		RolloutID:              rolloutID,
		CurrentBatch:           currentBatch,
		UpdateRevision:         release.Status.UpdateRevision,
		Replicas:               mc.Replicas,
		UpdatedReplicas:        mc.object.Status.UpdatedReplicas,
		UpdatedReadyReplicas:   updatedReadyReplicas,
		PlannedUpdatedReplicas: desiredUpdatedReplicas,
		DesiredUpdatedReplicas: desiredUpdatedReplicas,
		DesiredPartition:       desiredPartition,
		FailureThreshold:       release.Spec.ReleasePlan.FailureThreshold,
		Pods:                   mc.pods,
	}, nil
}

func (mc *MinReadyControl) ensureInitializeAllowed() error {
	if mc.realController == nil || mc.object == nil {
		return fmt.Errorf("deployment is not loaded")
	}
	if !utilfeature.DefaultFeatureGate.Enabled(feature.MinReadySecondsStrategy) {
		return fmt.Errorf("%s feature gate is disabled", feature.MinReadySecondsStrategy)
	}
	if err := validateDeploymentStrategyType(mc.object); err != nil {
		return err
	}
	return nil
}

func writeOriginalAnnotations(original, modified *apps.Deployment) error {
	if modified.Annotations == nil {
		modified.Annotations = map[string]string{}
	}
	if hasAnyOriginalAnnotation(original.Annotations) {
		_, err := parseOriginalDeploymentStrategy(original.Annotations)
		return err
	}
	modified.Annotations[AnnotationOriginalMinReadySeconds] = serializeOriginalInt32(&original.Spec.MinReadySeconds)
	modified.Annotations[AnnotationOriginalProgressDeadlineSeconds] = serializeOriginalInt32(original.Spec.ProgressDeadlineSeconds)
	modified.Annotations[AnnotationOriginalMaxUnavailable] = serializeOriginalIntOrString(originalMaxUnavailable(original))
	modified.Annotations[AnnotationOriginalMaxSurge] = serializeOriginalIntOrString(originalMaxSurge(original))
	return nil
}

func ensureAllOriginalAnnotations(annotations map[string]string) error {
	for _, key := range AllOriginalAnnotations {
		if _, err := readOriginalAnnotation(annotations, key); err != nil {
			return err
		}
	}
	return nil
}

func originalMaxUnavailable(deployment *apps.Deployment) *intstr.IntOrString {
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return nil
	}
	return deployment.Spec.Strategy.RollingUpdate.MaxUnavailable
}

func originalMaxSurge(deployment *apps.Deployment) *intstr.IntOrString {
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return nil
	}
	return deployment.Spec.Strategy.RollingUpdate.MaxSurge
}

func inflateDeploymentStrategy(deployment *apps.Deployment) {
	progressDeadlineSeconds := InflatedProgressDeadlineSeconds
	maxUnavailable := intstr.FromInt(0)
	deployment.Spec.MinReadySeconds = InflatedMinReadySeconds
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadlineSeconds
	if deployment.Spec.Strategy.RollingUpdate == nil {
		deployment.Spec.Strategy.RollingUpdate = &apps.RollingUpdateDeployment{}
	}
	deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	applyMaxSurgeValidationFallback(deployment)
}

func (mc *MinReadyControl) ensureInflatedDeploymentStrategy() error {
	if err := validateDeploymentStrategyType(mc.object); err != nil {
		return err
	}
	if validateInflatedDeploymentStrategy(mc.object) == nil {
		return nil
	}
	original := mc.object.DeepCopy()
	modified := original.DeepCopy()
	inflateDeploymentStrategy(modified)
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	if err := mc.client.Patch(context.TODO(), modified, patch); err != nil {
		return err
	}
	mc.object = modified
	return nil
}

func validateInflatedDeploymentStrategy(deployment *apps.Deployment) error {
	if err := validateDeploymentStrategyType(deployment); err != nil {
		return err
	}
	if deployment.Spec.MinReadySeconds != InflatedMinReadySeconds {
		return fmt.Errorf("%s: minReadySeconds=%d want %d",
			EventDegradedDriftDetected, deployment.Spec.MinReadySeconds, InflatedMinReadySeconds)
	}
	if deployment.Spec.ProgressDeadlineSeconds == nil || *deployment.Spec.ProgressDeadlineSeconds != InflatedProgressDeadlineSeconds {
		return fmt.Errorf("%s: progressDeadlineSeconds=%v want %d",
			EventDegradedDriftDetected, deployment.Spec.ProgressDeadlineSeconds, InflatedProgressDeadlineSeconds)
	}
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return fmt.Errorf("%s: rollingUpdate is nil", EventDegradedDriftDetected)
	}
	return nil
}

func validateDeploymentStrategyType(deployment *apps.Deployment) error {
	if deployment.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
		return fmt.Errorf("%s: deployment strategy type %s is not RollingUpdate",
			EventDegradedDriftDetected, deployment.Spec.Strategy.Type)
	}
	return nil
}

func hasInflatedDeploymentFields(deployment *apps.Deployment) bool {
	if deployment.Spec.MinReadySeconds == InflatedMinReadySeconds {
		return true
	}
	return deployment.Spec.ProgressDeadlineSeconds != nil &&
		*deployment.Spec.ProgressDeadlineSeconds == InflatedProgressDeadlineSeconds
}

func applyMaxSurgeValidationFallback(deployment *apps.Deployment) {
	if deployment.Spec.Strategy.RollingUpdate.MaxSurge == nil {
		return
	}
	replicas := int32(1)
	if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas > 0 {
		replicas = *deployment.Spec.Replicas
	}
	surge, err := intstr.GetScaledValueFromIntOrPercent(deployment.Spec.Strategy.RollingUpdate.MaxSurge, int(replicas), true)
	if err != nil || surge > 0 {
		return
	}
	maxSurge := intstr.FromInt(1)
	deployment.Spec.Strategy.RollingUpdate.MaxSurge = &maxSurge
}

type originalDeploymentStrategy struct {
	minReadySeconds         *int32
	progressDeadlineSeconds *int32
	maxUnavailable          *intstr.IntOrString
	maxSurge                *intstr.IntOrString
}

func parseOriginalDeploymentStrategy(annotations map[string]string) (*originalDeploymentStrategy, error) {
	if err := ensureAllOriginalAnnotations(annotations); err != nil {
		return nil, err
	}
	minReadySeconds, err := parseOriginalInt32(annotations, AnnotationOriginalMinReadySeconds)
	if err != nil {
		return nil, err
	}
	progressDeadlineSeconds, err := parseOriginalInt32(annotations, AnnotationOriginalProgressDeadlineSeconds)
	if err != nil {
		return nil, err
	}
	maxUnavailable, err := parseOriginalIntOrString(annotations, AnnotationOriginalMaxUnavailable)
	if err != nil {
		return nil, err
	}
	maxSurge, err := parseOriginalIntOrString(annotations, AnnotationOriginalMaxSurge)
	if err != nil {
		return nil, err
	}
	return &originalDeploymentStrategy{
		minReadySeconds:         minReadySeconds,
		progressDeadlineSeconds: progressDeadlineSeconds,
		maxUnavailable:          maxUnavailable,
		maxSurge:                maxSurge,
	}, nil
}

func applyOriginalDeploymentStrategy(deployment *apps.Deployment, original *originalDeploymentStrategy) {
	deployment.Spec.MinReadySeconds = 0
	if original.minReadySeconds != nil {
		deployment.Spec.MinReadySeconds = *original.minReadySeconds
	}
	deployment.Spec.ProgressDeadlineSeconds = original.progressDeadlineSeconds
	if original.maxUnavailable == nil && original.maxSurge == nil {
		deployment.Spec.Strategy.RollingUpdate = nil
		return
	}
	if deployment.Spec.Strategy.RollingUpdate == nil {
		deployment.Spec.Strategy.RollingUpdate = &apps.RollingUpdateDeployment{}
	}
	deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = original.maxUnavailable
	deployment.Spec.Strategy.RollingUpdate.MaxSurge = original.maxSurge
}

const EventDegradedDriftDetected = "MinReadyDegradedDriftDetected"

var _ partitionstyle.Interface = (*MinReadyControl)(nil)
