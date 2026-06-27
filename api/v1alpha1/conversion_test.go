/*
Copyright 2023 The Kruise Authors.

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

package v1alpha1

import (
	"testing"

	"github.com/openkruise/rollouts/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilpointer "k8s.io/utils/pointer"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"github.com/google/go-cmp/cmp"
)

func TestRolloutConversion(t *testing.T) {
	metaNow := metav1.Now()
	// Test cases for conversion
	tests := []struct {
		name            string
		v1alpha1Rollout *Rollout
		v1beta1Rollout  *v1beta1.Rollout
	}{
		{
			name: "basic rollout conversion",
			v1alpha1Rollout: &Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
				},
				Spec: RolloutSpec{
					ObjectRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					Strategy: RolloutStrategy{
						Canary: &CanaryStrategy{
							Steps: []CanaryStep{
								{
									TrafficRoutingStrategy: TrafficRoutingStrategy{
										Weight: utilpointer.Int32(10),
									},
									Pause: RolloutPause{Duration: utilpointer.Int32(30)},
								},
							},
						},
					},
				},
			},
			v1beta1Rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation: "",
					}},
				Spec: v1beta1.RolloutSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							EnableExtraWorkloadForCanary: true,
							Steps: []v1beta1.CanaryStep{
								{
									TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
										Traffic: utilpointer.String("10%"),
									},
									Replicas: &intstr.IntOrString{
										Type:   intstr.String,
										StrVal: "10%",
									},
									Pause: v1beta1.RolloutPause{Duration: utilpointer.Int32(30)},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "rollout with rollout ID",
			v1alpha1Rollout: &Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Labels: map[string]string{
						"app": "test",
					},
				},
				Spec: RolloutSpec{
					DeprecatedRolloutID: "rollout-123",
					ObjectRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					Strategy: RolloutStrategy{
						Canary: &CanaryStrategy{
							Steps: []CanaryStep{
								{
									TrafficRoutingStrategy: TrafficRoutingStrategy{
										Weight: utilpointer.Int32(20),
									},
									Pause: RolloutPause{Duration: utilpointer.Int32(60)},
								},
							},
						},
					},
				},
			},
			v1beta1Rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Labels: map[string]string{
						"app":                  "test",
						v1beta1.RolloutIDLabel: "rollout-123",
					},
					Annotations: map[string]string{
						RolloutStyleAnnotation: "",
					},
				},
				Spec: v1beta1.RolloutSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{
									TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
										Traffic: utilpointer.String("20%"),
									},
									Replicas: &intstr.IntOrString{
										Type:   intstr.String,
										StrVal: "20%",
									},
									Pause: v1beta1.RolloutPause{Duration: utilpointer.Int32(60)},
								},
							},
							EnableExtraWorkloadForCanary: true,
						},
					},
				},
			},
		},
		{
			name: "rollout with annotations",
			v1alpha1Rollout: &Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation:   string(PartitionRollingStyle),
						TrafficRoutingAnnotation: "test-traffic-routing",
						"custom-annotation":      "custom-value",
					},
				},
				Spec: RolloutSpec{
					ObjectRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					Strategy: RolloutStrategy{
						Canary: &CanaryStrategy{
							Steps: []CanaryStep{
								{
									TrafficRoutingStrategy: TrafficRoutingStrategy{
										Weight: utilpointer.Int32(30),
									},
									Pause: RolloutPause{},
								},
							},
						},
					},
				},
			},
			v1beta1Rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation:   string(PartitionRollingStyle),
						TrafficRoutingAnnotation: "test-traffic-routing",
						"custom-annotation":      "custom-value",
					},
				},
				Spec: v1beta1.RolloutSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{
									TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
										Traffic: utilpointer.String("30%"),
									},
									Replicas: &intstr.IntOrString{
										Type:   intstr.String,
										StrVal: "30%",
									},
									Pause: v1beta1.RolloutPause{},
								},
							},
							EnableExtraWorkloadForCanary: false,
							TrafficRoutingRef:            "test-traffic-routing",
						},
					},
				},
			},
		},
		{
			name: "rollout with traffic routing",
			v1alpha1Rollout: &Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
				},
				Spec: RolloutSpec{
					ObjectRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					Strategy: RolloutStrategy{
						Canary: &CanaryStrategy{
							Steps: []CanaryStep{
								{
									TrafficRoutingStrategy: TrafficRoutingStrategy{
										Weight: utilpointer.Int32(40),
									},
									Pause: RolloutPause{Duration: utilpointer.Int32(120)},
								},
							},
							TrafficRoutings: []TrafficRoutingRef{
								{
									Service: "test-service",
									Ingress: &IngressTrafficRouting{
										ClassType: "nginx",
										Name:      "test-ingress",
									},
								},
							},
						},
					},
				},
			},
			v1beta1Rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation: "",
					},
				},
				Spec: v1beta1.RolloutSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{
									TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
										Traffic: utilpointer.String("40%"),
									},
									Replicas: &intstr.IntOrString{
										Type:   intstr.String,
										StrVal: "40%",
									},
									Pause: v1beta1.RolloutPause{Duration: utilpointer.Int32(120)},
								},
							},
							EnableExtraWorkloadForCanary: true,
							TrafficRoutings: []v1beta1.TrafficRoutingRef{
								{
									Service: "test-service",
									Ingress: &v1beta1.IngressTrafficRouting{
										ClassType: "nginx",
										Name:      "test-ingress",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "rollout with patch pod template metadata",
			v1alpha1Rollout: &Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
				},
				Spec: RolloutSpec{
					ObjectRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					Strategy: RolloutStrategy{
						Canary: &CanaryStrategy{
							Steps: []CanaryStep{
								{
									TrafficRoutingStrategy: TrafficRoutingStrategy{
										Weight: utilpointer.Int32(50),
									},
									Pause: RolloutPause{},
								},
							},
							PatchPodTemplateMetadata: &PatchPodTemplateMetadata{
								Labels: map[string]string{
									"test-label": "test-value",
								},
								Annotations: map[string]string{
									"test-annotation": "test-annotation-value",
								},
							},
						},
					},
				},
			},
			v1beta1Rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation: "",
					},
				},
				Spec: v1beta1.RolloutSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{
									TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
										Traffic: utilpointer.String("50%"),
									},
									Replicas: &intstr.IntOrString{
										Type:   intstr.String,
										StrVal: "50%",
									},
									Pause: v1beta1.RolloutPause{},
								},
							},
							EnableExtraWorkloadForCanary: true,
							PatchPodTemplateMetadata: &v1beta1.PatchPodTemplateMetadata{
								Labels: map[string]string{
									"test-label": "test-value",
								},
								Annotations: map[string]string{
									"test-annotation": "test-annotation-value",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "rollout with status",
			v1alpha1Rollout: &Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
				},
				Spec: RolloutSpec{
					ObjectRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					Strategy: RolloutStrategy{
						Canary: &CanaryStrategy{
							Steps: []CanaryStep{
								{
									TrafficRoutingStrategy: TrafficRoutingStrategy{
										Weight: utilpointer.Int32(60),
									},
									Pause: RolloutPause{},
								},
							},
						},
					},
				},
				Status: RolloutStatus{
					ObservedGeneration: 1,
					Phase:              RolloutPhaseProgressing,
					CanaryStatus: &CanaryStatus{
						StableRevision:   "stable-revision",
						CurrentStepIndex: 1,
						CurrentStepState: CanaryStepStateReady,
					},
				},
			},
			v1beta1Rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation: "",
					},
				},
				Spec: v1beta1.RolloutSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{
									TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
										Traffic: utilpointer.String("60%"),
									},
									Replicas: &intstr.IntOrString{
										Type:   intstr.String,
										StrVal: "60%",
									},
									Pause: v1beta1.RolloutPause{},
								},
							},
							EnableExtraWorkloadForCanary: true,
						},
					},
				},
				Status: v1beta1.RolloutStatus{
					ObservedGeneration: 1,
					Phase:              v1beta1.RolloutPhaseProgressing,
					CanaryStatus: &v1beta1.CanaryStatus{
						CommonStatus: v1beta1.CommonStatus{
							StableRevision:   "stable-revision",
							CurrentStepIndex: 1,
							CurrentStepState: v1beta1.CanaryStepStateReady,
						},
					},
					CurrentStepIndex: 1,
					CurrentStepState: v1beta1.CanaryStepStateReady,
				},
			},
		},
		{
			name: "rollout with complex traffic routing strategy",
			v1alpha1Rollout: &Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
				},
				Spec: RolloutSpec{
					ObjectRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					Strategy: RolloutStrategy{
						Canary: &CanaryStrategy{
							Steps: []CanaryStep{
								{
									TrafficRoutingStrategy: TrafficRoutingStrategy{
										Weight: utilpointer.Int32(70),
										Matches: []HttpRouteMatch{
											{
												Headers: []gatewayv1beta1.HTTPHeaderMatch{
													{
														Name:  "test-header",
														Value: "test-value",
														Type:  &[]gatewayv1beta1.HeaderMatchType{gatewayv1beta1.HeaderMatchExact}[0],
													},
												},
											},
										},
										RequestHeaderModifier: &gatewayv1beta1.HTTPHeaderFilter{
											Set: []gatewayv1beta1.HTTPHeader{
												{
													Name:  "X-Test-Header",
													Value: "test-value",
												},
											},
										},
									},
									Pause: RolloutPause{},
								},
							},
						},
					},
				},
			},
			v1beta1Rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation: "",
					},
				},
				Spec: v1beta1.RolloutSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{
									TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
										Traffic: utilpointer.String("70%"),
										Matches: []v1beta1.HttpRouteMatch{
											{
												Headers: []gatewayv1beta1.HTTPHeaderMatch{
													{
														Name:  "test-header",
														Value: "test-value",
														Type:  &[]gatewayv1beta1.HeaderMatchType{gatewayv1beta1.HeaderMatchExact}[0],
													},
												},
											},
										},
										RequestHeaderModifier: &gatewayv1beta1.HTTPHeaderFilter{
											Set: []gatewayv1beta1.HTTPHeader{
												{
													Name:  "X-Test-Header",
													Value: "test-value",
												},
											},
										},
									},
									Replicas: &intstr.IntOrString{
										Type:   intstr.String,
										StrVal: "70%",
									},
									Pause: v1beta1.RolloutPause{},
								},
							},
							EnableExtraWorkloadForCanary: true,
						},
					},
				},
			},
		},
		{
			name: "rollout with conditions",
			v1alpha1Rollout: &Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
				},
				Spec: RolloutSpec{
					ObjectRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					Strategy: RolloutStrategy{
						Canary: &CanaryStrategy{
							Steps: []CanaryStep{
								{
									TrafficRoutingStrategy: TrafficRoutingStrategy{
										Weight: utilpointer.Int32(50),
									},
									Pause: RolloutPause{Duration: utilpointer.Int32(60)},
								},
							},
						},
					},
				},
				Status: RolloutStatus{
					ObservedGeneration: 1,
					Phase:              RolloutPhaseProgressing,
					Message:            "Test rollout message",
					Conditions: []RolloutCondition{
						{
							Type:               RolloutConditionProgressing,
							Status:             corev1.ConditionTrue,
							Reason:             "TestReason",
							Message:            "Test message",
							LastUpdateTime:     metaNow,
							LastTransitionTime: metaNow,
						},
					},
				},
			},
			v1beta1Rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rollout",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation: "",
					},
				},
				Spec: v1beta1.RolloutSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{
									TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
										Traffic: utilpointer.String("50%"),
									},
									Replicas: &intstr.IntOrString{
										Type:   intstr.String,
										StrVal: "50%",
									},
									Pause: v1beta1.RolloutPause{Duration: utilpointer.Int32(60)},
								},
							},
							EnableExtraWorkloadForCanary: true,
						},
					},
				},
				Status: v1beta1.RolloutStatus{
					ObservedGeneration: 1,
					Phase:              v1beta1.RolloutPhaseProgressing,
					Message:            "Test rollout message",
					Conditions: []v1beta1.RolloutCondition{
						{
							Type:               v1beta1.RolloutConditionProgressing,
							Status:             corev1.ConditionTrue,
							Reason:             "TestReason",
							Message:            "Test message",
							LastUpdateTime:     metaNow,
							LastTransitionTime: metaNow,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test v1alpha1 -> v1beta1 conversion
			dst := &v1beta1.Rollout{}
			err := tt.v1alpha1Rollout.ConvertTo(dst)
			if err != nil {
				t.Errorf("ConvertTo() error = %v", err)
				return
			}

			if diff := cmp.Diff(tt.v1beta1Rollout, dst); diff != "" {
				t.Errorf("ConvertTo() mismatch (-want +got):\n%s", diff)
			}

			// Test v1beta1 -> v1alpha1 conversion
			dstAlpha := &Rollout{}
			err = dstAlpha.ConvertFrom(tt.v1beta1Rollout)
			if err != nil {
				t.Errorf("ConvertFrom() error = %v", err)
				return
			}

			// Test round-trip conversion consistency
			roundTrip := &v1beta1.Rollout{}
			err = dstAlpha.ConvertTo(roundTrip)
			if err != nil {
				t.Errorf("Round-trip ConvertTo() error = %v", err)
				return
			}

			// Compare with original v1beta1
			if diff := cmp.Diff(tt.v1beta1Rollout, roundTrip); diff != "" {
				t.Errorf("Round-trip conversion mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBatchReleaseConversion(t *testing.T) {
	metaNow := metav1.Now()
	tests := []struct {
		name                 string
		v1alpha1BatchRelease *BatchRelease
		v1beta1BatchRelease  *v1beta1.BatchRelease
	}{
		{
			name: "basic batch release conversion",
			v1alpha1BatchRelease: &BatchRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-batchrelease",
					Namespace: "default",
				},
				Spec: BatchReleaseSpec{
					TargetRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					ReleasePlan: ReleasePlan{
						BatchPartition: utilpointer.Int32(1),
						Batches: []ReleaseBatch{
							{
								CanaryReplicas: intstr.IntOrString{
									Type:   intstr.String,
									StrVal: "20%",
								},
							},
						},
					},
				},
			},
			v1beta1BatchRelease: &v1beta1.BatchRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-batchrelease",
					Namespace: "default",
				},
				Spec: v1beta1.BatchReleaseSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					ReleasePlan: v1beta1.ReleasePlan{
						BatchPartition: utilpointer.Int32(1),
						Batches: []v1beta1.ReleaseBatch{
							{
								CanaryReplicas: intstr.IntOrString{
									Type:   intstr.String,
									StrVal: "20%",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "batch release with annotations",
			v1alpha1BatchRelease: &BatchRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-batchrelease",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation: string(PartitionRollingStyle),
						"custom-annotation":    "custom-value",
					},
				},
				Spec: BatchReleaseSpec{
					TargetRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					ReleasePlan: ReleasePlan{
						BatchPartition: utilpointer.Int32(2),
						Batches: []ReleaseBatch{
							{
								CanaryReplicas: intstr.IntOrString{
									Type:   intstr.String,
									StrVal: "30%",
								},
							},
							{
								CanaryReplicas: intstr.IntOrString{
									Type:   intstr.Int,
									IntVal: 5,
								},
							},
						},
						RollingStyle: RollingStyleType(PartitionRollingStyle),
					},
				},
			},
			v1beta1BatchRelease: &v1beta1.BatchRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-batchrelease",
					Namespace: "default",
					Annotations: map[string]string{
						RolloutStyleAnnotation: string(PartitionRollingStyle),
						"custom-annotation":    "custom-value",
					},
				},
				Spec: v1beta1.BatchReleaseSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					ReleasePlan: v1beta1.ReleasePlan{
						BatchPartition: utilpointer.Int32(2),
						Batches: []v1beta1.ReleaseBatch{
							{
								CanaryReplicas: intstr.IntOrString{
									Type:   intstr.String,
									StrVal: "30%",
								},
							},
							{
								CanaryReplicas: intstr.IntOrString{
									Type:   intstr.Int,
									IntVal: 5,
								},
							},
						},
						RollingStyle: v1beta1.PartitionRollingStyle,
					},
				},
			},
		},
		{
			name: "batch release with status",
			v1alpha1BatchRelease: &BatchRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-batchrelease",
					Namespace: "default",
				},
				Spec: BatchReleaseSpec{
					TargetRef: ObjectRef{
						WorkloadRef: &WorkloadRef{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
							Name:       "test-deployment",
						},
					},
					ReleasePlan: ReleasePlan{
						Batches: []ReleaseBatch{
							{
								CanaryReplicas: intstr.IntOrString{
									Type:   intstr.String,
									StrVal: "10%",
								},
							},
						},
					},
				},
				Status: BatchReleaseStatus{
					StableRevision:     "stable-revision",
					UpdateRevision:     "update-revision",
					ObservedGeneration: 1,
					Phase:              RolloutPhaseProgressing,
					CanaryStatus: BatchReleaseCanaryStatus{
						CurrentBatch:         1,
						CurrentBatchState:    BatchReleaseBatchStateType("Ready"),
						UpdatedReplicas:      5,
						UpdatedReadyReplicas: 3,
					},
					Conditions: []RolloutCondition{
						{
							Type:               RolloutConditionType("RolloutStarted"),
							Status:             corev1.ConditionTrue,
							LastTransitionTime: metaNow,
						},
					},
				},
			},
			v1beta1BatchRelease: &v1beta1.BatchRelease{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-batchrelease",
					Namespace: "default",
				},
				Spec: v1beta1.BatchReleaseSpec{
					WorkloadRef: v1beta1.ObjectRef{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "test-deployment",
					},
					ReleasePlan: v1beta1.ReleasePlan{
						Batches: []v1beta1.ReleaseBatch{
							{
								CanaryReplicas: intstr.IntOrString{
									Type:   intstr.String,
									StrVal: "10%",
								},
							},
						},
					},
				},
				Status: v1beta1.BatchReleaseStatus{
					StableRevision:     "stable-revision",
					UpdateRevision:     "update-revision",
					ObservedGeneration: 1,
					Phase:              v1beta1.RolloutPhaseProgressing,
					CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
						CurrentBatch:         1,
						CurrentBatchState:    v1beta1.BatchReleaseBatchStateType("Ready"),
						UpdatedReplicas:      5,
						UpdatedReadyReplicas: 3,
					},
					Conditions: []v1beta1.RolloutCondition{
						{
							Type:               v1beta1.RolloutConditionType("RolloutStarted"),
							Status:             corev1.ConditionTrue,
							LastTransitionTime: metaNow,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test v1alpha1 -> v1beta1 conversion
			dst := &v1beta1.BatchRelease{}
			err := tt.v1alpha1BatchRelease.ConvertTo(dst)
			if err != nil {
				t.Errorf("ConvertTo() error = %v", err)
				return
			}

			if diff := cmp.Diff(tt.v1beta1BatchRelease, dst); diff != "" {
				t.Errorf("ConvertTo() mismatch (-want +got):\n%s", diff)
			}

			// Test v1beta1 -> v1alpha1 conversion
			dstAlpha := &BatchRelease{}
			err = dstAlpha.ConvertFrom(tt.v1beta1BatchRelease)
			if err != nil {
				t.Errorf("ConvertFrom() error = %v", err)
				return
			}

			// Test round-trip conversion consistency
			roundTrip := &v1beta1.BatchRelease{}
			err = dstAlpha.ConvertTo(roundTrip)
			if err != nil {
				t.Errorf("Round-trip ConvertTo() error = %v", err)
				return
			}

			// Compare with original v1beta1
			if diff := cmp.Diff(tt.v1beta1BatchRelease, roundTrip); diff != "" {
				t.Errorf("Round-trip conversion mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Helper function to create a deep copy of a Rollout for comparison
func copyV1Alpha1Rollout(in *Rollout) *Rollout {
	// Create a new Rollout
	out := &Rollout{}

	// Copy ObjectMeta
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()

	// Copy Spec
	out.Spec = RolloutSpec{
		ObjectRef: ObjectRef{
			WorkloadRef: in.Spec.ObjectRef.WorkloadRef.DeepCopy(),
		},
		Strategy:            *in.Spec.Strategy.DeepCopy(),
		DeprecatedRolloutID: in.Spec.DeprecatedRolloutID,
		Disabled:            in.Spec.Disabled,
	}

	// Copy Status if exists
	if in.Status.CanaryStatus != nil {
		out.Status = *in.Status.DeepCopy()
	}

	return out
}

// Helper function to create a deep copy of a v1beta1.Rollout for comparison
func copyV1Beta1Rollout(in *v1beta1.Rollout) *v1beta1.Rollout {
	// Create a new Rollout
	out := &v1beta1.Rollout{}

	// Copy ObjectMeta
	out.ObjectMeta = *in.ObjectMeta.DeepCopy()

	// Copy Spec
	out.Spec = v1beta1.RolloutSpec{
		WorkloadRef: in.Spec.WorkloadRef,
		Strategy:    *in.Spec.Strategy.DeepCopy(),
		Disabled:    in.Spec.Disabled,
	}

	// Copy Status if exists
	if in.Status.CanaryStatus != nil {
		out.Status = *in.Status.DeepCopy()
	}

	return out
}
