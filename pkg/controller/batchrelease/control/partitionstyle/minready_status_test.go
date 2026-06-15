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
		Type:               v1beta1.RolloutConditionMinReadyBatching,
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

	rc.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")

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
		Type:   v1beta1.RolloutConditionMinReadyDegraded,
		Status: v1.ConditionTrue,
		Reason: "MinReadyDegradedMissingAnnotations",
	})
	rc := &MinReadyStatusWriter{
		release:  release,
		status:   status,
		recorder: record.NewFakeRecorder(2),
	}

	rc.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatching", "MinReadySeconds strategy advanced the current batch")

	degraded := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyDegraded)
	if degraded == nil || degraded.Status != v1.ConditionTrue {
		t.Fatalf("degraded condition = %v, want still true after batching", degraded)
	}
	if status.Message != "annotation missing" {
		t.Fatalf("status.message = %q, want previous degraded message", status.Message)
	}

	rc.RecordNormal(v1beta1.RolloutConditionMinReadyFinalized, "MinReadyFinalized", "MinReadySeconds strategy finalized")

	degraded = util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyDegraded)
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
		Type:               v1beta1.RolloutConditionMinReadyBatching,
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
