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
	"k8s.io/client-go/tools/record"
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
	statusWriter *partitionstyle.MinReadyStatusWriter
}

func (mc *MinReadyControl) IsMinReadyControl() bool {
	return true
}

func (mc *MinReadyControl) BindMinReadyStatus(release *v1beta1.BatchRelease, status *v1beta1.BatchReleaseStatus, recorder record.EventRecorder) {
	mc.statusWriter = partitionstyle.NewMinReadyStatusWriter(release, status, recorder)
}

func (mc *MinReadyControl) RecordOperationFailed(reason string, err error) {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordDegraded(reason, err)
	}
}

func (mc *MinReadyControl) RecordZeroReplicaBatching() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatching", "MinReadySeconds strategy has no replicas to upgrade")
	}
}

func (mc *MinReadyControl) RecordBatchAdvanced() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatching", "MinReadySeconds strategy advanced the current batch")
	}
}

func (mc *MinReadyControl) RecordZeroReplicaBatchReady() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")
	}
}

func (mc *MinReadyControl) RecordBatchReady() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")
	}
}

func (mc *MinReadyControl) ObserveBatchWait() {
	if mc.statusWriter == nil {
		return
	}
	status := mc.statusWriter.BatchReleaseStatus()
	if status == nil {
		return
	}
	condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyBatching)
	partitionstyle.ObserveMinReadyBatchWait(mc.statusWriter.BatchRelease(), condition)
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
	return &MinReadyControl{realController: rc, statusWriter: mc.statusWriter}, nil
}

func (mc *MinReadyControl) Initialize(ctx context.Context, release *v1beta1.BatchRelease) error {
	if release == nil {
		err := fmt.Errorf("MinReadyControl.Initialize: release is nil")
		mc.RecordOperationFailed("MinReadyInitializeFailed", err)
		return err
	}
	if err := mc.ensureInitializeAllowed(); err != nil {
		wrapped := fmt.Errorf("MinReadyControl.Initialize: %w", err)
		mc.RecordOperationFailed("MinReadyInitializeFailed", wrapped)
		return wrapped
	}
	original := mc.object
	modified := mc.object.DeepCopy()
	if err := prepareOriginalAnnotations(original, modified); err != nil {
		wrapped := fmt.Errorf("MinReadyControl.Initialize: %w", err)
		mc.RecordOperationFailed("MinReadyInitializeFailed", wrapped)
		return wrapped
	}
	modified.Annotations[util.BatchReleaseControlAnnotation] = util.DumpJSON(metav1.NewControllerRef(
		release, release.GetObjectKind().GroupVersionKind()))
	inflateDeploymentStrategy(modified)
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	if err := mc.client.Patch(ctx, modified, patch); err != nil {
		wrapped := fmt.Errorf("MinReadyControl.Initialize: %w", err)
		mc.RecordOperationFailed("MinReadyInitializeFailed", wrapped)
		return wrapped
	}
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyInitialized, "MinReadyInitialized", "MinReadySeconds strategy initialized")
	}
	return nil
}

func (mc *MinReadyControl) UpgradeBatch(ctx context.Context, batchContext *batchcontext.BatchContext) error {
	if err := mc.ensureInflatedDeploymentStrategy(ctx); err != nil {
		wrapped := fmt.Errorf("MinReadyControl.UpgradeBatch[%d]: %w", batchContext.CurrentBatch, err)
		mc.RecordOperationFailed("MinReadyBatchingFailed", wrapped)
		return wrapped
	}
	current, err := intstr.GetScaledValueFromIntOrPercent(
		mc.object.Spec.Strategy.RollingUpdate.MaxUnavailable, int(batchContext.Replicas), true)
	if err != nil {
		wrapped := fmt.Errorf("MinReadyControl.UpgradeBatch[%d]: %w", batchContext.CurrentBatch, err)
		mc.RecordOperationFailed("MinReadyBatchingFailed", wrapped)
		return wrapped
	}
	target := batchContext.DesiredUpdatedReplicas
	if int32(current) == target {
		return nil
	}
	if int32(current) > target {
		// maxUnavailable above the batch target is a legal state after a
		// scale-down (HPA or manual) and also self-heals external tampering;
		// converge it back to the target instead of reporting degraded drift.
		warningS(nil, "MinReady maxUnavailable exceeds target, reducing",
			"batch", batchContext.CurrentBatch, "deployment", klog.KObj(mc.object),
			"maxUnavailable", current, "target", target)
	}
	original := mc.object
	modified := mc.object.DeepCopy()
	maxUnavailable := intstr.FromInt(int(target))
	modified.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	if err := mc.client.Patch(ctx, modified, patch); err != nil {
		wrapped := fmt.Errorf("MinReadyControl.UpgradeBatch[%d]: %w", batchContext.CurrentBatch, err)
		mc.RecordOperationFailed("MinReadyBatchingFailed", wrapped)
		return wrapped
	}
	return nil
}

