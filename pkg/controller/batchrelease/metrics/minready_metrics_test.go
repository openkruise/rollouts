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

package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/openkruise/rollouts/api/v1beta1"
)

func TestMinReadyMetricsRecorders(t *testing.T) {
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rollout-a",
			Namespace: "default",
		},
	}

	RecordMinReadyBatch(release, BatchResultSuccess)
	ObserveMinReadyBatchDuration(release, 2*time.Second)
	SetMinReadyStuckSeconds(release, StuckReasonBatchReadyTimeout, 3)
	ClearMinReadyStuckSeconds(release, StuckReasonBatchReadyTimeout)
	RecordMinReadyDegraded(release, DegradedReasonControllerError)

	assertCounterPositive(t, minReadyBatchesTotal.WithLabelValues("rollout-a", "default", BatchResultSuccess))
	histogram, ok := minReadyBatchDurationSeconds.WithLabelValues("rollout-a", "default").(prometheus.Metric)
	if !ok {
		t.Fatalf("histogram observer does not implement prometheus.Metric")
	}
	assertHistogramCountPositive(t, histogram)
	assertMetricAbsent(t, "rollout_minready_stuck_seconds", map[string]string{
		"rollout":   "rollout-a",
		"namespace": "default",
		"reason":    StuckReasonBatchReadyTimeout,
	})
	assertCounterPositive(t, minReadyDegradedTotal.WithLabelValues("rollout-a", "default", DegradedReasonControllerError))
}

func TestDeleteMinReadyMetricsDeletesLabelValues(t *testing.T) {
	release := &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rollout-cleanup",
			Namespace: "default",
		},
	}

	RecordMinReadyBatch(release, BatchResultSuccess)
	ObserveMinReadyBatchDuration(release, 2*time.Second)
	SetMinReadyStuckSeconds(release, StuckReasonBatchReadyTimeout, 3)
	RecordMinReadyDegraded(release, DegradedReasonControllerError)

	DeleteMinReadyMetrics(release)

	assertMetricAbsent(t, "rollout_minready_batches_total", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"result":    BatchResultSuccess,
	})
	assertMetricAbsent(t, "rollout_minready_batch_duration_seconds", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
	})
	assertMetricAbsent(t, "rollout_minready_stuck_seconds", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"reason":    StuckReasonBatchReadyTimeout,
	})
	assertMetricAbsent(t, "rollout_minready_degraded_total", map[string]string{
		"rollout":   release.Name,
		"namespace": release.Namespace,
		"reason":    DegradedReasonControllerError,
	})
}

func assertCounterPositive(t *testing.T, metric interface{ Write(*dto.Metric) error }) {
	t.Helper()
	var got dto.Metric
	if err := metric.Write(&got); err != nil {
		t.Fatalf("write metric failed: %v", err)
	}
	if got.Counter == nil || got.Counter.GetValue() <= 0 {
		t.Fatalf("counter = %v, want positive", got.Counter)
	}
}

func assertGaugeValue(t *testing.T, metric interface{ Write(*dto.Metric) error }, want float64) {
	t.Helper()
	var got dto.Metric
	if err := metric.Write(&got); err != nil {
		t.Fatalf("write metric failed: %v", err)
	}
	if got.Gauge == nil || got.Gauge.GetValue() != want {
		t.Fatalf("gauge = %v, want %v", got.Gauge, want)
	}
}

func assertHistogramCountPositive(t *testing.T, metric interface{ Write(*dto.Metric) error }) {
	t.Helper()
	var got dto.Metric
	if err := metric.Write(&got); err != nil {
		t.Fatalf("write metric failed: %v", err)
	}
	if got.Histogram == nil || got.Histogram.GetSampleCount() == 0 {
		t.Fatalf("histogram = %v, want sample count > 0", got.Histogram)
	}
}

func assertMetricAbsent(t *testing.T, name string, labels map[string]string) {
	t.Helper()
	metrics, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics failed: %v", err)
	}
	for _, family := range metrics {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricLabelsMatch(metric, labels) {
				t.Fatalf("metric %s with labels %v still exists", name, labels)
			}
		}
	}
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
