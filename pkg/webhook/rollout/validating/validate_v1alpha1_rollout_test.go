package validating

import (
	"strings"
	"testing"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func int32Ptr(i int32) *int32 {
	return &i
}

// Helper function to check if an error list contains a specific field path
func containsField(errList field.ErrorList, fieldPath string) bool {
	for _, err := range errList {
		if strings.Contains(err.Field, fieldPath) {
			return true
		}
	}
	return false
}

func TestValidateV1alpha1Rollout(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1alpha1.AddToScheme(scheme)

	tests := []struct {
		name    string
		rollout *appsv1alpha1.Rollout
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid rollout with replicas",
			rollout: &appsv1alpha1.Rollout{
				ObjectMeta: metav1.ObjectMeta{Name: "valid-rollout", Namespace: "default"},
				Spec: appsv1alpha1.RolloutSpec{
					ObjectRef: appsv1alpha1.ObjectRef{
						WorkloadRef: &appsv1alpha1.WorkloadRef{
							APIVersion: "apps/v1", Kind: "Deployment", Name: "test",
						},
					},
					Strategy: appsv1alpha1.RolloutStrategy{
						Canary: &appsv1alpha1.CanaryStrategy{
							Steps: []appsv1alpha1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing workloadRef",
			rollout: &appsv1alpha1.Rollout{
				ObjectMeta: metav1.ObjectMeta{Name: "rollout-no-ref"},
				Spec: appsv1alpha1.RolloutSpec{
					ObjectRef: appsv1alpha1.ObjectRef{}, // WorkloadRef is nil
					Strategy: appsv1alpha1.RolloutStrategy{
						Canary: nil,
					},
				},
			},
			wantErr: true,
			errMsg:  "WorkloadRef is required",
		},
		{
			name: "invalid rolling style",
			rollout: &appsv1alpha1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "rollout-invalid-style",
					Annotations: map[string]string{appsv1alpha1.RolloutStyleAnnotation: "invalid-style"},
				},
				Spec: appsv1alpha1.RolloutSpec{
					ObjectRef: appsv1alpha1.ObjectRef{
						WorkloadRef: &appsv1alpha1.WorkloadRef{APIVersion: "apps/v1", Kind: "Deployment", Name: "test"},
					},
					Strategy: appsv1alpha1.RolloutStrategy{
						Canary: &appsv1alpha1.CanaryStrategy{
							Steps: []appsv1alpha1.CanaryStep{{Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: 1}}},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "Rolling style must be 'Canary', 'Partition' or empty",
		},
		{
			name: "empty steps",
			rollout: &appsv1alpha1.Rollout{
				ObjectMeta: metav1.ObjectMeta{Name: "rollout-empty-steps"},
				Spec: appsv1alpha1.RolloutSpec{
					ObjectRef: appsv1alpha1.ObjectRef{
						WorkloadRef: &appsv1alpha1.WorkloadRef{APIVersion: "apps/v1", Kind: "Deployment", Name: "test"},
					},
					Strategy: appsv1alpha1.RolloutStrategy{
						Canary: &appsv1alpha1.CanaryStrategy{Steps: []appsv1alpha1.CanaryStep{}},
					},
				},
			},
			wantErr: true,
			errMsg:  "The number of Canary.Steps cannot be empty",
		},
		{
			name: "step with no replicas defined",
			rollout: &appsv1alpha1.Rollout{
				ObjectMeta: metav1.ObjectMeta{Name: "rollout-invalid-step"},
				Spec: appsv1alpha1.RolloutSpec{
					ObjectRef: appsv1alpha1.ObjectRef{
						WorkloadRef: &appsv1alpha1.WorkloadRef{APIVersion: "apps/v1", Kind: "Deployment", Name: "test"},
					},
					Strategy: appsv1alpha1.RolloutStrategy{
						Canary: &appsv1alpha1.CanaryStrategy{
							Steps: []appsv1alpha1.CanaryStep{{}}, // Invalid step
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "weight and replicas cannot be empty at the same time",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			handler := &RolloutCreateUpdateHandler{Client: fakeClient}
			errList := handler.validateV1alpha1Rollout(tt.rollout)
			if tt.wantErr {
				assert.NotEmpty(t, errList)
				assert.Contains(t, errList.ToAggregate().Error(), tt.errMsg)
			} else {
				assert.Empty(t, errList)
			}
		})
	}
}

func TestValidateV1alpha1RolloutUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1alpha1.AddToScheme(scheme)

	baseRollout := &appsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: appsv1alpha1.RolloutSpec{
			ObjectRef: appsv1alpha1.ObjectRef{
				WorkloadRef: &appsv1alpha1.WorkloadRef{
					APIVersion: "apps/v1", Kind: "Deployment", Name: "test-workload",
				},
			},
			Strategy: appsv1alpha1.RolloutStrategy{
				Canary: &appsv1alpha1.CanaryStrategy{
					Steps: []appsv1alpha1.CanaryStep{
						{Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: 1}},
					},
					TrafficRoutings: []appsv1alpha1.TrafficRoutingRef{
						{Service: "test-service", Ingress: &appsv1alpha1.IngressTrafficRouting{Name: "test-ingress"}},
					},
				},
			},
		},
		Status: appsv1alpha1.RolloutStatus{Phase: appsv1alpha1.RolloutPhaseInitial},
	}

	tests := []struct {
		name       string
		oldRollout *appsv1alpha1.Rollout
		newRollout *appsv1alpha1.Rollout
		wantErr    bool
		errMsg     string
	}{
		{
			name:       "allow update in initial phase",
			oldRollout: baseRollout,
			newRollout: func() *appsv1alpha1.Rollout {
				r := baseRollout.DeepCopy()
				r.Spec.Strategy.Canary.Steps[0].Replicas = &intstr.IntOrString{Type: intstr.Int, IntVal: 2}
				return r
			}(),
			wantErr: false,
		},
		{
			name: "forbid workloadRef change in progressing phase",
			oldRollout: func() *appsv1alpha1.Rollout {
				r := baseRollout.DeepCopy()
				r.Status.Phase = appsv1alpha1.RolloutPhaseProgressing
				return r
			}(),
			newRollout: func() *appsv1alpha1.Rollout {
				r := baseRollout.DeepCopy()
				r.Status.Phase = appsv1alpha1.RolloutPhaseProgressing
				r.Spec.ObjectRef.WorkloadRef.Name = "new-workload"
				return r
			}(),
			wantErr: true,
			errMsg:  "'ObjectRef' field is immutable",
		},
		{
			name: "forbid traffic routing change in terminating phase",
			oldRollout: func() *appsv1alpha1.Rollout {
				r := baseRollout.DeepCopy()
				r.Status.Phase = appsv1alpha1.RolloutPhaseTerminating
				return r
			}(),
			newRollout: func() *appsv1alpha1.Rollout {
				r := baseRollout.DeepCopy()
				r.Status.Phase = appsv1alpha1.RolloutPhaseTerminating
				r.Spec.Strategy.Canary.TrafficRoutings = []appsv1alpha1.TrafficRoutingRef{
					{Service: "another-service", Ingress: &appsv1alpha1.IngressTrafficRouting{Name: "test-ingress"}},
				}
				return r
			}(),
			wantErr: true,
			errMsg:  "'Strategy.Canary.TrafficRoutings' field is immutable",
		},
		{
			name: "forbid rolling style change in progressing phase",
			oldRollout: func() *appsv1alpha1.Rollout {
				r := baseRollout.DeepCopy()
				r.Status.Phase = appsv1alpha1.RolloutPhaseProgressing
				return r
			}(),
			newRollout: func() *appsv1alpha1.Rollout {
				r := baseRollout.DeepCopy()
				r.Status.Phase = appsv1alpha1.RolloutPhaseProgressing
				r.Annotations = map[string]string{appsv1alpha1.RolloutStyleAnnotation: "Partition"}
				return r
			}(),
			wantErr: true,
			errMsg:  "'Rolling-Style' annotation is immutable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.oldRollout).
				Build()

			handler := &RolloutCreateUpdateHandler{Client: fakeClient}
			errList := handler.validateV1alpha1RolloutUpdate(tt.oldRollout, tt.newRollout)

			if tt.wantErr {
				assert.NotEmpty(t, errList)
				assert.Contains(t, errList.ToAggregate().Error(), tt.errMsg)
			} else {
				assert.Empty(t, errList)
			}
		})
	}
}

func TestValidateV1alpha1RolloutSpecCanarySteps(t *testing.T) {
	ctxCanary := &validateContext{style: string(appsv1alpha1.CanaryRollingStyle)}
	tests := []struct {
		name     string
		ctx      *validateContext
		steps    []appsv1alpha1.CanaryStep
		traffic  bool
		wantErr  bool
		errField string
	}{
		{
			name:    "valid steps with non-decreasing replicas",
			ctx:     ctxCanary,
			traffic: false,
			steps: []appsv1alpha1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: 2}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "50%"}},
			},
			wantErr: false,
		},
		{
			name:    "decreasing replicas",
			ctx:     ctxCanary,
			traffic: false,
			steps: []appsv1alpha1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: 5}},
				{Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: 3}},
			},
			wantErr:  true,
			errField: "CanaryReplicas",
		},
		{
			name:    "invalid replica percentage > 100%",
			ctx:     ctxCanary,
			traffic: false,
			steps: []appsv1alpha1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "120%"}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errList := validateV1alpha1RolloutSpecCanarySteps(tt.ctx, tt.steps, field.NewPath("steps"), tt.traffic)
			if tt.wantErr {
				assert.NotEmpty(t, errList)
				if tt.errField != "" {
					assert.True(t, containsField(errList, tt.errField), "expected error on field %s", tt.errField)
				}
			} else {
				assert.Empty(t, errList, "expected no errors but got: %v", errList)
			}
		})
	}
}

