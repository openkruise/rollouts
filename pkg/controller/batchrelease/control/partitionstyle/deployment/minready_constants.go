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

import minreadyutil "github.com/openkruise/rollouts/pkg/util/minready"

const (
	// Aliases kept for readability inside this package; the canonical
	// definitions live in api/v1beta1 so that packages which cannot import
	// this one (e.g. partitionstyle) can still recognize MinReady state.
	AnnotationOriginalMinReadySeconds         = minreadyutil.AnnotationOriginalMinReadySeconds
	AnnotationOriginalProgressDeadlineSeconds = minreadyutil.AnnotationOriginalProgressDeadlineSeconds
	AnnotationOriginalMaxUnavailable          = minreadyutil.AnnotationOriginalMaxUnavailable

	DefaultProgressDeadlineSeconds int32 = minreadyutil.DefaultProgressDeadlineSeconds
	DefaultMaxUnavailable                = minreadyutil.DefaultMaxUnavailable

	InflatedMinReadySeconds         int32 = minreadyutil.InflatedMinReadySeconds
	InflatedProgressDeadlineSeconds int32 = minreadyutil.InflatedProgressDeadlineSeconds
)

var AllOriginalAnnotations = minreadyutil.AllOriginalAnnotations

func hasAnyOriginalAnnotation(annotations map[string]string) bool {
	return minreadyutil.HasOriginalAnnotations(annotations)
}
