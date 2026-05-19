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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/openkruise/rollouts/api/v1beta1"
)

const (
	BatchResultSuccess  = "success"
	BatchResultStuck    = "stuck"
	BatchResultDegraded = "degraded"

	DegradedReasonControllerError     = "controller_error"
	DegradedReasonFeatureGateDisabled = "feature_gate_disabled"
	DegradedReasonGitOpsDrift         = "gitops_drift"
	DegradedReasonMissingAnnotations  = "missing_annotations"
	DegradedReasonPDBIncompatible     = "pdb_incompatible"
	StuckReasonBatchReadyTimeout      = "batch_ready_timeout"
)

var (
	minReadyBatchesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rollout_minready_batches_total",
			Help: "Total number of MinReadySeconds rollout batches by result.",
		},
		[]string{"rollout", "namespace", "result"},
	)
	minReadyBatchDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rollout_minready_batch_duration_seconds",
			Help:    "Duration in seconds from MinReadySeconds batch upgrade to readiness.",
			Buckets: []float64{5, 15, 30, 60, 180, 600, 1800},
		},
		[]string{"rollout", "namespace"},
	)
	minReadyStuckSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rollout_minready_stuck_seconds",
			Help: "Current MinReadySeconds stuck duration in seconds by reason.",
		},
		[]string{"rollout", "namespace", "reason"},
	)
	minReadyDegradedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rollout_minready_degraded_total",
			Help: "Total number of MinReadySeconds degraded transitions by reason.",
		},
		[]string{"rollout", "namespace", "reason"},
	)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		minReadyBatchesTotal,
		minReadyBatchDurationSeconds,
		minReadyStuckSeconds,
		minReadyDegradedTotal,
	)
}

func RecordMinReadyBatch(release *v1beta1.BatchRelease, result string) {
	if release == nil {
		return
	}
	minReadyBatchesTotal.WithLabelValues(release.Name, release.Namespace, result).Inc()
}

func ObserveMinReadyBatchDuration(release *v1beta1.BatchRelease, duration time.Duration) {
	if release == nil {
		return
	}
	minReadyBatchDurationSeconds.WithLabelValues(release.Name, release.Namespace).Observe(duration.Seconds())
}

func SetMinReadyStuckSeconds(release *v1beta1.BatchRelease, reason string, seconds float64) {
	if release == nil {
		return
	}
	minReadyStuckSeconds.WithLabelValues(release.Name, release.Namespace, reason).Set(seconds)
}

func ClearMinReadyStuckSeconds(release *v1beta1.BatchRelease, reason string) {
	if release == nil {
		return
	}
	minReadyStuckSeconds.WithLabelValues(release.Name, release.Namespace, reason).Set(0)
}

func RecordMinReadyDegraded(release *v1beta1.BatchRelease, reason string) {
	if release == nil {
		return
	}
	minReadyDegradedTotal.WithLabelValues(release.Name, release.Namespace, reason).Inc()
}
