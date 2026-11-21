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
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
	"github.com/openkruise/rollouts/pkg/feature"
	"github.com/openkruise/rollouts/pkg/util"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
	minreadyutil "github.com/openkruise/rollouts/pkg/util/minready"
)

type MinReadyControl struct {
	*realController
	statusWriter *partitionstyle.MinReadyStatusWriter
}

// GetReporter returns mc itself, which implements the partitionstyle.Reporter
// interface (lifecycle recording + failure reasons). The control plane drives
// status reporting through it instead of type assertions.
func (mc *MinReadyControl) GetReporter() partitionstyle.Reporter {
	return mc
}

func (mc *MinReadyControl) BindStrategyStatus(release *v1beta1.BatchRelease, status *v1beta1.BatchReleaseStatus, recorder record.EventRecorder) {
	mc.statusWriter = partitionstyle.NewMinReadyStatusWriter(release, status, recorder)
}

func (mc *MinReadyControl) RecordOperationFailed(reason string, err error) {
	if mc.statusWriter == nil {
		klog.V(3).InfoS("MinReadyControl.RecordOperationFailed: statusWriter is nil, degraded condition will not be recorded", "reason", reason, "err", err)
		return
	}
	mc.statusWriter.RecordDegraded(reason, err)
}

func (mc *MinReadyControl) FailureReason(operation partitionstyle.StrategyOperation) string {
	switch operation {
	case partitionstyle.StrategyOperationInitialize:
		return "MinReadyInitializeFailed"
	case partitionstyle.StrategyOperationBatching:
		return "MinReadyBatchingFailed"
	case partitionstyle.StrategyOperationFinalize:
		return "MinReadyFinalizeFailed"
	default:
		return ""
	}
}

func (mc *MinReadyControl) RecordZeroReplicaBatching() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatching", "MinReadySeconds strategy has no replicas to upgrade")
	}
}

func (mc *MinReadyControl) RecordBatchAdvanced() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatching", "MinReadySeconds strategy advanced the current batch")
	}
}

func (mc *MinReadyControl) RecordZeroReplicaBatchReady() {
	mc.RecordBatchReady()
}

func (mc *MinReadyControl) RecordBatchReady() {
	if mc.statusWriter == nil {
		klog.V(3).InfoS("MinReadyControl.RecordBatchReady: statusWriter is nil, BindStrategyStatus may not have been called")
		return
	}
	mc.statusWriter.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")
}

func (mc *MinReadyControl) RecordInitialized() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionStrategyInitialized, "MinReadyInitialized", "MinReadySeconds strategy initialized")
	}
}

func (mc *MinReadyControl) RecordFinalized() {
	if mc.statusWriter != nil {
		mc.statusWriter.RecordNormal(v1beta1.RolloutConditionStrategyFinalized, "MinReadyFinalized", "MinReadySeconds strategy finalized")
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
	condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionStrategyBatching)
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
	// Keep the MinReady wrapper after the real controller loads the Deployment;
	// returning rc directly would drop MinReady lifecycle, drift-reconcile, and
	// status-writer behavior from the partition-style control plane.
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
		klog.V(3).InfoS("MinReady maxUnavailable exceeds target, reducing",
			"batch", batchContext.CurrentBatch, "deployment", klog.KObj(mc.object),
			"maxUnavailable", current, "target", target)
		return mc.patchMaxUnavailable(ctx, int(target))
	}

	// Sliding window: keep no more than the user's original maxUnavailable
	// budget worth of updated-but-not-ready pods in flight. As each updated pod
	// becomes ready under the original minReadySeconds, top up the window. When
	// the original maxUnavailable is 0, maxSurge can still create the first new
	// pod; once that pod is ready, maxUnavailable advances one ready pod at a
	// time instead of jumping straight to the batch target.
	step, err := mc.maxUnavailableStep(batchContext.Replicas)
	if err != nil {
		return fmt.Errorf("MinReadyControl.reconcileMaxUnavailable[%d]: %w", batchContext.CurrentBatch, err)
	}
	if step < 0 {
		return fmt.Errorf("MinReadyControl.reconcileMaxUnavailable[%d]: original maxUnavailable resolved to negative %d: %w",
			batchContext.CurrentBatch, step, partitionstyle.ErrMinReadyAnnotationInvalid)
	}
	next := int(batchContext.UpdatedReadyReplicas)
	if step > 0 {
		next += step
	}
	if next <= current {
		return nil
	}
	if int32(next) > target {
		next = int(target)
	}
	return mc.patchMaxUnavailable(ctx, next)
}

