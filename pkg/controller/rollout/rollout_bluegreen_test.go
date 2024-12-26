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

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"

	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBlueGreenRunCanary(t *testing.T) {
	cases := []struct {
		name         string
		getObj       func() ([]*apps.Deployment, []*apps.ReplicaSet)
		getNetwork   func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout   func() (*v1beta1.Rollout, *v1beta1.BatchRelease)
		expectStatus func() *v1beta1.RolloutStatus
		expectBr     func() *v1beta1.BatchRelease
	}{
		{
			name: "run bluegreen upgrade1",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				rs1 := rsDemo.DeepCopy()
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease) {
				obj := rolloutDemoBlueGreen.DeepCopy()
				obj.Status.BlueGreenStatus.ObservedWorkloadGeneration = 2
				obj.Status.BlueGreenStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.BlueGreenStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.BlueGreenStatus.UpdatedRevision = "6f8cc56547"
				obj.Status.BlueGreenStatus.CurrentStepIndex = 1
				obj.Status.BlueGreenStatus.NextStepIndex = 2
				obj.Status.BlueGreenStatus.ObservedRolloutID = "88bd5dbfd"
				obj.Status.BlueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemoBlueGreen.Status.DeepCopy()
				s.BlueGreenStatus.ObservedWorkloadGeneration = 2
				s.BlueGreenStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.BlueGreenStatus.StableRevision = "pod-template-hash-v1"
				s.BlueGreenStatus.UpdatedRevision = "6f8cc56547"
				s.BlueGreenStatus.CurrentStepIndex = 1
				s.BlueGreenStatus.NextStepIndex = 2
				s.BlueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				s.BlueGreenStatus.ObservedRolloutID = "88bd5dbfd"
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(s, *cond)
				return s
			},
			expectBr: func() *v1beta1.BatchRelease {
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("50%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				br.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				br.Spec.ReleasePlan.RollingStyle = v1beta1.BlueGreenRollingStyle
				br.Spec.ReleasePlan.RolloutID = "88bd5dbfd"
				return br
			},
		},
		{
			name: "run bluegreen traffic routing",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				rs1 := rsDemo.DeepCopy()
				rs2 := rsDemo.DeepCopy()
				rs2.Name = "echoserver-canary"
				rs2.Labels["pod-template-hash"] = "pod-template-hash-v2"
				rs2.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1, rs2}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease) {
				obj := rolloutDemoBlueGreen.DeepCopy()
				obj.Status.BlueGreenStatus.ObservedWorkloadGeneration = 2
				obj.Status.BlueGreenStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.BlueGreenStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.BlueGreenStatus.UpdatedRevision = "6f8cc56547"
				obj.Status.BlueGreenStatus.CurrentStepIndex = 1
				obj.Status.BlueGreenStatus.NextStepIndex = 2
				obj.Status.BlueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				obj.Status.BlueGreenStatus.ObservedRolloutID = "88bd5dbfd"
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("50%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				br.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				br.Spec.ReleasePlan.RollingStyle = v1beta1.BlueGreenRollingStyle
				br.Spec.ReleasePlan.RolloutID = "88bd5dbfd"
				br.Status = v1beta1.BatchReleaseStatus{
					ObservedGeneration:      1,
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
				s := rolloutDemoBlueGreen.Status.DeepCopy()
				s.BlueGreenStatus.ObservedWorkloadGeneration = 2
				s.BlueGreenStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.BlueGreenStatus.StableRevision = "pod-template-hash-v1"
				s.BlueGreenStatus.UpdatedRevision = "6f8cc56547"
				s.BlueGreenStatus.PodTemplateHash = "pod-template-hash-v2"
				s.BlueGreenStatus.UpdatedReplicas = 1
				s.BlueGreenStatus.UpdatedReadyReplicas = 1
				s.BlueGreenStatus.CurrentStepIndex = 1
				s.BlueGreenStatus.NextStepIndex = 2
				s.BlueGreenStatus.ObservedRolloutID = "88bd5dbfd"
				s.BlueGreenStatus.CurrentStepState = v1beta1.CanaryStepStateTrafficRouting
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(s, *cond)
				return s
			},
			expectBr: func() *v1beta1.BatchRelease {
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("50%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				br.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				br.Spec.ReleasePlan.RollingStyle = v1beta1.BlueGreenRollingStyle
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
			r.blueGreenManager = &blueGreenReleaseManager{
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
			err = r.blueGreenManager.runCanary(c)
			if err != nil {
				t.Fatalf("reconcileRolloutProgressing failed: %s", err.Error())
			}
			checkBatchReleaseEqual(fc, t, client.ObjectKey{Name: rollout.Name}, cs.expectBr())
			cStatus := c.NewStatus.DeepCopy()
			cStatus.Message = ""
			if cStatus.BlueGreenStatus != nil {
				cStatus.BlueGreenStatus.LastUpdateTime = nil
				cStatus.BlueGreenStatus.Message = ""
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

func TestBlueGreenRunCanaryPaused(t *testing.T) {
	cases := []struct {
		name         string
		getRollout   func() *v1beta1.Rollout
		expectStatus func() *v1beta1.RolloutStatus
	}{
		{
			name: "paused, last step, 60% weight",
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemoBlueGreen.DeepCopy()
				obj.Status.BlueGreenStatus.ObservedWorkloadGeneration = 2
				obj.Status.BlueGreenStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.BlueGreenStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.BlueGreenStatus.UpdatedRevision = "6f8cc56547"
				obj.Status.BlueGreenStatus.CurrentStepIndex = 3
				obj.Status.BlueGreenStatus.NextStepIndex = 4
				obj.Status.BlueGreenStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.BlueGreenStatus.CurrentStepState = v1beta1.CanaryStepStatePaused
				return obj
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				obj := rolloutDemoBlueGreen.Status.DeepCopy()
				obj.BlueGreenStatus.ObservedWorkloadGeneration = 2
				obj.BlueGreenStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.BlueGreenStatus.StableRevision = "pod-template-hash-v1"
				obj.BlueGreenStatus.UpdatedRevision = "6f8cc56547"
				obj.BlueGreenStatus.CurrentStepIndex = 3
				obj.BlueGreenStatus.NextStepIndex = 4
				obj.BlueGreenStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.BlueGreenStatus.CurrentStepState = v1beta1.CanaryStepStatePaused
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
			r.blueGreenManager = &blueGreenReleaseManager{
				Client:                fc,
				trafficRoutingManager: r.trafficRoutingManager,
				recorder:              r.Recorder,
			}
			c := &RolloutContext{
				Rollout:   rollout,
				NewStatus: rollout.Status.DeepCopy(),
			}
			err := r.blueGreenManager.runCanary(c)
			if err != nil {
				t.Fatalf("reconcileRolloutProgressing failed: %s", err.Error())
			}
			cStatus := c.NewStatus.DeepCopy()
			cStatus.BlueGreenStatus.LastUpdateTime = nil
			cStatus.BlueGreenStatus.Message = ""
			cStatus.Message = ""
			if !reflect.DeepEqual(cs.expectStatus(), cStatus) {
				t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(cs.expectStatus()), util.DumpJSON(cStatus))
			}
		})
	}
}
