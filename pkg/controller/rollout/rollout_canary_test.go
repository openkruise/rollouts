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

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/trafficrouting"
	"github.com/openkruise/rollouts/pkg/util"
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
				dep1.Labels[v1beta1.RolloutIDLabel] = "test-id"
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
				br.Spec.ReleasePlan.RolloutID = "test-id"
				return br
			},
		},
		{
			name: "run canary traffic routing",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				dep2 := deploymentDemo.DeepCopy()
				dep1.Labels[v1beta1.RolloutIDLabel] = "test-id"
				dep2.Labels[v1beta1.RolloutIDLabel] = "test-id"
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
				obj.Status.CanaryStatus.ObservedRolloutID = "test-id"
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
				br.Spec.ReleasePlan.RolloutID = "test-id"
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
				s.CanaryStatus.ObservedRolloutID = "test-id"
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
				br.Spec.ReleasePlan.RolloutID = "test-id"
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
			// canary revision may change after k8s API changes, munge the revision to make the test stable
			expectStatus.SetCanaryRevision(cStatus.GetCanaryRevision())
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
			expectStatus := cs.expectStatus()
			// canary revision may change after k8s API changes, munge the revision to make the test stable
			expectStatus.SetCanaryRevision(cStatus.GetCanaryRevision())
			if !reflect.DeepEqual(expectStatus, cStatus) {
				t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expectStatus), util.DumpJSON(cStatus))
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