// maxUnavailableStep mirrors Kubernetes Deployment fencepost resolution for the
// user's original maxUnavailable; the sliding window uses it as the stride.
func (mc *MinReadyControl) maxUnavailableStep(replicas int32) (int, error) {
	original, err := parseOriginalDeploymentStrategy(mc.object.Annotations)
	if err != nil {
		return 0, err
	}
	step := intstr.FromString(DefaultMaxUnavailable)
	if original.maxUnavailable != nil {
		step = *original.maxUnavailable
	}
	surge := intstr.FromInt(0)
	if mc.object.Spec.Strategy.RollingUpdate != nil && mc.object.Spec.Strategy.RollingUpdate.MaxSurge != nil {
		surge = *mc.object.Spec.Strategy.RollingUpdate.MaxSurge
	}
	resolvedSurge, err := intstr.GetScaledValueFromIntOrPercent(&surge, int(replicas), true)
	if err != nil {
		return 0, err
	}
	resolvedUnavailable, err := intstr.GetScaledValueFromIntOrPercent(&step, int(replicas), false)
	if err != nil {
		return 0, err
	}
	if resolvedSurge == 0 && resolvedUnavailable == 0 {
		return 1, nil
	}
	return resolvedUnavailable, nil
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
	// The Rollout controller restores the same Deployment concurrently while a
	// Rollout deletion finalizes the BatchRelease, so the optimistic-lock patch
	// below can hit a resourceVersion conflict. Refresh the Deployment and retry
	// on conflict so finalization completes instead of getting stuck.
	//
	// E2E AfterEach (and operators) may also delete the Deployment before
	// BatchRelease finishes Finalize. BuildController can still succeed from a
	// cached object, then Get/Patch return NotFound and would otherwise leave
	// the BatchRelease finalizer stuck, blocking namespace deletion.
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := mc.refreshDeployment(ctx); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !mc.object.DeletionTimestamp.IsZero() {
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
			return client.IgnoreNotFound(err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("MinReadyControl.Finalize: %w", err)
	}
	return nil
}

func (mc *MinReadyControl) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	rolloutID := release.Spec.ReleasePlan.RolloutID
	pods, err := mc.ListOwnedPods()
	if err != nil {
		return nil, fmt.Errorf("MinReadyControl.CalculateBatchContext: %w", err)
	}

	currentBatch := release.Status.CanaryStatus.CurrentBatch
	desiredPartition := release.Spec.ReleasePlan.Batches[currentBatch].CanaryReplicas
	desiredUpdatedReplicas, err := minReadyDesiredUpdatedReplicas(desiredPartition, mc.object)
	if err != nil {
		return nil, fmt.Errorf("MinReadyControl.CalculateBatchContext: %w", err)
	}
	updatedReadyReplicas, err := mc.minReadyUpdatedReadyReplicas(release.Status.UpdateRevision, pods)
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
		Pods:                   pods,
	}, nil
}

func (mc *MinReadyControl) ensureInitializeAllowed() error {
	if mc.realController == nil || mc.object == nil {
		return fmt.Errorf("deployment is not loaded")
	}
	if !utilfeature.DefaultFeatureGate.Enabled(feature.MinReadySecondsStrategy) && !hasAnyOriginalAnnotation(mc.object.Annotations) {
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
	if err := validateOriginalAnnotations(deployment); err != nil {
		return err
	}
	if err := validateInflatedDeploymentStrategy(deployment); err != nil {
		if !minreadyutil.HasOriginalAvailabilityChange(deployment) {
			return err
		}
		if err := minreadyutil.ValidateRefreshableDeployment(deployment); err != nil {
			return err
		}
		writeOriginalAvailabilityAnnotations(deployment, writeTarget)
	}
	return nil
}

func validateOriginalAnnotations(deployment *apps.Deployment) error {
	_, err := parseOriginalDeploymentStrategy(deployment.Annotations)
	return err
}

func writeOriginalAnnotations(original, modified *apps.Deployment) {
	minreadyutil.WriteOriginalAnnotations(original, modified)
}

func writeOriginalAvailabilityAnnotations(original, modified *apps.Deployment) {
	minreadyutil.WriteOriginalAvailabilityAnnotations(original, modified)
}

func inflateDeploymentStrategy(deployment *apps.Deployment) {
	minreadyutil.InflateDeploymentStrategy(deployment)
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
	if err := minreadyutil.ValidateInflatedDeploymentStrategy(deployment); err != nil {
		return fmt.Errorf("%w: %v", partitionstyle.ErrMinReadyDriftDetected, err)
	}
	return nil
}

func validateDeploymentStrategyType(deployment *apps.Deployment) error {
	if err := minreadyutil.ValidateDeploymentStrategyType(deployment); err != nil {
		return fmt.Errorf("%w: %v", partitionstyle.ErrMinReadyDriftDetected, err)
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
	original, err := minreadyutil.ParseOriginalDeploymentStrategy(annotations)
	if err != nil {
		return nil, fmt.Errorf("%v: %w", err, partitionstyle.ErrMinReadyAnnotationInvalid)
	}
	return &originalDeploymentStrategy{
		minReadySeconds:         original.MinReadySeconds,
		progressDeadlineSeconds: original.ProgressDeadlineSeconds,
		maxUnavailable:          original.MaxUnavailable,
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

var _ partitionstyle.Interface = (*MinReadyControl)(nil)
var _ partitionstyle.StrategyStatusBinder = (*MinReadyControl)(nil)
var _ partitionstyle.StrategyLifecycle = (*MinReadyControl)(nil)
var _ partitionstyle.StrategyFailureReasoner = (*MinReadyControl)(nil)
var _ partitionstyle.MinReadyDriftReconciler = (*MinReadyControl)(nil)
