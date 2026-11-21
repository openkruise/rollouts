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
	"fmt"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/openkruise/rollouts/api/v1beta1"
	brmetrics "github.com/openkruise/rollouts/pkg/controller/batchrelease/metrics"
	"github.com/openkruise/rollouts/pkg/feature"
	"github.com/openkruise/rollouts/pkg/util"
	utilfeature "github.com/openkruise/rollouts/pkg/util/feature"
)

func TestRecordMinReadyNormalObservesBatchDuration(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "duration-rollout", Namespace: "default"},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: apps.SchemeGroupVersion.String(), Kind: "Deployment", Name: "demo"},
			ReleasePlan: v1beta1.ReleasePlan{RollingStyle: v1beta1.PartitionRollingStyle},
		},
	}
	status := &v1beta1.BatchReleaseStatus{}
	startedAt := metav1.NewTime(time.Now().Add(-3 * time.Second))
	util.SetBatchReleaseCondition(status, v1beta1.RolloutCondition{
		Type:               v1beta1.RolloutConditionStrategyBatching,
		Status:             v1.ConditionTrue,
		Reason:             "MinReadyBatching",
		Message:            "MinReadySeconds strategy advanced the current batch",
		LastTransitionTime: startedAt,
		LastUpdateTime:     startedAt,
	})

	rc := &MinReadyStatusWriter{
		release:  release,
		status:   status,
		recorder: record.NewFakeRecorder(1),
	}

	rc.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")

	histogram := findHistogramMetric(t, "rollout_minready_batch_duration_seconds", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
	})
	if histogram.GetSampleCount() == 0 {
		t.Fatalf("histogram sample count = %d, want > 0", histogram.GetSampleCount())
	}
	if status.Message != "" {
		t.Fatalf("status.message = %q, want empty", status.Message)
	}
}

func TestRecordMinReadyBatchReadyIsIdempotent(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "batch-ready-idempotent", Namespace: "default"},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: apps.SchemeGroupVersion.String(), Kind: "Deployment", Name: "demo"},
			ReleasePlan: v1beta1.ReleasePlan{RollingStyle: v1beta1.PartitionRollingStyle},
		},
	}
	brmetrics.DeleteMinReadyMetrics(release)
	defer brmetrics.DeleteMinReadyMetrics(release)

	status := &v1beta1.BatchReleaseStatus{}
	startedAt := metav1.NewTime(time.Now().Add(-3 * time.Second))
	util.SetBatchReleaseCondition(status, v1beta1.RolloutCondition{
		Type:               v1beta1.RolloutConditionStrategyBatching,
		Status:             v1.ConditionTrue,
		Reason:             "MinReadyBatching",
		Message:            "MinReadySeconds strategy advanced the current batch",
		LastTransitionTime: startedAt,
		LastUpdateTime:     startedAt,
	})
	recorder := record.NewFakeRecorder(4)
	rc := &MinReadyStatusWriter{
		release:  release,
		status:   status,
		recorder: recorder,
	}

	rc.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")
	rc.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")

	if value := findCounterValue(t, "rollout_minready_batches_total", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"result":    brmetrics.BatchResultSuccess,
	}); value != 1 {
		t.Fatalf("success counter = %v, want 1 after duplicate BatchReady", value)
	}
	events := 0
	for {
		select {
		case <-recorder.Events:
			events++
		default:
			if events != 1 {
				t.Fatalf("events = %d, want 1 after duplicate BatchReady", events)
			}
			return
		}
	}
}

func TestRecordMinReadyDegradedIsIdempotent(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "degraded-idempotent", Namespace: "default"},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: apps.SchemeGroupVersion.String(), Kind: "Deployment", Name: "demo"},
			ReleasePlan: v1beta1.ReleasePlan{RollingStyle: v1beta1.PartitionRollingStyle},
		},
	}
	brmetrics.DeleteMinReadyMetrics(release)
	defer brmetrics.DeleteMinReadyMetrics(release)

	status := &v1beta1.BatchReleaseStatus{}
	recorder := record.NewFakeRecorder(4)
	rc := &MinReadyStatusWriter{
		release:  release,
		status:   status,
		recorder: recorder,
	}

	// A persistent error re-entering the control plane should only record the
	// degraded transition once; subsequent calls with the same error must not
	// flood the degraded counter, batch metric, or warning events.
	err := fmt.Errorf("UpgradeBatch[1]: %w", ErrMinReadyDriftDetected)
	rc.RecordDegraded("MinReadyBatchingFailed", err)
	rc.RecordDegraded("MinReadyBatchingFailed", err)

	if value := findCounterValue(t, "rollout_minready_degraded_total", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"reason":    brmetrics.DegradedReasonGitOpsDrift,
	}); value != 1 {
		t.Fatalf("degraded counter = %v, want 1 after duplicate RecordDegraded", value)
	}
	if value := findCounterValue(t, "rollout_minready_batches_total", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"result":    brmetrics.BatchResultDegraded,
	}); value != 1 {
		t.Fatalf("degraded batch counter = %v, want 1 after duplicate RecordDegraded", value)
	}
	events := 0
	for {
		select {
		case <-recorder.Events:
			events++
		default:
			if events != 1 {
				t.Fatalf("events = %d, want 1 after duplicate RecordDegraded", events)
			}
			return
		}
	}
}