func (mc *MinReadyControl) Finalize(ctx context.Context, _ *v1beta1.BatchRelease) error {
	if mc.object == nil {
		return nil
	}
	if !hasAnyOriginalAnnotation(mc.object.Annotations) {
		if hasInflatedDeploymentFields(mc.object) {
			err := fmt.Errorf("MinReadyControl.Finalize: annotation state missing while deployment fields are still inflated: %w",
				partitionstyle.ErrMinReadyAnnotationInvalid)
			mc.RecordOperationFailed("MinReadyFinalizeFailed", err)
			return err
		}
		return nil
	}
	original := mc.object
	restored, err := parseOriginalDeploymentStrategy(original.Annotations)
	if err != nil {
		wrapped := fmt.Errorf("MinReadyControl.Finalize: %w", err)
		mc.RecordOperationFailed("MinReadyFinalizeFailed", wrapped)
		return wrapped
	}
	modified := mc.object.DeepCopy()
	applyOriginalDeploymentStrategy(modified, restored)
	for _, key := range AllOriginalAnnotations {
		delete(modified.Annotations, key)
	}
	delete(modified.Annotations, util.BatchReleaseControlAnnotation)
	delete(modified.Labels, v1alpha1.DeploymentStableRevisionLabel)
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	if err := mc.client.Patch(ctx, modified, patch); err != nil {
		wrapped := fmt.Errorf("MinReadyControl.Finalize: %w", err)
		mc.RecordOperationFailed("MinReadyFinalizeFailed", wrapped)
		return wrapped
	}
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyFinalized, "MinReadyFinalized", "MinReadySeconds strategy finalized")
	}
	return nil
}

