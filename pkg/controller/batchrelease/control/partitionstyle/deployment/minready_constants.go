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
)

const (
	AnnotationOriginalMinReadySeconds         = "rollouts.kruise.io/original-min-ready-seconds"
	AnnotationOriginalProgressDeadlineSeconds = "rollouts.kruise.io/original-progress-deadline-seconds"
	AnnotationOriginalMaxUnavailable          = "rollouts.kruise.io/original-max-unavailable"
	AnnotationOriginalMaxSurge                = "rollouts.kruise.io/original-max-surge"

	AnnotationValueKubernetesDefault = "__k8s_default__"

	InflatedMinReadySeconds         int32 = v1beta1.MaxReadySeconds
	InflatedProgressDeadlineSeconds int32 = v1beta1.MaxProgressSeconds
	InflatedMaxSurgeInt             int32 = 1
)

var AllOriginalAnnotations = []string{
	AnnotationOriginalMinReadySeconds,
	AnnotationOriginalProgressDeadlineSeconds,
	AnnotationOriginalMaxUnavailable,
	AnnotationOriginalMaxSurge,
}

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
		return nil, fmt.Errorf("annotation %s malformed int32: %w", key, err)
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
			return nil, fmt.Errorf("annotation %s malformed percent: %w", key, err)
		}
		v := intstr.FromString(raw)
		return &v, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("annotation %s malformed int: %w", key, err)
	}
	v := intstr.FromInt(n)
	return &v, nil
}

func readOriginalAnnotation(annotations map[string]string, key string) (string, error) {
	raw, ok := annotations[key]
	if !ok {
		return "", fmt.Errorf("annotation %s missing", key)
	}
	if raw == "" {
		return "", fmt.Errorf("annotation %s present but empty", key)
	}
	return raw, nil
}

func hasAnyOriginalAnnotation(annotations map[string]string) bool {
	for _, key := range AllOriginalAnnotations {
		if _, ok := annotations[key]; ok {
			return true
		}
	}
	return false
}
