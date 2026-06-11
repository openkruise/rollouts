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
	"errors"
	"reflect"
	"time"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"

	"github.com/openkruise/rollouts/api/v1beta1"
	brmetrics "github.com/openkruise/rollouts/pkg/controller/batchrelease/metrics"
	"github.com/openkruise/rollouts/pkg/feature"
	"github.com/openkruise/rollouts/pkg/util"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
)

func (rc *realBatchControlPlane) isMinReadyRelease() bool {
	if rc.release == nil {
		return false
	}
	targetRef := rc.release.Spec.WorkloadRef
	isDeploymentPartition := targetRef.APIVersion == apps.SchemeGroupVersion.String() &&
		targetRef.Kind == reflect.TypeOf(apps.Deployment{}).Name() &&
		rc.release.Spec.ReleasePlan.RollingStyle == v1beta1.PartitionRollingStyle
	if !isDeploymentPartition {
		return false
	}
	if utilfeature.DefaultFeatureGate.Enabled(feature.MinReadySecondsStrategy) {
		return true
	}
	// Gate disabled mid-rollout: a Deployment still carrying MinReady original
	// annotations is under MinReady control until finalized. Keep recording its
	// status so degraded conditions are not silently suppressed. Falls back to
	// false before the controller is built (no workload info yet).
	if info := rc.GetWorkloadInfo(); info != nil {
		return v1beta1.HasMinReadyOriginalAnnotations(info.Annotations)
	}
	return false
}

func (rc *realBatchControlPlane) recordMinReadyNormal(condType v1beta1.RolloutConditionType, reason, message string) {
	if !rc.isMinReadyRelease() {
		return
	}
	previousCondition := util.GetBatchReleaseCondition(*rc.newStatus, condType)
	condition := util.NewRolloutCondition(condType, v1.ConditionTrue, reason, message)
	util.SetBatchReleaseCondition(rc.newStatus, *condition)
	if reason == "MinReadyFinalized" {
		clearMinReadyDegraded(rc.newStatus)
		rc.newStatus.Message = ""
	}
	if reason == "MinReadyBatchReady" {
		observeMinReadyBatchDuration(rc.release, previousCondition)
		brmetrics.RecordMinReadyBatch(rc.release, brmetrics.BatchResultSuccess)
	}
	if reason == "MinReadyBatchReady" || reason == "MinReadyFinalized" {
		brmetrics.ClearMinReadyStuckSeconds(rc.release, brmetrics.StuckReasonBatchReadyTimeout)
	}
	rc.Event(rc.release, v1.EventTypeNormal, reason, message)
}

func observeMinReadyBatchDuration(release *v1beta1.BatchRelease, condition *v1beta1.RolloutCondition) {
	if release == nil || condition == nil || condition.LastTransitionTime.IsZero() {
		return
	}
	duration := time.Since(condition.LastTransitionTime.Time)
	if duration < 0 {
		return
	}
	brmetrics.ObserveMinReadyBatchDuration(release, duration)
}

func (rc *realBatchControlPlane) recordMinReadyDegraded(reason string, err error) {
	if !rc.isMinReadyRelease() || err == nil {
		return
	}
	message := err.Error()
	classified := classifyMinReadyDegradedReason(reason, err)
	eventReason := classified.event
	condition := util.NewRolloutCondition(v1beta1.RolloutConditionMinReadyDegraded, v1.ConditionTrue, eventReason, message)
	util.SetBatchReleaseCondition(rc.newStatus, *condition)
	rc.newStatus.Message = message
	degradedReason := classified.metric
	brmetrics.ClearMinReadyStuckSeconds(rc.release, brmetrics.StuckReasonBatchReadyTimeout)
	brmetrics.RecordMinReadyBatch(rc.release, brmetrics.BatchResultDegraded)
	brmetrics.RecordMinReadyDegraded(rc.release, degradedReason)
	rc.Event(rc.release, v1.EventTypeWarning, eventReason, message)
}

func observeMinReadyBatchWait(release *v1beta1.BatchRelease, condition *v1beta1.RolloutCondition) {
	if release == nil || condition == nil || condition.LastTransitionTime.IsZero() {
		return
	}
	duration := time.Since(condition.LastTransitionTime.Time)
	if duration < 0 {
		return
	}
	brmetrics.SetMinReadyStuckSeconds(release, brmetrics.StuckReasonBatchReadyTimeout, duration.Seconds())
}

func clearMinReadyDegraded(status *v1beta1.BatchReleaseStatus) {
	condition := util.NewRolloutCondition(v1beta1.RolloutConditionMinReadyDegraded, v1.ConditionFalse, "MinReadyHealthy", "")
	util.SetBatchReleaseCondition(status, *condition)
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
