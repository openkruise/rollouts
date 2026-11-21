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

package minready

import (
	"reflect"
	"strings"
	"testing"

	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
)

func TestSerializeOriginalInt32(t *testing.T) {
	cases := []struct {
		name         string
		value        *int32
		defaultValue int32
		want         string
	}{
		{
			name:         "nil uses default",
			value:        nil,
			defaultValue: 600,
			want:         "600",
		},
		{
			name:         "non-nil value",
			value:        pointer.Int32(7),
			defaultValue: 600,
			want:         "7",
		},
		{
			name:         "zero value",
			value:        pointer.Int32(0),
			defaultValue: 600,
			want:         "0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SerializeOriginalInt32(tc.value, tc.defaultValue); got != tc.want {
				t.Fatalf("SerializeOriginalInt32() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSerializeOriginalIntOrString(t *testing.T) {
	percent := intstr.FromString("25%")
	count := intstr.FromInt(1)
	cases := []struct {
		name         string
		value        *intstr.IntOrString
		defaultValue string
		want         string
	}{
		{
			name:         "nil uses default",
			value:        nil,
			defaultValue: DefaultMaxUnavailable,
			want:         "25%",
		},
		{
			name:         "percent value",
			value:        &percent,
			defaultValue: DefaultMaxUnavailable,
			want:         "25%",
		},
		{
			name:         "int value",
			value:        &count,
			defaultValue: DefaultMaxUnavailable,
			want:         "1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SerializeOriginalIntOrString(tc.value, tc.defaultValue); got != tc.want {
				t.Fatalf("SerializeOriginalIntOrString() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseOriginalInt32(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    *int32
		wantErr bool
	}{
		{name: "valid int", raw: "7", want: pointer.Int32(7)},
		{name: "zero", raw: "0", want: pointer.Int32(0)},
		{name: "invalid", raw: "abc", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOriginalInt32(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseOriginalInt32(%q) error = nil, want error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOriginalInt32(%q) unexpected error: %v", tc.raw, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseOriginalInt32(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseOriginalIntOrString(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    intstr.IntOrString
		wantErr bool
	}{
		{name: "valid int", raw: "1", want: intstr.FromInt(1)},
		{name: "valid percent", raw: "25%", want: intstr.FromString("25%")},
		{name: "invalid percent", raw: "abc%", wantErr: true},
		{name: "invalid string", raw: "not-a-percent", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOriginalIntOrString(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseOriginalIntOrString(%q) error = nil, want error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOriginalIntOrString(%q) unexpected error: %v", tc.raw, err)
			}
			if got == nil || *got != tc.want {
				t.Fatalf("ParseOriginalIntOrString(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseOriginalDeploymentStrategyAndValidate(t *testing.T) {
	valid := map[string]string{
		AnnotationOriginalMinReadySeconds:         "7",
		AnnotationOriginalProgressDeadlineSeconds: "60",
		AnnotationOriginalMaxUnavailable:          "25%",
	}
	cases := []struct {
		name        string
		annotations map[string]string
		wantErr     string
	}{
		{name: "happy path", annotations: valid},
		{
			name:        "missing annotation",
			annotations: map[string]string{AnnotationOriginalMinReadySeconds: "7"},
			wantErr:     "missing",
		},
		{
			name: "empty annotation",
			annotations: map[string]string{
				AnnotationOriginalMinReadySeconds:         "",
				AnnotationOriginalProgressDeadlineSeconds: "60",
				AnnotationOriginalMaxUnavailable:          "25%",
			},
			wantErr: "present but empty",
		},
		{
			name: "malformed int32",
			annotations: map[string]string{
				AnnotationOriginalMinReadySeconds:         "abc",
				AnnotationOriginalProgressDeadlineSeconds: "60",
				AnnotationOriginalMaxUnavailable:          "25%",
			},
			wantErr: "malformed int32",
		},
		{
			name: "malformed IntOrString",
			annotations: map[string]string{
				AnnotationOriginalMinReadySeconds:         "7",
				AnnotationOriginalProgressDeadlineSeconds: "60",
				AnnotationOriginalMaxUnavailable:          "abc%",
			},
			wantErr: "malformed IntOrString",
		},
		{name: "nil annotations", annotations: nil, wantErr: "missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOriginalDeploymentStrategy(tc.annotations)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseOriginalDeploymentStrategy() error = %v, want containing %q", err, tc.wantErr)
				}
				if validateErr := ValidateOriginalAnnotations(tc.annotations); validateErr == nil || !strings.Contains(validateErr.Error(), tc.wantErr) {
					t.Fatalf("ValidateOriginalAnnotations() error = %v, want containing %q", validateErr, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOriginalDeploymentStrategy() unexpected error: %v", err)
			}
			if got.MinReadySeconds == nil || *got.MinReadySeconds != 7 {
				t.Fatalf("MinReadySeconds = %v, want 7", got.MinReadySeconds)
			}
			if got.ProgressDeadlineSeconds == nil || *got.ProgressDeadlineSeconds != 60 {
				t.Fatalf("ProgressDeadlineSeconds = %v, want 60", got.ProgressDeadlineSeconds)
			}
			if got.MaxUnavailable == nil || got.MaxUnavailable.String() != "25%" {
				t.Fatalf("MaxUnavailable = %v, want 25%%", got.MaxUnavailable)
			}
			if err := ValidateOriginalAnnotations(tc.annotations); err != nil {
				t.Fatalf("ValidateOriginalAnnotations() unexpected error: %v", err)
			}
		})
	}
}

func TestWriteOriginalAnnotations(t *testing.T) {
	maxUnavailable := intstr.FromString("10%")
	original := &apps.Deployment{
		Spec: apps.DeploymentSpec{
			MinReadySeconds:         7,
			ProgressDeadlineSeconds: pointer.Int32(60),
			Strategy: apps.DeploymentStrategy{
				Type: apps.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
				},
			},
		},
	}
	modified := &apps.Deployment{}
	WriteOriginalAnnotations(original, modified)

	want := map[string]string{
		AnnotationOriginalMinReadySeconds:         "7",
		AnnotationOriginalProgressDeadlineSeconds: "60",
		AnnotationOriginalMaxUnavailable:          "10%",
	}
	if !reflect.DeepEqual(modified.Annotations, want) {
		t.Fatalf("WriteOriginalAnnotations() annotations = %v, want %v", modified.Annotations, want)
	}
}

func TestWriteOriginalAvailabilityAnnotationsDefaults(t *testing.T) {
	original := &apps.Deployment{
		Spec: apps.DeploymentSpec{
			MinReadySeconds: 0,
			// ProgressDeadlineSeconds nil → default 600
		},
	}
	modified := &apps.Deployment{}
	WriteOriginalAvailabilityAnnotations(original, modified)

	if got := modified.Annotations[AnnotationOriginalMinReadySeconds]; got != "0" {
		t.Fatalf("minReadySeconds annotation = %q, want 0", got)
	}
	if got := modified.Annotations[AnnotationOriginalProgressDeadlineSeconds]; got != "600" {
		t.Fatalf("progressDeadlineSeconds annotation = %q, want 600", got)
	}
}

func TestWriteOriginalMaxUnavailableAnnotationDefaults(t *testing.T) {
	// RollingUpdate nil → OriginalMaxUnavailable nil → default "25%"
	original := &apps.Deployment{}
	modified := &apps.Deployment{}
	WriteOriginalMaxUnavailableAnnotation(original, modified)
	if got := modified.Annotations[AnnotationOriginalMaxUnavailable]; got != DefaultMaxUnavailable {
		t.Fatalf("maxUnavailable annotation = %q, want %q", got, DefaultMaxUnavailable)
	}
}

func TestOriginalMaxUnavailable(t *testing.T) {
	maxUnavailable := intstr.FromInt(2)
	cases := []struct {
		name       string
		deployment *apps.Deployment
		wantNil    bool
		want       intstr.IntOrString
	}{
		{
			name:       "nil rollingUpdate",
			deployment: &apps.Deployment{},
			wantNil:    true,
		},
		{
			name: "with maxUnavailable",
			deployment: &apps.Deployment{
				Spec: apps.DeploymentSpec{
					Strategy: apps.DeploymentStrategy{
						RollingUpdate: &apps.RollingUpdateDeployment{
							MaxUnavailable: &maxUnavailable,
						},
					},
				},
			},
			want: intstr.FromInt(2),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := OriginalMaxUnavailable(tc.deployment)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("OriginalMaxUnavailable() = %v, want nil", got)
				}
				return
			}
			if got == nil || *got != tc.want {
				t.Fatalf("OriginalMaxUnavailable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInflateAndValidateInflatedDeploymentStrategy(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		deployment := &apps.Deployment{
			Spec: apps.DeploymentSpec{
				Paused:          true,
				MinReadySeconds: 7,
				Strategy: apps.DeploymentStrategy{
					Type: apps.RollingUpdateDeploymentStrategyType,
				},
			},
		}
		InflateDeploymentStrategy(deployment)
		if err := ValidateInflatedDeploymentStrategy(deployment); err != nil {
			t.Fatalf("ValidateInflatedDeploymentStrategy() unexpected error: %v", err)
		}
		if deployment.Spec.Paused {
			t.Fatalf("Paused = true, want false")
		}
		if deployment.Spec.MinReadySeconds != InflatedMinReadySeconds {
			t.Fatalf("MinReadySeconds = %d, want %d", deployment.Spec.MinReadySeconds, InflatedMinReadySeconds)
		}
		if deployment.Spec.ProgressDeadlineSeconds == nil || *deployment.Spec.ProgressDeadlineSeconds != InflatedProgressDeadlineSeconds {
			t.Fatalf("ProgressDeadlineSeconds = %v, want %d", deployment.Spec.ProgressDeadlineSeconds, InflatedProgressDeadlineSeconds)
		}
		if deployment.Spec.Strategy.RollingUpdate == nil || deployment.Spec.Strategy.RollingUpdate.MaxUnavailable == nil {
			t.Fatalf("RollingUpdate.MaxUnavailable is nil")
		}
		if deployment.Spec.Strategy.RollingUpdate.MaxUnavailable.IntValue() != 0 {
			t.Fatalf("MaxUnavailable = %v, want 0", deployment.Spec.Strategy.RollingUpdate.MaxUnavailable)
		}
	})

	failureCases := []struct {
		name       string
		deployment *apps.Deployment
		wantErr    string
	}{
		{
			name: "paused",
			deployment: inflatedDeployment(func(d *apps.Deployment) {
				d.Spec.Paused = true
			}),
			wantErr: "deployment is paused",
		},
		{
			name: "wrong minReadySeconds",
			deployment: inflatedDeployment(func(d *apps.Deployment) {
				d.Spec.MinReadySeconds = 10
			}),
			wantErr: "minReadySeconds=",
		},
		{
			name: "wrong progressDeadlineSeconds",
			deployment: inflatedDeployment(func(d *apps.Deployment) {
				d.Spec.ProgressDeadlineSeconds = pointer.Int32(600)
			}),
			wantErr: "progressDeadlineSeconds=",
		},
		{
			name: "nil progressDeadlineSeconds",
			deployment: inflatedDeployment(func(d *apps.Deployment) {
				d.Spec.ProgressDeadlineSeconds = nil
			}),
			wantErr: "progressDeadlineSeconds=",
		},
		{
			name: "nil rollingUpdate",
			deployment: inflatedDeployment(func(d *apps.Deployment) {
				d.Spec.Strategy.RollingUpdate = nil
			}),
			wantErr: "rollingUpdate is nil",
		},
		{
			name: "wrong strategy type",
			deployment: inflatedDeployment(func(d *apps.Deployment) {
				d.Spec.Strategy.Type = apps.RecreateDeploymentStrategyType
			}),
			wantErr: "is not RollingUpdate",
		},
	}
	for _, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInflatedDeploymentStrategy(tc.deployment)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateInflatedDeploymentStrategy() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestHasOriginalAvailabilityChange(t *testing.T) {
	cases := []struct {
		name       string
		deployment *apps.Deployment
		want       bool
	}{
		{
			name:       "inflated fields",
			deployment: inflatedDeployment(nil),
			want:       false,
		},
		{
			name: "different minReadySeconds",
			deployment: inflatedDeployment(func(d *apps.Deployment) {
				d.Spec.MinReadySeconds = 7
			}),
			want: true,
		},
		{
			name: "nil progressDeadlineSeconds",
			deployment: inflatedDeployment(func(d *apps.Deployment) {
				d.Spec.ProgressDeadlineSeconds = nil
			}),
			want: true,
		},
		{
			name: "different progressDeadlineSeconds",
			deployment: inflatedDeployment(func(d *apps.Deployment) {
				d.Spec.ProgressDeadlineSeconds = pointer.Int32(600)
			}),
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasOriginalAvailabilityChange(tc.deployment); got != tc.want {
				t.Fatalf("HasOriginalAvailabilityChange() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasOriginalAnnotations(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		want        bool
	}{
		{name: "nil", annotations: nil, want: false},
		{name: "empty", annotations: map[string]string{}, want: false},
		{
			name:        "any original annotation present",
			annotations: map[string]string{AnnotationOriginalMinReadySeconds: "7"},
			want:        true,
		},
		{
			name: "unrelated annotation",
			annotations: map[string]string{
				"rollouts.kruise.io/other": "true",
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasOriginalAnnotations(tc.annotations); got != tc.want {
				t.Fatalf("HasOriginalAnnotations() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateRefreshableDeployment(t *testing.T) {
	cases := []struct {
		name       string
		deployment *apps.Deployment
		wantErr    string
	}{
		{
			name: "happy path",
			deployment: &apps.Deployment{
				Spec: apps.DeploymentSpec{
					Strategy: apps.DeploymentStrategy{
						RollingUpdate: &apps.RollingUpdateDeployment{},
					},
				},
			},
		},
		{
			name: "paused",
			deployment: &apps.Deployment{
				Spec: apps.DeploymentSpec{
					Paused: true,
					Strategy: apps.DeploymentStrategy{
						RollingUpdate: &apps.RollingUpdateDeployment{},
					},
				},
			},
			wantErr: "deployment is paused",
		},
		{
			name:       "nil rollingUpdate",
			deployment: &apps.Deployment{},
			wantErr:    "rollingUpdate is nil",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRefreshableDeployment(tc.deployment)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ValidateRefreshableDeployment() error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateRefreshableDeployment() unexpected error: %v", err)
			}
		})
	}
}

func TestValidateDeploymentStrategyType(t *testing.T) {
	cases := []struct {
		name       string
		deployment *apps.Deployment
		wantErr    bool
	}{
		{
			name: "rollingUpdate",
			deployment: &apps.Deployment{
				Spec: apps.DeploymentSpec{
					Strategy: apps.DeploymentStrategy{Type: apps.RollingUpdateDeploymentStrategyType},
				},
			},
		},
		{
			name: "recreate",
			deployment: &apps.Deployment{
				Spec: apps.DeploymentSpec{
					Strategy: apps.DeploymentStrategy{Type: apps.RecreateDeploymentStrategyType},
				},
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDeploymentStrategyType(tc.deployment)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateDeploymentStrategyType() error = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateDeploymentStrategyType() unexpected error: %v", err)
			}
		})
	}
}

func inflatedDeployment(mutate func(*apps.Deployment)) *apps.Deployment {
	progressDeadlineSeconds := InflatedProgressDeadlineSeconds
	maxUnavailable := intstr.FromInt(0)
	deployment := &apps.Deployment{
		Spec: apps.DeploymentSpec{
			Paused:                  false,
			MinReadySeconds:         InflatedMinReadySeconds,
			ProgressDeadlineSeconds: &progressDeadlineSeconds,
			Strategy: apps.DeploymentStrategy{
				Type: apps.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
				},
			},
		},
	}
	if mutate != nil {
		mutate(deployment)
	}
	return deployment
}
