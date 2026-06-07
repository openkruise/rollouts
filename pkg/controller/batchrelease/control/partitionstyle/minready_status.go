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
	"reflect"
	"strings"
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
	if rc.release == nil || !utilfeature.DefaultFeatureGate.Enabled(feature.MinReadySecondsStrategy) {
		return false
	}
	targetRef := rc.release.Spec.WorkloadRef
	return targetRef.APIVersion == apps.SchemeGroupVersion.String() &&
		targetRef.Kind == reflect.TypeOf(apps.Deployment{}).Name() &&
		rc.release.Spec.ReleasePlan.RollingStyle == v1beta1.PartitionRollingStyle
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
	eventReason := minReadyDegradedEventReason(reason, message)
	condition := util.NewRolloutCondition(v1beta1.RolloutConditionMinReadyDegraded, v1.ConditionTrue, eventReason, message)
	util.SetBatchReleaseCondition(rc.newStatus, *condition)
	rc.newStatus.Message = message
	degradedReason := minReadyDegradedMetricReason(message)
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

func minReadyDegradedMetricReason(message string) string {
	return classifyMinReadyDegradedReason("", message).metric
}

func minReadyDegradedEventReason(fallback, message string) string {
	return classifyMinReadyDegradedReason(fallback, message).event
}

func classifyMinReadyDegradedReason(fallback, message string) minReadyDegradedReason {
	eventReason := fallback
	metricReason := brmetrics.DegradedReasonControllerError
	switch {
	case strings.Contains(message, "feature gate is disabled"):
		metricReason = brmetrics.DegradedReasonFeatureGateDisabled
		eventReason = "MinReadyFeatureGateDisabled"
	case strings.Contains(message, "annotation ") && strings.Contains(message, "missing"):
		metricReason = brmetrics.DegradedReasonMissingAnnotations
		eventReason = "MinReadyDegradedMissingAnnotations"
	case strings.Contains(message, "annotation ") && strings.Contains(message, "empty"):
		metricReason = brmetrics.DegradedReasonMissingAnnotations
		eventReason = "MinReadyDegradedMissingAnnotations"
	case strings.Contains(message, "annotation ") && strings.Contains(message, "malformed"):
		metricReason = brmetrics.DegradedReasonMissingAnnotations
		eventReason = "MinReadyDegradedMissingAnnotations"
	case strings.Contains(message, "MinReadyDegradedDriftDetected"):
		metricReason = brmetrics.DegradedReasonGitOpsDrift
		eventReason = "MinReadyDegradedDriftDetected"
	}
	return minReadyDegradedReason{
		metric: metricReason,
		event:  eventReason,
	}
}
