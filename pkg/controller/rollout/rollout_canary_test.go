/*
Copyright 2022 The Kruise Authors.

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

package rollout

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRunCanary(t *testing.T) {
	cases := []struct {
		name         string
		getObj       func() ([]*apps.Deployment, []*apps.ReplicaSet)
		getNetwork   func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout   func() (*v1beta1.Rollout, *v1beta1.BatchRelease)
		expectStatus func() *v1beta1.RolloutStatus
		expectBr     func() *v1beta1.BatchRelease
	}{
		{
			name: "run canary upgrade1",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				rs1 := rsDemo.DeepCopy()
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.EnableExtraWorkloadForCanary = true
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "6f8cc56547"
				obj.Status.CanaryStatus.CurrentStepIndex = 1
				obj.Status.CanaryStatus.NextStepIndex = 2
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "6f8cc56547"
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.NextStepIndex = 2
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(s, *cond)
				return s
			},
			expectBr: func() *v1beta1.BatchRelease {
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
					{
						CanaryReplicas: intstr.FromInt(2),
					},
					{
						CanaryReplicas: intstr.FromInt(6),
					},
					{
						CanaryReplicas: intstr.FromInt(10),
					},
				}
				br.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				br.Spec.ReleasePlan.EnableExtraWorkloadForCanary = true
				br.Spec.ReleasePlan.RollingStyle = v1beta1.CanaryRollingStyle
				br.Spec.ReleasePlan.RolloutID = "88bd5dbfd"
				return br
			},
		},
		{
			name: "run canary traffic routing",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				dep2 := deploymentDemo.DeepCopy()
				dep2.UID = "1ca4d850-9ec3-48bd-84cb-19f2e8cf4180"
				dep2.Name = dep1.Name + "-canary"
				dep2.Labels[util.CanaryDeploymentLabel] = dep1.Name
				rs1 := rsDemo.DeepCopy()
				rs2 := rsDemo.DeepCopy()
				rs2.Name = "echoserver-canary-2"
				rs2.OwnerReferences = []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       dep2.Name,
						UID:        "1ca4d850-9ec3-48bd-84cb-19f2e8cf4180",
						Controller: utilpointer.Bool(true),
					},
				}
				rs2.Labels["pod-template-hash"] = "pod-template-hash-v2"
				rs2.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return []*apps.Deployment{dep1, dep2}, []*apps.ReplicaSet{rs1, rs2}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.EnableExtraWorkloadForCanary = true
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "6f8cc56547"
				obj.Status.CanaryStatus.CurrentStepIndex = 1
				obj.Status.CanaryStatus.NextStepIndex = 2
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				obj.Status.CanaryStatus.ObservedRolloutID = "88bd5dbfd"
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
					{
						CanaryReplicas: intstr.FromInt(2),
					},
					{
						CanaryReplicas: intstr.FromInt(6),
					},
					{
						CanaryReplicas: intstr.FromInt(10),
					},
				}
				br.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				br.Spec.ReleasePlan.EnableExtraWorkloadForCanary = true
				br.Spec.ReleasePlan.RollingStyle = v1beta1.CanaryRollingStyle
				br.Spec.ReleasePlan.RolloutID = "88bd5dbfd"
				br.Status = v1beta1.BatchReleaseStatus{
					ObservedGeneration: 1,
					// since we use RollingStyle over EnableExtraWorkloadForCanary now, former hardcoded hash
					// should be re-calculated
					ObservedReleasePlanHash: util.HashReleasePlanBatches(&br.Spec.ReleasePlan),
					CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
						CurrentBatchState:    v1beta1.ReadyBatchState,
						CurrentBatch:         0,
						UpdatedReplicas:      1,
						UpdatedReadyReplicas: 1,
					},
				}
				return obj, br
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "6f8cc56547"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CanaryReplicas = 1
				s.CanaryStatus.CanaryReadyReplicas = 1
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.NextStepIndex = 2
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateTrafficRouting
				s.CanaryStatus.ObservedRolloutID = "88bd5dbfd"
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(s, *cond)
				return s
			},
			expectBr: func() *v1beta1.BatchRelease {
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
					{
						CanaryReplicas: intstr.FromInt(2),
					},
					{
						CanaryReplicas: intstr.FromInt(6),
					},
					{
						CanaryReplicas: intstr.FromInt(10),
					},
				}
				br.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				br.Spec.ReleasePlan.EnableExtraWorkloadForCanary = true
				br.Spec.ReleasePlan.RollingStyle = v1beta1.CanaryRollingStyle
				br.Spec.ReleasePlan.RolloutID = "88bd5dbfd"
				return br
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			deps, rss := cs.getObj()
			rollout, br := cs.getRollout()
			fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout).Build()
			for _, rs := range rss {
				_ = fc.Create(context.TODO(), rs)
			}
			for _, dep := range deps {
				_ = fc.Create(context.TODO(), dep)
			}
			if br != nil {
				_ = fc.Create(context.TODO(), br)
			}
			ss, in := cs.getNetwork()
			_ = fc.Create(context.TODO(), ss[0])
			_ = fc.Create(context.TODO(), in[0])
			r := &RolloutReconciler{
				Client:                fc,
				Scheme:                scheme,
				Recorder:              record.NewFakeRecorder(10),
				finder:                util.NewControllerFinder(fc),
				trafficRoutingManager: trafficrouting.NewTrafficRoutingManager(fc),
			}
			r.canaryManager = &canaryReleaseManager{
				Client:                fc,
				trafficRoutingManager: r.trafficRoutingManager,
				recorder:              r.Recorder,
			}
			workload, err := r.finder.GetWorkloadForRef(rollout)
			if err != nil {
				t.Fatalf("GetWorkloadForRef failed: %s", err.Error())
			}
			c := &RolloutContext{
				Rollout:   rollout,
				NewStatus: rollout.Status.DeepCopy(),
				Workload:  workload,
			}
			err = r.canaryManager.runCanary(c)
			if err != nil {
				t.Fatalf("reconcileRolloutProgressing failed: %s", err.Error())
			}
			checkBatchReleaseEqual(fc, t, client.ObjectKey{Name: rollout.Name}, cs.expectBr())
			cStatus := c.NewStatus.DeepCopy()
			cStatus.Message = ""
			if cStatus.CanaryStatus != nil {
				cStatus.CanaryStatus.LastUpdateTime = nil
				cStatus.CanaryStatus.Message = ""
			}
			cond := util.GetRolloutCondition(*cStatus, v1beta1.RolloutConditionProgressing)
			cond.Message = ""
			util.SetRolloutCondition(cStatus, *cond)
			expectStatus := cs.expectStatus()
			if !reflect.DeepEqual(expectStatus, cStatus) {
				t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(cs.expectStatus()), util.DumpJSON(cStatus))
			}
		})
	}
}

func TestRunCanaryPaused(t *testing.T) {
	cases := []struct {
		name         string
		getRollout   func() *v1beta1.Rollout
		expectStatus func() *v1beta1.RolloutStatus
	}{
		{
			name: "paused, last step, 60% weight",
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "6f8cc56547"
				obj.Status.CanaryStatus.CurrentStepIndex = 3
				obj.Status.CanaryStatus.NextStepIndex = 4
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStatePaused
				return obj
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				obj := rolloutDemo.Status.DeepCopy()
				obj.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.CanaryStatus.CanaryRevision = "6f8cc56547"
				obj.CanaryStatus.CurrentStepIndex = 3
				obj.CanaryStatus.NextStepIndex = 4
				obj.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStatePaused
				return obj
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			rollout := cs.getRollout()
			fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout).Build()
			r := &RolloutReconciler{
				Client:                fc,
				Scheme:                scheme,
				Recorder:              record.NewFakeRecorder(10),
				finder:                util.NewControllerFinder(fc),
				trafficRoutingManager: trafficrouting.NewTrafficRoutingManager(fc),
			}
			r.canaryManager = &canaryReleaseManager{
				Client:                fc,
				trafficRoutingManager: r.trafficRoutingManager,
				recorder:              r.Recorder,
			}
			c := &RolloutContext{
				Rollout:   rollout,
				NewStatus: rollout.Status.DeepCopy(),
			}
			err := r.canaryManager.runCanary(c)
			if err != nil {
				t.Fatalf("reconcileRolloutProgressing failed: %s", err.Error())
			}
			cStatus := c.NewStatus.DeepCopy()
			cStatus.CanaryStatus.LastUpdateTime = nil
			cStatus.CanaryStatus.Message = ""
			cStatus.Message = ""
			if !reflect.DeepEqual(cs.expectStatus(), cStatus) {
				t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(cs.expectStatus()), util.DumpJSON(cStatus))
			}
		})
	}
}

func checkBatchReleaseEqual(c client.WithWatch, t *testing.T, key client.ObjectKey, expect *v1beta1.BatchRelease) {
	obj := &v1beta1.BatchRelease{}
	err := c.Get(context.TODO(), key, obj)
	if err != nil {
		t.Fatalf("get object failed: %s", err.Error())
	}
	if !reflect.DeepEqual(expect.Spec, obj.Spec) {
		t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expect.Spec), util.DumpJSON(obj.Spec))
	}
}

func TestDoCanaryJump(t *testing.T) {
	tests := []struct {
		name              string
		rollout           *v1beta1.Rollout
		commonStatus      *v1beta1.CommonStatus
		updatedReplicas   int32
		steps             []v1beta1.CanaryStep
		expectedJumped    bool
		expectedStatus    *v1beta1.CommonStatus
		expectedLogOutput string
	}{
		{
			name: "jump to next step normally",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
							},
						},
					},
				},
			},
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    3, // jump to step 3
				CurrentStepState: v1beta1.CanaryStepStateReady,
				LastUpdateTime:   &metav1.Time{Time: time.Now()},
			},
			updatedReplicas: 1,
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
			},
			expectedJumped: true,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 3,
				NextStepIndex:    -1, // last step
				CurrentStepState: v1beta1.CanaryStepStateInit,
			},
		},
		{
			name: "jump to next step with same replicas",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}}, // same as first step
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
							},
						},
					},
				},
			},
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    2, // jump to step 2
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
			updatedReplicas: 1,
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}}, // same as first step
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
			},
			expectedJumped: false,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    2,
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
		},
		{
			name: "jump to TrafficRouting state",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: 1}},
								{Replicas: &intstr.IntOrString{Type: intstr.Int, IntVal: 1}}, // same as first step
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
							},
						},
					},
				},
			},
			updatedReplicas: 4,
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    3, // jump to step 3
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "40%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "40%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "40%"}},
			},
			expectedJumped: true,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 3,
				NextStepIndex:    -1, // last step
				CurrentStepState: v1beta1.CanaryStepStateTrafficRouting,
			},
		},
		{
			name: "NextStepIndex is default value, no jump",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
							},
						},
					},
				},
			},
			updatedReplicas: 1,
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    2, // normal next step
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
			},
			expectedJumped: false,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    2,
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
		},
		{
			name: "NextStepIndex is -1, no jump",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
							},
						},
					},
				},
			},
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    -1, // indicates completion
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
			updatedReplicas: 1,
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
			},
			expectedJumped: false,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    -1,
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
		},
		{
			name: "NextStepIndex out of range, no jump",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
							},
						},
					},
				},
			},
			updatedReplicas: 1,
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    5, // out of range
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
			},
			expectedJumped: false,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    5,
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
		},
		{
			name: "NextStepIndex is 0, no jump",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
							},
						},
					},
				},
			},
			updatedReplicas: 1,
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    0, // 0 is not used
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
			},
			expectedJumped: false,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    0,
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
		},
		{
			name: "CurrentStepIndex out of bounds, trigger Init state",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
							},
						},
					},
				},
			},
			updatedReplicas: 1,
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 5, // out of range
				NextStepIndex:    2,
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
			},
			expectedJumped: true,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 2,
				NextStepIndex:    -1,
				CurrentStepState: v1beta1.CanaryStepStateInit, // current step replicas set to -1, different from target
			},
		},
		{
			name: "NextStepIndex out of bounds (negative), no jump",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
							},
						},
					},
				},
			},
			updatedReplicas: 1,
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    -2, // negative and not -1
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
			},
			expectedJumped: false,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    -2,
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
		},
		{
			name: "nil status",
			rollout: &v1beta1.Rollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-rollout",
				},
				Spec: v1beta1.RolloutSpec{
					Strategy: v1beta1.RolloutStrategy{
						Canary: &v1beta1.CanaryStrategy{
							Steps: []v1beta1.CanaryStep{
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
								{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
							},
						},
					},
				},
			},
			commonStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    3, // jump to step 3
				CurrentStepState: v1beta1.CanaryStepStateReady,
				LastUpdateTime:   &metav1.Time{Time: time.Now()},
			},
			steps: []v1beta1.CanaryStep{
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"}},
				{Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}},
			},
			expectedJumped: false,
			expectedStatus: &v1beta1.CommonStatus{
				CurrentStepIndex: 1,
				NextStepIndex:    3,
				CurrentStepState: v1beta1.CanaryStepStateReady,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// save original status for comparison
			originalStatus := tt.commonStatus.DeepCopy()

			var newStatus v1beta1.RolloutStatus
			if tt.updatedReplicas > 0 {
				newStatus = v1beta1.RolloutStatus{
					CanaryStatus: &v1beta1.CanaryStatus{
						CommonStatus:        *tt.commonStatus,
						CanaryRevision:      "new-version",
						CanaryReplicas:      tt.updatedReplicas,
						CanaryReadyReplicas: tt.updatedReplicas,
					},
				}
			}

			// execute test
			jumped := doStepJump(tt.rollout, &newStatus, tt.steps, 10)

			// verify results
			if jumped != tt.expectedJumped {
				t.Errorf("doStepJump() jumped = %v, want %v", jumped, tt.expectedJumped)
			}

			// verify status update
			if tt.expectedStatus != nil && newStatus.CanaryStatus != nil {
				tt.commonStatus = &newStatus.CanaryStatus.CommonStatus

				if tt.commonStatus.CurrentStepIndex != tt.expectedStatus.CurrentStepIndex {
					t.Errorf("CurrentStepIndex = %v, want %v", tt.commonStatus.CurrentStepIndex, tt.expectedStatus.CurrentStepIndex)
				}

				if tt.expectedStatus.NextStepIndex != tt.commonStatus.NextStepIndex {
					t.Errorf("NextStepIndex = %v, want: %v", tt.commonStatus.NextStepIndex, tt.expectedStatus.NextStepIndex)
				}

				if tt.commonStatus.CurrentStepState != tt.expectedStatus.CurrentStepState {
					t.Errorf("CurrentStepState = %v, want %v", tt.commonStatus.CurrentStepState, tt.expectedStatus.CurrentStepState)
				}

				// check if LastUpdateTime is updated
				if jumped {
					if tt.commonStatus.LastUpdateTime == nil {
						t.Errorf("LastUpdateTime was not set")
					} else if originalStatus.LastUpdateTime != nil &&
						!tt.commonStatus.LastUpdateTime.After(originalStatus.LastUpdateTime.Time) {
						t.Errorf("LastUpdateTime was not updated")
					}
				}
			}
		})
	}
}
