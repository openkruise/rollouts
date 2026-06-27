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

import "errors"

// Sentinel errors used to classify MinReady degraded conditions into stable
// Prometheus metric labels and event reasons. Producers must wrap them with
// %w so that classification relies on errors.Is instead of message text.
var (
	// ErrMinReadyFeatureGateDisabled indicates the MinReadySecondsStrategy
	// feature gate is disabled while a MinReady operation was requested.
	ErrMinReadyFeatureGateDisabled = errors.New("feature gate is disabled")

	// ErrMinReadyAnnotationInvalid covers missing, empty or malformed
	// MinReady original-strategy annotations.
	ErrMinReadyAnnotationInvalid = errors.New("original annotation invalid")

	// ErrMinReadyDriftDetected indicates the inflated Deployment fields were
	// changed externally (GitOps reconcile, manual kubectl, etc.). Its text
	// doubles as the warning event reason.
	ErrMinReadyDriftDetected = errors.New("MinReadyDegradedDriftDetected")
)