func TestRecordMinReadyDegradedSuppressesMessageOnlySideEffects(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "degraded-message-change", Namespace: "default"},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: apps.SchemeGroupVersion.String(), Kind: "Deployment", Name: "demo"},
			ReleasePlan: v1beta1.ReleasePlan{RollingStyle: v1beta1.PartitionRollingStyle},
		},
	}
	brmetrics.DeleteMinReadyMetrics(release)
	defer brmetrics.DeleteMinReadyMetrics(release)

	status := &v1beta1.BatchReleaseStatus{}
	recorder := record.NewFakeRecorder(4)
	rc := &MinReadyStatusWriter{
		release:  release,
		status:   status,
		recorder: recorder,
	}

	rc.RecordDegraded("MinReadyBatchingFailed", errors.New("controller error attempt=1"))
	rc.RecordDegraded("MinReadyBatchingFailed", errors.New("controller error attempt=2"))

	if value := findCounterValue(t, "rollout_minready_degraded_total", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"reason":    brmetrics.DegradedReasonControllerError,
	}); value != 1 {
		t.Fatalf("degraded counter = %v, want 1 after message-only change", value)
	}
	if value := findCounterValue(t, "rollout_minready_batches_total", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"result":    brmetrics.BatchResultDegraded,
	}); value != 1 {
		t.Fatalf("degraded batch counter = %v, want 1 after message-only change", value)
	}
	condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionStrategyDegraded)
	if condition == nil || condition.Message != "controller error attempt=2" {
		t.Fatalf("degraded condition message = %v, want latest message", condition)
	}
	if status.Message != "controller error attempt=2" {
		t.Fatalf("status.message = %q, want latest message", status.Message)
	}
	events := 0
	for {
		select {
		case <-recorder.Events:
			events++
		default:
			if events != 1 {
				t.Fatalf("events = %d, want 1 after message-only change", events)
			}
			return
		}
	}
}

func TestRecordMinReadyDegradedCountsReasonTransition(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "degraded-reason-transition", Namespace: "default"},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: apps.SchemeGroupVersion.String(), Kind: "Deployment", Name: "demo"},
			ReleasePlan: v1beta1.ReleasePlan{RollingStyle: v1beta1.PartitionRollingStyle},
		},
	}
	brmetrics.DeleteMinReadyMetrics(release)
	defer brmetrics.DeleteMinReadyMetrics(release)

	status := &v1beta1.BatchReleaseStatus{}
	recorder := record.NewFakeRecorder(4)
	rc := &MinReadyStatusWriter{
		release:  release,
		status:   status,
		recorder: recorder,
	}

	rc.RecordDegraded("MinReadyBatchingFailed", errors.New("controller error during batching"))
	rc.RecordDegraded("MinReadyFinalizeFailed", errors.New("controller error during finalize"))

	if value := findCounterValue(t, "rollout_minready_degraded_total", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"reason":    brmetrics.DegradedReasonControllerError,
	}); value != 2 {
		t.Fatalf("degraded counter = %v, want 2 after reason transition", value)
	}
	if value := findCounterValue(t, "rollout_minready_batches_total", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"result":    brmetrics.BatchResultDegraded,
	}); value != 2 {
		t.Fatalf("degraded batch counter = %v, want 2 after reason transition", value)
	}
	condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionStrategyDegraded)
	if condition == nil || condition.Reason != "MinReadyFinalizeFailed" {
		t.Fatalf("degraded condition = %#v, want finalize reason", condition)
	}
	events := 0
	for {
		select {
		case <-recorder.Events:
			events++
		default:
			if events != 2 {
				t.Fatalf("events = %d, want 2 after reason transition", events)
			}
			return
		}
	}
}