func (mc *MinReadyControl) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	rolloutID := release.Spec.ReleasePlan.RolloutID
	if rolloutID != "" {
		if _, err := mc.ListOwnedPods(); err != nil {
			wrapped := fmt.Errorf("MinReadyControl.CalculateBatchContext: %w", err)
			mc.RecordOperationFailed("MinReadyBatchingFailed", wrapped)
			return nil, wrapped
		}
	}

	currentBatch := release.Status.CanaryStatus.CurrentBatch
	desiredPartition := release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas
	desiredUpdatedReplicas, err := minReadyDesiredUpdatedReplicas(desiredPartition, mc.object)
	if err != nil {
		wrapped := fmt.Errorf("MinReadyControl.CalculateBatchContext: %w", err)
		mc.RecordOperationFailed("MinReadyBatchingFailed", wrapped)
		return nil, wrapped
	}
	updatedReadyReplicas, err := mc.minReadyUpdatedReadyReplicas(release.Status.UpdateRevision)
	if err != nil {
		wrapped := fmt.Errorf("MinReadyControl.CalculateBatchContext: %w", err)
		mc.RecordOperationFailed("MinReadyBatchingFailed", wrapped)
		return nil, wrapped
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

func prepareOriginalAnnotations(deployment, writeTarget *apps.Deployment) error {
	if !hasAnyOriginalAnnotation(deployment.Annotations) {
		writeOriginalAnnotations(deployment, writeTarget)
		return nil
	}
	if err := ensureOriginalAnnotations(deployment); err != nil {
		return err
	}
	return validateInflatedDeploymentStrategy(deployment)
}

func ensureOriginalAnnotations(deployment *apps.Deployment) error {
	_, err := parseOriginalDeploymentStrategy(deployment.Annotations)
	return err
}

func writeOriginalAnnotations(original, modified *apps.Deployment) {
	if modified.Annotations == nil {
		modified.Annotations = map[string]string{}
	}
	writeOriginalAvailabilityAnnotations(original, modified)
	modified.Annotations[AnnotationOriginalMaxUnavailable] = serializeOriginalIntOrString(originalMaxUnavailable(original))
}

func writeOriginalAvailabilityAnnotations(original, modified *apps.Deployment) {
	if modified.Annotations == nil {
		modified.Annotations = map[string]string{}
	}
	modified.Annotations[AnnotationOriginalMinReadySeconds] = serializeOriginalInt32(&original.Spec.MinReadySeconds)
	modified.Annotations[AnnotationOriginalProgressDeadlineSeconds] = serializeOriginalInt32(original.Spec.ProgressDeadlineSeconds)
}

func originalMaxUnavailable(deployment *apps.Deployment) *intstr.IntOrString {
	if deployment.Spec.Strategy.RollingUpdate == nil {
		return nil
	}
	return deployment.Spec.Strategy.RollingUpdate.MaxUnavailable
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
}

// EnrollMinReadyDeployment snapshots the original strategy fields into
// annotations and inflates them in place. The workload mutating webhook calls
// it when a Deployment enters rollout progressing, so the native controller
// never observes the original maxUnavailable/minReadySeconds budget between
// admission and MinReadyControl.Initialize. If a continuous release updates the
// user-owned availability fields while MinReady annotations already exist,
// enrollment refreshes those original annotations before re-inflating.
func EnrollMinReadyDeployment(deployment *apps.Deployment) error {
	if err := validateDeploymentStrategyType(deployment); err != nil {
		return err
	}
	snapshot := deployment.DeepCopy()
	if err := enrollOriginalAnnotations(snapshot, deployment); err != nil {
		return err
	}
	inflateDeploymentStrategy(deployment)
	return nil
}

func enrollOriginalAnnotations(snapshot, target *apps.Deployment) error {
	if !hasAnyOriginalAnnotation(snapshot.Annotations) {
		writeOriginalAnnotations(snapshot, target)
		return nil
	}
	if err := ensureOriginalAnnotations(snapshot); err != nil {
		return err
	}
	if err := validateInflatedDeploymentStrategy(snapshot); err != nil {
		if !hasOriginalAvailabilityChange(snapshot) {
			return err
		}
		if err := validateMinReadyRefreshableDeployment(snapshot); err != nil {
			return err
		}
		writeOriginalAvailabilityAnnotations(snapshot, target)
	}
	return nil
}

func (mc *MinReadyControl) ensureInflatedDeploymentStrategy(ctx context.Context) error {
	if err := validateDeploymentStrategyType(mc.object); err != nil {
		return err
	}
	if validateInflatedDeploymentStrategy(mc.object) == nil {
		return nil
	}
	original := mc.object
	modified := mc.object.DeepCopy()
	inflateDeploymentStrategy(modified)
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	if err := mc.client.Patch(ctx, modified, patch); err != nil {
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

func hasOriginalAvailabilityChange(deployment *apps.Deployment) bool {
	if deployment.Spec.MinReadySeconds != InflatedMinReadySeconds {
		return true
	}
	return deployment.Spec.ProgressDeadlineSeconds == nil ||
		*deployment.Spec.ProgressDeadlineSeconds != InflatedProgressDeadlineSeconds
}

func validateMinReadyRefreshableDeployment(deployment *apps.Deployment) error {
	if deployment.Spec.Paused {
		return fmt.Errorf("%w: deployment is paused", partitionstyle.ErrMinReadyDriftDetected)
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

type originalDeploymentStrategy struct {
	minReadySeconds         *int32
	progressDeadlineSeconds *int32
	maxUnavailable          *intstr.IntOrString
}

func parseOriginalDeploymentStrategy(annotations map[string]string) (*originalDeploymentStrategy, error) {
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
	return &originalDeploymentStrategy{
		minReadySeconds:         minReadySeconds,
		progressDeadlineSeconds: progressDeadlineSeconds,
		maxUnavailable:          maxUnavailable,
	}, nil
}

func applyOriginalDeploymentStrategy(deployment *apps.Deployment, original *originalDeploymentStrategy) {
	deployment.Spec.MinReadySeconds = 0
	if original.minReadySeconds != nil {
		deployment.Spec.MinReadySeconds = *original.minReadySeconds
	}
	deployment.Spec.ProgressDeadlineSeconds = original.progressDeadlineSeconds
	if original.maxUnavailable == nil && (deployment.Spec.Strategy.RollingUpdate == nil ||
		deployment.Spec.Strategy.RollingUpdate.MaxSurge == nil) {
		deployment.Spec.Strategy.RollingUpdate = nil
		return
	}
	if deployment.Spec.Strategy.RollingUpdate == nil {
		deployment.Spec.Strategy.RollingUpdate = &apps.RollingUpdateDeployment{}
	}
	deployment.Spec.Strategy.RollingUpdate.MaxUnavailable = original.maxUnavailable
}

// EventDegradedDriftDetected is the warning event reason recorded when
// external drift of the inflated fields is detected. It equals the sentinel
// error text so events, metrics and errors.Is classification stay in sync.
var EventDegradedDriftDetected = partitionstyle.ErrMinReadyDriftDetected.Error()

var _ partitionstyle.Interface = (*MinReadyControl)(nil)
var _ partitionstyle.MinReadyStatusBinder = (*MinReadyControl)(nil)
var _ partitionstyle.MinReadyLifecycle = (*MinReadyControl)(nil)