func TestValidateV1alpha1RolloutConflict(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1alpha1.AddToScheme(scheme)

	workloadRef := &appsv1alpha1.WorkloadRef{
		APIVersion: "apps/v1", Kind: "Deployment", Name: "test",
	}

	existingRollout := &appsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "existing-rollout", Namespace: "default"},
		Spec:       appsv1alpha1.RolloutSpec{ObjectRef: appsv1alpha1.ObjectRef{WorkloadRef: workloadRef}},
	}

	newRollout := &appsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "new-rollout", Namespace: "default"},
		Spec:       appsv1alpha1.RolloutSpec{ObjectRef: appsv1alpha1.ObjectRef{WorkloadRef: workloadRef}},
	}

	tests := []struct {
		name     string
		existing []client.Object
		rollout  *appsv1alpha1.Rollout
		wantErr  bool
	}{
		{
			name:     "no conflict",
			existing: []client.Object{},
			rollout:  newRollout,
			wantErr:  false,
		},
		{
			name:     "conflict with existing rollout",
			existing: []client.Object{existingRollout},
			rollout:  newRollout,
			wantErr:  true,
		},
		{
			name:     "no conflict if workload is different",
			existing: []client.Object{existingRollout},
			rollout: func() *appsv1alpha1.Rollout {
				r := newRollout.DeepCopy()
				r.Spec.ObjectRef.WorkloadRef.Name = "different-workload"
				return r
			}(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.existing...).
				Build()

			handler := &RolloutCreateUpdateHandler{Client: fakeClient}
			errList := handler.validateV1alpha1RolloutConflict(tt.rollout, field.NewPath("spec"))

			if tt.wantErr {
				assert.NotEmpty(t, errList)
				assert.Contains(t, errList.ToAggregate().Error(), "conflict with Rollout")
			} else {
				assert.Empty(t, errList)
			}
		})
	}
}