func TestRecordMinReadyNormalKeepsDegradedUntilFinalize(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "degraded-rollout", Namespace: "default"},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: apps.SchemeGroupVersion.String(), Kind: "Deployment", Name: "demo"},
			ReleasePlan: v1beta1.ReleasePlan{RollingStyle: v1beta1.PartitionRollingStyle},
		},
	}
	status := &v1beta1.BatchReleaseStatus{Message: "annotation missing"}
	util.SetBatchReleaseCondition(status, v1beta1.RolloutCondition{
		Type:   v1beta1.RolloutConditionStrategyDegraded,
		Status: v1.ConditionTrue,
		Reason: "MinReadyDegradedMissingAnnotations",
	})
	rc := &MinReadyStatusWriter{
		release:  release,
		status:   status,
		recorder: record.NewFakeRecorder(2),
	}

	rc.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatching", "MinReadySeconds strategy advanced the current batch")
	select {
	case event := <-rc.recorder.(*record.FakeRecorder).Events:
		t.Fatalf("unexpected MinReadyBatching event: %s", event)
	default:
	}

	degraded := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionStrategyDegraded)
	if degraded == nil || degraded.Status != v1.ConditionTrue {
		t.Fatalf("degraded condition = %v, want still true after batching", degraded)
	}
	if status.Message != "annotation missing" {
		t.Fatalf("status.message = %q, want previous degraded message", status.Message)
	}

	rc.RecordNormal(v1beta1.RolloutConditionStrategyFinalized, "MinReadyFinalized", "MinReadySeconds strategy finalized")

	degraded = util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionStrategyDegraded)
	if degraded == nil || degraded.Status != v1.ConditionFalse {
		t.Fatalf("degraded condition = %v, want false after finalize", degraded)
	}
	if status.Message != "" {
		t.Fatalf("status.message = %q, want empty after finalize", status.Message)
	}
}

func TestObserveMinReadyBatchWaitSetsStuckGauge(t *testing.T) {
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "stuck-rollout", Namespace: "default"},
	}
	startedAt := metav1.NewTime(time.Now().Add(-4 * time.Second))
	condition := &v1beta1.RolloutCondition{
		Type:               v1beta1.RolloutConditionStrategyBatching,
		Status:             v1.ConditionTrue,
		Reason:             "MinReadyBatching",
		Message:            "MinReadySeconds strategy advanced the current batch",
		LastTransitionTime: startedAt,
		LastUpdateTime:     startedAt,
	}

	ObserveMinReadyBatchWait(release, condition)

	gauge := findGaugeMetric(t, "rollout_minready_stuck_seconds", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"reason":    "batch_ready_timeout",
	})
	if gauge.GetValue() <= 0 {
		t.Fatalf("gauge value = %v, want > 0", gauge.GetValue())
	}
}

// TestRecordMinReadyNormalMultiBatchResetsBatchDuration drives the per-batch
// window semantics: the Batching condition stays True across
// MinReadyBatching→MinReadyBatchReady→MinReadyBatching transitions, so
// LastTransitionTime stays anchored to batch 0. Both durations must be
// computed from LastUpdateTime instead, so the second batch observes ~0s
// rather than accumulating the whole release time.
func TestRecordMinReadyNormalMultiBatchResetsBatchDuration(t *testing.T) {
	_ = utilfeature.DefaultMutableFeatureGate.Set(string(feature.MinReadySecondsStrategy) + "=true")
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-batch-duration", Namespace: "default"},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: apps.SchemeGroupVersion.String(), Kind: "Deployment", Name: "demo"},
			ReleasePlan: v1beta1.ReleasePlan{RollingStyle: v1beta1.PartitionRollingStyle},
		},
	}
	brmetrics.DeleteMinReadyMetrics(release)
	defer brmetrics.DeleteMinReadyMetrics(release)

	status := &v1beta1.BatchReleaseStatus{}
	batch0Started := metav1.NewTime(time.Now().Add(-5 * time.Second))
	util.SetBatchReleaseCondition(status, v1beta1.RolloutCondition{
		Type:               v1beta1.RolloutConditionStrategyBatching,
		Status:             v1.ConditionTrue,
		Reason:             "MinReadyBatching",
		Message:            "MinReadySeconds strategy advanced the current batch",
		LastTransitionTime: batch0Started,
		LastUpdateTime:     batch0Started,
	})
	rc := &MinReadyStatusWriter{
		release:  release,
		status:   status,
		recorder: record.NewFakeRecorder(4),
	}

	// batch 0 ready: observes ~5s since batch0Started.
	rc.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")
	// batch 1 starts: reason changes back to MinReadyBatching, so the
	// condition is re-set and LastUpdateTime refreshes to now.
	rc.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatching", "MinReadySeconds strategy advanced the current batch")
	// batch 1 ready: must observe ~0s since the refresh, not accumulate.
	rc.RecordNormal(v1beta1.RolloutConditionStrategyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")

	histogram := findHistogramMetric(t, "rollout_minready_batch_duration_seconds", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
	})
	if histogram.GetSampleCount() != 2 {
		t.Fatalf("histogram sample count = %d, want 2", histogram.GetSampleCount())
	}
	// With LastTransitionTime anchoring, the sum would be ~10s (both batches
	// measured from batch 0); with LastUpdateTime it is ~5s. Tolerate
	// scheduling noise on the second (near-zero) sample.
	if sum := histogram.GetSampleSum(); sum >= 8 {
		t.Fatalf("histogram sample sum = %v, want < 8 (per-batch duration must reset)", sum)
	}
}

