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

func (mc *MinReadyControl) RecordInitialized() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyInitialized, "MinReadyInitialized", "MinReadySeconds strategy initialized")
	}
}

func (mc *MinReadyControl) RecordFinalized() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyFinalized, "MinReadyFinalized", "MinReadySeconds strategy finalized")
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
		return fmt.Errorf("MinReadyControl.Initialize: release is nil")
	}
	if err := mc.ensureInitializeAllowed(); err != nil {
		return fmt.Errorf("MinReadyControl.Initialize: %w", err)
	}
	original := mc.object
	modified := mc.object.DeepCopy()
	if err := prepareOriginalAnnotations(original, modified); err != nil {
		return fmt.Errorf("MinReadyControl.Initialize: %w", err)
	}
	modified.Annotations[util.BatchReleaseControlAnnotation] = util.DumpJSON(metav1.NewControllerRef(
		release, release.GetObjectKind().GroupVersionKind()))
	inflateDeploymentStrategy(modified)
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	if err := mc.client.Patch(ctx, modified, patch); err != nil {
		return fmt.Errorf("MinReadyControl.Initialize: %w", err)
	}
	return nil
}

func (mc *MinReadyControl) UpgradeBatch(ctx context.Context, batchContext *batchcontext.BatchContext) error {
	if err := mc.ensureInflatedDeploymentStrategy(ctx); err != nil {
		return fmt.Errorf("MinReadyControl.UpgradeBatch[%d]: %w", batchContext.CurrentBatch, err)
	}
	return mc.reconcileMaxUnavailable(ctx, batchContext)
}

func (mc *MinReadyControl) ReconcileMaxUnavailableDrift(ctx context.Context, batchContext *batchcontext.BatchContext) error {
	if err := mc.ensureInflatedDeploymentStrategy(ctx); err != nil {
		return fmt.Errorf("MinReadyControl.ReconcileMaxUnavailableDrift[%d]: %w", batchContext.CurrentBatch, err)
	}
	return mc.reconcileMaxUnavailable(ctx, batchContext)
}

func (mc *MinReadyControl) reconcileMaxUnavailable(ctx context.Context, batchContext *batchcontext.BatchContext) error {
	if err := mc.refreshDeployment(ctx); err != nil {
		return fmt.Errorf("MinReadyControl.reconcileMaxUnavailable[%d]: %w", batchContext.CurrentBatch, err)
	}
	current, err := intstr.GetScaledValueFromIntOrPercent(
		mc.object.Spec.Strategy.RollingUpdate.MaxUnavailable, int(batchContext.Replicas), true)
	if err != nil {
		return fmt.Errorf("MinReadyControl.reconcileMaxUnavailable[%d]: %w", batchContext.CurrentBatch, err)
	}
	target := batchContext.DesiredUpdatedReplicas

	// At or above the batch target there is nothing to advance. When current
	// exceeds the target (HPA scale-down or external tampering) converge it
	// back down so the native controller never holds a wider budget than this
	// batch needs.
	if int32(current) >= target {
		if int32(current) == target {
			return nil
		}
		warningS(nil, "MinReady maxUnavailable exceeds target, reducing",
			"batch", batchContext.CurrentBatch, "deployment", klog.KObj(mc.object),
			"maxUnavailable", current, "target", target)
		return mc.patchMaxUnavailable(ctx, int(target))
	}

	// Sliding window (P0-3): advance maxUnavailable by the user's original
	// budget one step at a time, waiting for the current window's pods to
	// become available before widening the budget again. Without this, a large
	// batch target (e.g. 99 after a 1-pod canary) is written in a single patch
	// and the native controller tears down far more pods than the user's
	// declared maxUnavailable in one shot, breaking the anti-disturbance safety
	// the batched release is supposed to provide.
	step, err := mc.maxUnavailableStep(batchContext.Replicas)
	if err != nil {
		return fmt.Errorf("MinReadyControl.reconcileMaxUnavailable[%d]: %w", batchContext.CurrentBatch, err)
	}
	if step <= 0 {
		// maxUnavailable=0 means the user relies on maxSurge for concurrency
		// control; there is no budget to slide, so drive the batch directly.
		return mc.patchMaxUnavailable(ctx, int(target))
	}
	if batchContext.UpdatedReadyReplicas < int32(current) {
		// current window not yet filled; keep the budget and wait for readiness
		return nil
	}
	next := current + step
	if int32(next) > target {
		next = int(target)
	}
	return mc.patchMaxUnavailable(ctx, next)
}

// maxUnavailableStep returns the user's original maxUnavailable scaled to the
// replica count; the sliding window uses it as the advancement stride.
func (mc *MinReadyControl) maxUnavailableStep(replicas int32) (int, error) {
	original, err := parseOriginalDeploymentStrategy(mc.object.Annotations)
	if err != nil {
		return 0, err
	}
	step := intstr.FromString(DefaultMaxUnavailable)
	if original.maxUnavailable != nil {
		step = *original.maxUnavailable
	}
	return intstr.GetScaledValueFromIntOrPercent(&step, int(replicas), true)
}

// patchMaxUnavailable writes the given integer maxUnavailable back to the
// Deployment with an optimistic-lock patch and refreshes the cached object.
func (mc *MinReadyControl) patchMaxUnavailable(ctx context.Context, value int) error {
	original := mc.object
	modified := mc.object.DeepCopy()
	maxUnavailable := intstr.FromInt(value)
	modified.Spec.Strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	patch := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
	if err := mc.client.Patch(ctx, modified, patch); err != nil {
		return fmt.Errorf("MinReadyControl.reconcileMaxUnavailable: %w", err)
	}
	mc.object = modified
	return nil
}

func (mc *MinReadyControl) refreshDeployment(ctx context.Context) error {
	if mc.realController == nil {
		return fmt.Errorf("deployment is not loaded")
	}
	object := &apps.Deployment{}
	if err := mc.client.Get(ctx, mc.key, object); err != nil {
		return err
	}
	mc.object = object
	mc.WorkloadInfo = mc.getWorkloadInfo(object)
	return nil
}

func (mc *MinReadyControl) Finalize(ctx context.Context, _ *v1beta1.BatchRelease) error {
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
	original := mc.object
	restored, err := parseOriginalDeploymentStrategy(original.Annotations)
	if err != nil {
		return fmt.Errorf("MinReadyControl.Finalize: %w", err)
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
		return fmt.Errorf("MinReadyControl.Finalize: %w", err)
	}
	return nil
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
var _ partitionstyle.MinReadyDriftReconciler = (*MinReadyControl)(nil)
