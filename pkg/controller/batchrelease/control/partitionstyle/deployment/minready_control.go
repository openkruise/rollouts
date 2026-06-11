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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
	"github.com/openkruise/rollouts/pkg/feature"
	"github.com/openkruise/rollouts/pkg/util"
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

func (mc *MinReadyControl) Initialize(release *v1beta1.BatchRelease) error {
	if release == nil {
		return fmt.Errorf("MinReadyControl.Initialize: release is nil")
	}
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
	modified.Annotations[util.BatchReleaseControlAnnotation] = util.DumpJSON(metav1.NewControllerRef(
		release, release.GetObjectKind().GroupVersionKind()))
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
	if int32(current) == target {
		return nil
	}
	if int32(current) > target {
		// maxUnavailable above the batch target is a legal state after a
		// scale-down (HPA or manual) and also self-heals external tampering;
		// converge it back to the target instead of reporting degraded drift.
		klog.Warningf("MinReadyControl.UpgradeBatch[%d]: deployment %v maxUnavailable=%d exceeds target=%d, reducing it to the target",
			ctx.CurrentBatch, klog.KObj(mc.object), current, target)
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
			return fmt.Errorf("MinReadyControl.Finalize: annotation state missing while deployment fields are still inflated: %w",
				partitionstyle.ErrMinReadyAnnotationInvalid)
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
	delete(modified.Annotations, util.BatchReleaseControlAnnotation)
	delete(modified.Labels, v1alpha1.DeploymentStableRevisionLabel)
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
		return fmt.Errorf("%s %w", feature.MinReadySecondsStrategy, partitionstyle.ErrMinReadyFeatureGateDisabled)
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
	// MinReady keeps the native controller running; a paused Deployment would
	// freeze silently, so pausing is always reverted together with inflation.
	deployment.Spec.Paused = false
	deployment.Spec.MinReadySeconds = InflatedMinReadySeconds
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadlineSeconds
	if deployment.Spec.Strategy.RollingUpdate == nil {
		deployment.Spec.Strategy.RollingUpdate = &apps.RollingUpdateDeployment{}
	}
	deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	applyMaxSurgeValidationFallback(deployment)
}

// EnrollMinReadyDeployment snapshots the original strategy fields into
// annotations and inflates them in place. The workload mutating webhook calls
// it when a Deployment enters rollout progressing, so the native controller
// never observes the original maxUnavailable/minReadySeconds budget between
// admission and MinReadyControl.Initialize; Initialize stays the fallback and
// validates (instead of rewriting) annotations that already exist.
func EnrollMinReadyDeployment(deployment *apps.Deployment) error {
	if err := validateDeploymentStrategyType(deployment); err != nil {
		return err
	}
	snapshot := deployment.DeepCopy()
	if err := writeOriginalAnnotations(snapshot, deployment); err != nil {
		return err
	}
	if hasAnyOriginalAnnotation(snapshot.Annotations) {
		if err := validateInflatedDeploymentStrategy(snapshot); err != nil {
			return err
		}
	}
	inflateDeploymentStrategy(deployment)
	return nil
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
	if deployment.Spec.Paused {
		// A paused Deployment silently freezes the native controller; surface
		// it through the degraded channel instead of waiting without signal.
		return fmt.Errorf("%w: deployment is paused", partitionstyle.ErrMinReadyDriftDetected)
	}
	if deployment.Spec.MinReadySeconds != InflatedMinReadySeconds {
		return fmt.Errorf("%w: minReadySeconds=%d want %d",
			partitionstyle.ErrMinReadyDriftDetected, deployment.Spec.MinReadySeconds, InflatedMinReadySeconds)
	}
	if deployment.Spec.ProgressDeadlineSeconds == nil || *deployment.Spec.ProgressDeadlineSeconds != InflatedProgressDeadlineSeconds {
		return fmt.Errorf("%w: progressDeadlineSeconds=%v want %d",
			partitionstyle.ErrMinReadyDriftDetected, deployment.Spec.ProgressDeadlineSeconds, InflatedProgressDeadlineSeconds)
	}
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return fmt.Errorf("%w: rollingUpdate is nil", partitionstyle.ErrMinReadyDriftDetected)
	}
	return nil
}

func validateDeploymentStrategyType(deployment *apps.Deployment) error {
	if deployment.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
		return fmt.Errorf("%w: deployment strategy type %s is not RollingUpdate",
			partitionstyle.ErrMinReadyDriftDetected, deployment.Spec.Strategy.Type)
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

// EventDegradedDriftDetected is the warning event reason recorded when
// external drift of the inflated fields is detected. It equals the sentinel
// error text so events, metrics and errors.Is classification stay in sync.
var EventDegradedDriftDetected = partitionstyle.ErrMinReadyDriftDetected.Error()

var _ partitionstyle.Interface = (*MinReadyControl)(nil)
