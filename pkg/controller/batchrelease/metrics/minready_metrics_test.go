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
	RecordMinReadyDegraded(release, DegradedReasonPDBIncompatible)

	assertCounterPositive(t, minReadyBatchesTotal.WithLabelValues("rollout-a", "default", BatchResultSuccess))
	histogram, ok := minReadyBatchDurationSeconds.WithLabelValues("rollout-a", "default").(prometheus.Metric)
	if !ok {
		t.Fatalf("histogram observer does not implement prometheus.Metric")
	}
	assertHistogramCountPositive(t, histogram)
	assertGaugeValue(t, minReadyStuckSeconds.WithLabelValues("rollout-a", "default", StuckReasonBatchReadyTimeout), 0)
	assertCounterPositive(t, minReadyDegradedTotal.WithLabelValues("rollout-a", "default", DegradedReasonPDBIncompatible))
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
