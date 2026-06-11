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

import (
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
)

const (
	// Aliases kept for readability inside this package; the canonical
	// definitions live in api/v1beta1 so that packages which cannot import
	// this one (e.g. partitionstyle) can still recognize MinReady state.
	AnnotationOriginalMinReadySeconds         = v1beta1.MinReadyOriginalMinReadySecondsAnnotation
	AnnotationOriginalProgressDeadlineSeconds = v1beta1.MinReadyOriginalProgressDeadlineSecondsAnnotation
	AnnotationOriginalMaxUnavailable          = v1beta1.MinReadyOriginalMaxUnavailableAnnotation
	AnnotationOriginalMaxSurge                = v1beta1.MinReadyOriginalMaxSurgeAnnotation

	AnnotationValueKubernetesDefault = "__k8s_default__"

	InflatedMinReadySeconds         int32 = v1beta1.MaxReadySeconds
	InflatedProgressDeadlineSeconds int32 = v1beta1.MaxProgressSeconds
	InflatedMaxSurgeInt             int32 = 1
)

var AllOriginalAnnotations = v1beta1.MinReadyOriginalAnnotations

func serializeOriginalInt32(value *int32) string {
	if value == nil {
		return AnnotationValueKubernetesDefault
	}
	return strconv.FormatInt(int64(*value), 10)
}

func serializeOriginalIntOrString(value *intstr.IntOrString) string {
	if value == nil {
		return AnnotationValueKubernetesDefault
	}
	if value.Type == intstr.String {
		return value.StrVal
	}
	return strconv.FormatInt(int64(value.IntVal), 10)
}

func parseOriginalInt32(annotations map[string]string, key string) (*int32, error) {
	raw, err := readOriginalAnnotation(annotations, key)
	if err != nil || raw == AnnotationValueKubernetesDefault {
		return nil, err
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("annotation %s malformed int32: %v: %w", key, err, partitionstyle.ErrMinReadyAnnotationInvalid)
	}
	v := int32(n)
	return &v, nil
}

func parseOriginalIntOrString(annotations map[string]string, key string) (*intstr.IntOrString, error) {
	raw, err := readOriginalAnnotation(annotations, key)
	if err != nil || raw == AnnotationValueKubernetesDefault {
		return nil, err
	}
	if strings.HasSuffix(raw, "%") {
		if _, err := strconv.Atoi(strings.TrimSuffix(raw, "%")); err != nil {
			return nil, fmt.Errorf("annotation %s malformed percent: %v: %w", key, err, partitionstyle.ErrMinReadyAnnotationInvalid)
		}
		v := intstr.FromString(raw)
		return &v, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("annotation %s malformed int: %v: %w", key, err, partitionstyle.ErrMinReadyAnnotationInvalid)
	}
	v := intstr.FromInt(n)
	return &v, nil
}

func readOriginalAnnotation(annotations map[string]string, key string) (string, error) {
	raw, ok := annotations[key]
	if !ok {
		return "", fmt.Errorf("annotation %s missing: %w", key, partitionstyle.ErrMinReadyAnnotationInvalid)
	}
	if raw == "" {
		return "", fmt.Errorf("annotation %s present but empty: %w", key, partitionstyle.ErrMinReadyAnnotationInvalid)
	}
	return raw, nil
}

func hasAnyOriginalAnnotation(annotations map[string]string) bool {
	return v1beta1.HasMinReadyOriginalAnnotations(annotations)
}
