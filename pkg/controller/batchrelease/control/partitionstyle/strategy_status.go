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

package partitionstyle

import (
	"context"
	"errors"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	brmetrics "github.com/openkruise/rollouts/pkg/controller/batchrelease/metrics"
	"github.com/openkruise/rollouts/pkg/util"
)

// StrategyStatusBinder injects BatchRelease status/event dependencies into
// strategy-specific controllers before lifecycle methods run.
type StrategyStatusBinder interface {
	BindStrategyStatus(release *v1beta1.BatchRelease, status *v1beta1.BatchReleaseStatus, recorder record.EventRecorder)
}

// StrategyLifecycle records strategy-specific status from control-plane batch
// paths that are not Initialize/UpgradeBatch/Finalize.
type StrategyLifecycle interface {
	RecordZeroReplicaBatching()
	RecordBatchAdvanced()
	RecordZeroReplicaBatchReady()
	RecordBatchReady()
	ObserveBatchWait()
	RecordOperationFailed(reason string, err error)
	RecordInitialized()
	RecordFinalized()
}

type StrategyOperation string

const (
	StrategyOperationInitialize StrategyOperation = "Initialize"
	StrategyOperationBatching   StrategyOperation = "Batching"
	StrategyOperationFinalize   StrategyOperation = "Finalize"
)

// StrategyFailureReasoner lets strategy-specific controllers choose stable
// event/condition reasons without hard-coding one strategy in the control plane.
type StrategyFailureReasoner interface {
	FailureReason(operation StrategyOperation) string
}

// Reporter is the strategy-specific status reporting surface the control plane
// drives through Interface.GetReporter. Strategy controllers that do not report
// strategy status return nil, so the control plane needs no type assertions.
type Reporter interface {
	StrategyLifecycle
	StrategyFailureReasoner
}

// MinReadyDriftReconciler converges inflated maxUnavailable back to the active
// batch target. EnsureBatchPodsReadyAndLabeled calls it so external drift is
// healed even while BatchRelease waits in ReadyBatchState for rollout resume.
type MinReadyDriftReconciler interface {
	ReconcileMaxUnavailableDrift(ctx context.Context, batchContext *batchcontext.BatchContext) error
}

type MinReadyStatusWriter struct {
	release  *v1beta1.BatchRelease
	status   *v1beta1.BatchReleaseStatus
	recorder record.EventRecorder
}

func NewMinReadyStatusWriter(release *v1beta1.BatchRelease, status *v1beta1.BatchReleaseStatus, recorder record.EventRecorder) *MinReadyStatusWriter {
	return &MinReadyStatusWriter{
		release:  release,
		status:   status,
		recorder: recorder,
	}
}

func (w *MinReadyStatusWriter) BatchRelease() *v1beta1.BatchRelease {
	if w == nil {
		return nil
	}
	return w.release
}

func (w *MinReadyStatusWriter) BatchReleaseStatus() *v1beta1.BatchReleaseStatus {
	if w == nil {
		return nil
	}
	return w.status
}

func (w *MinReadyStatusWriter) RecordNormal(condType v1beta1.RolloutConditionType, reason, message string) {
	if w == nil || w.status == nil {
		return
	}
	previousCondition := util.GetBatchReleaseCondition(*w.status, condType)
	condition := util.NewRolloutCondition(condType, v1.ConditionTrue, reason, message)
	updated := util.SetBatchReleaseCondition(w.status, *condition)
	if !updated {
		if reason == "MinReadyFinalized" {
			brmetrics.DeleteMinReadyMetrics(w.release)
		}
		return
	}
	if reason == "MinReadyFinalized" {
		clearMinReadyDegraded(w.status)
		w.status.Message = ""
		brmetrics.DeleteMinReadyMetrics(w.release)
	}
	if reason == "MinReadyBatchReady" {
		observeMinReadyBatchDuration(w.release, previousCondition)
		brmetrics.RecordMinReadyBatch(w.release, brmetrics.BatchResultSuccess)
	}
	if reason == "MinReadyBatchReady" || reason == "MinReadyFinalized" {
		brmetrics.ClearMinReadyStuckSeconds(w.release, brmetrics.StuckReasonBatchReadyTimeout)
	}
	if w.recorder != nil && w.release != nil && shouldRecordMinReadyNormalEvent(reason) {
		w.recorder.Event(w.release, v1.EventTypeNormal, reason, message)
	}
}