// TestObserveMinReadyBatchWaitMultiBatchResetsGauge drives the same per-batch
// reset for the stuck-seconds gauge: after the condition is refreshed for the
// next batch, the gauge must reflect the current batch wait, not accumulate
// across batches.
func TestObserveMinReadyBatchWaitMultiBatchResetsGauge(t *testing.T) {
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-batch-stuck", Namespace: "default"},
	}
	brmetrics.DeleteMinReadyMetrics(release)
	defer brmetrics.DeleteMinReadyMetrics(release)

	condition := &v1beta1.RolloutCondition{
		Type:               v1beta1.RolloutConditionStrategyBatching,
		Status:             v1.ConditionTrue,
		Reason:             "MinReadyBatching",
		Message:            "MinReadySeconds strategy advanced the current batch",
		LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Second)),
		LastUpdateTime:     metav1.NewTime(time.Now().Add(-4 * time.Second)),
	}
	ObserveMinReadyBatchWait(release, condition)

	// Simulate the next batch: SetBatchReleaseCondition refreshes
	// LastUpdateTime while LastTransitionTime stays anchored to batch 0.
	condition.LastUpdateTime = metav1.Now()
	ObserveMinReadyBatchWait(release, condition)

	gauge := findGaugeMetric(t, "rollout_minready_stuck_seconds", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"reason":    "batch_ready_timeout",
	})
	if gauge.GetValue() >= 1 {
		t.Fatalf("gauge value = %v, want < 1 (stuck seconds must reset per batch, not accumulate)", gauge.GetValue())
	}
}

func TestClassifyMinReadyDegradedReason(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		metric string
		event  string
	}{
		{
			name:   "drift",
			err:    fmt.Errorf("MinReadyControl.UpgradeBatch[1]: %w: maxUnavailable=3 exceeds target=2", ErrMinReadyDriftDetected),
			metric: brmetrics.DegradedReasonGitOpsDrift,
			event:  "MinReadyDegradedDriftDetected",
		},
		{
			name:   "feature gate disabled",
			err:    fmt.Errorf("MinReadyControl.Initialize: %w", ErrMinReadyFeatureGateDisabled),
			metric: brmetrics.DegradedReasonFeatureGateDisabled,
			event:  "MinReadyFeatureGateDisabled",
		},
		{
			name:   "annotation invalid",
			err:    fmt.Errorf("annotation foo missing: %w", ErrMinReadyAnnotationInvalid),
			metric: brmetrics.DegradedReasonMissingAnnotations,
			event:  "MinReadyDegradedMissingAnnotations",
		},
		{
			name:   "unclassified falls back",
			err:    errors.New("some controller error"),
			metric: brmetrics.DegradedReasonControllerError,
			event:  "MinReadyBatchingFailed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyMinReadyDegradedReason("MinReadyBatchingFailed", tc.err)
			if got.metric != tc.metric {
				t.Fatalf("metric reason = %q, want %q", got.metric, tc.metric)
			}
			if got.event != tc.event {
				t.Fatalf("event reason = %q, want %q", got.event, tc.event)
			}
		})
	}
}

func findHistogramMetric(t *testing.T, name string, labels map[string]string) *dto.Histogram {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics failed: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricLabelsMatch(metric, labels) {
				return metric.GetHistogram()
			}
		}
	}
	t.Fatalf("histogram %s with labels %v not found", name, labels)
	return nil
}

func findCounterValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics failed: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricLabelsMatch(metric, labels) {
				if metric.GetCounter() == nil {
					t.Fatalf("metric %s with labels %v is not a counter", name, labels)
				}
				return metric.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("counter %s with labels %v not found", name, labels)
	return 0
}

func findGaugeMetric(t *testing.T, name string, labels map[string]string) *dto.Gauge {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics failed: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricLabelsMatch(metric, labels) {
				return metric.GetGauge()
			}
		}
	}
	t.Fatalf("gauge %s with labels %v not found", name, labels)
	return nil
}

func metricLabelsMatch(metric *dto.Metric, labels map[string]string) bool {
	for key, want := range labels {
		matched := false
		for _, pair := range metric.GetLabel() {
			if pair.GetName() == key && pair.GetValue() == want {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