func (w *MinReadyStatusWriter) RecordDegraded(reason string, err error) {
	if w == nil || w.status == nil || err == nil {
		return
	}
	message := err.Error()
	classified := classifyMinReadyDegradedReason(reason, err)
	eventReason := classified.event
	previousCondition := util.GetBatchReleaseCondition(*w.status, v1beta1.RolloutConditionStrategyDegraded)
	sameDegradedReason := previousCondition != nil &&
		previousCondition.Status == v1.ConditionTrue &&
		previousCondition.Reason == eventReason
	// Error text can include retry/object details. A recurring degraded reason
	// is not a new transition, even when the human-readable message changes.
	if sameDegradedReason {
		condition := util.NewRolloutCondition(v1beta1.RolloutConditionStrategyDegraded, v1.ConditionTrue, eventReason, message)
		util.SetBatchReleaseCondition(w.status, *condition)
		w.status.Message = message
		brmetrics.ClearMinReadyStuckSeconds(w.release, brmetrics.StuckReasonBatchReadyTimeout)
		return
	}
	condition := util.NewRolloutCondition(v1beta1.RolloutConditionStrategyDegraded, v1.ConditionTrue, eventReason, message)
	updated := util.SetBatchReleaseCondition(w.status, *condition)
	if !updated {
		return
	}
	w.status.Message = message
	brmetrics.ClearMinReadyStuckSeconds(w.release, brmetrics.StuckReasonBatchReadyTimeout)
	brmetrics.RecordMinReadyBatch(w.release, brmetrics.BatchResultDegraded)
	brmetrics.RecordMinReadyDegraded(w.release, classified.metric)
	if w.recorder != nil && w.release != nil {
		w.recorder.Event(w.release, v1.EventTypeWarning, eventReason, message)
	}
}

// observeMinReadyBatchDuration observes the per-batch duration based on
// LastUpdateTime. The Batching condition stays True (only reason/message
// change) across MinReadyBatching→MinReadyBatchReady→MinReadyBatching
// transitions, so LastTransitionTime stays anchored to batch 0 and would
// measure the whole release; LastUpdateTime is refreshed on every
// SetBatchReleaseCondition set, giving the per-batch window.
func observeMinReadyBatchDuration(release *v1beta1.BatchRelease, condition *v1beta1.RolloutCondition) {
	if release == nil || condition == nil || condition.LastUpdateTime.IsZero() {
		return
	}
	duration := time.Since(condition.LastUpdateTime.Time)
	if duration < 0 {
		return
	}
	brmetrics.ObserveMinReadyBatchDuration(release, duration)
}

// ObserveMinReadyBatchWait updates the stuck-seconds metric while a batch waits
// to become ready. Same LastUpdateTime rationale as observeMinReadyBatchDuration.
func ObserveMinReadyBatchWait(release *v1beta1.BatchRelease, condition *v1beta1.RolloutCondition) {
	if release == nil || condition == nil || condition.LastUpdateTime.IsZero() {
		return
	}
	duration := time.Since(condition.LastUpdateTime.Time)
	if duration < 0 {
		return
	}
	brmetrics.SetMinReadyStuckSeconds(release, brmetrics.StuckReasonBatchReadyTimeout, duration.Seconds())
}

func clearMinReadyDegraded(status *v1beta1.BatchReleaseStatus) {
	condition := util.NewRolloutCondition(v1beta1.RolloutConditionStrategyDegraded, v1.ConditionFalse, "MinReadyHealthy", "")
	util.SetBatchReleaseCondition(status, *condition)
}

func shouldRecordMinReadyNormalEvent(reason string) bool {
	return reason != "MinReadyBatching"
}

type minReadyDegradedReason struct {
	metric string
	event  string
}

// classifyMinReadyDegradedReason maps a degraded error onto a stable metric
// label and event reason via errors.Is, so the classification does not depend
// on human-readable error text. Producers wrap the sentinels in minready_errors.go
// with %w; fallback is used as the event reason for unclassified errors.
func classifyMinReadyDegradedReason(fallback string, err error) minReadyDegradedReason {
	switch {
	case errors.Is(err, ErrMinReadyFeatureGateDisabled):
		return minReadyDegradedReason{
			metric: brmetrics.DegradedReasonFeatureGateDisabled,
			event:  "MinReadyFeatureGateDisabled",
		}
	case errors.Is(err, ErrMinReadyAnnotationInvalid):
		return minReadyDegradedReason{
			metric: brmetrics.DegradedReasonMissingAnnotations,
			event:  "MinReadyDegradedMissingAnnotations",
		}
	case errors.Is(err, ErrMinReadyDriftDetected):
		return minReadyDegradedReason{
			metric: brmetrics.DegradedReasonGitOpsDrift,
			event:  "MinReadyDegradedDriftDetected",
		}
	}
	return minReadyDegradedReason{
		metric: brmetrics.DegradedReasonControllerError,
		event:  fallback,
	}
}
