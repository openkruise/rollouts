/*
Copyright 2021.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	utilpointer "k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileRolloutProgressing(t *testing.T) {
	cases := []struct {
		name         string
		getObj       func() ([]*apps.Deployment, []*apps.ReplicaSet)
		getNetwork   func() ([]*corev1.Service, []*netv1.Ingress)
		getRollout   func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting)
		expectStatus func() *v1beta1.RolloutStatus
		expectTr     func() *v1alpha1.TrafficRouting
	}{
		{
			name: "ReconcileRolloutProgressing init trafficRouting",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				rs1 := rsDemo.DeepCopy()
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Annotations[v1alpha1.TrafficRoutingAnnotation] = "tr-demo"
				return obj, nil, demoTR.DeepCopy()
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.CurrentStepIndex = 1
				// s.CanaryStatus.NextStepIndex will be initialized as 0 in ReconcileRolloutProgressing.
				// util.NextBatchIndex(rollout, s.CanaryStatus.CurrentStepIndex), which is 2 here.
				s.CanaryStatus.NextStepIndex = 2
				// now the first step is no longer StepStateUpgrade, it is StepStateInit now
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateInit
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				s.CanaryStatus.ObservedRolloutID = "88bd5dbfd"
				return s
			},
			expectTr: func() *v1alpha1.TrafficRouting {
				tr := demoTR.DeepCopy()
				tr.Finalizers = []string{util.ProgressingRolloutFinalizer(rolloutDemo.Name)}
				return tr
			},
		},
		{
			name: "ReconcileRolloutProgressing init -> rolling",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				rs1 := rsDemo.DeepCopy()
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				tr := demoTR.DeepCopy()
				tr.Finalizers = []string{util.ProgressingRolloutFinalizer(rolloutDemo.Name)}
				return obj, nil, tr
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.NextStepIndex = 2
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateInit
				s.CanaryStatus.ObservedRolloutID = "88bd5dbfd"
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(s, *cond)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling1",
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
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.CurrentStepIndex = 1
				obj.Status.CanaryStatus.NextStepIndex = 2
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.NextStepIndex = 2
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(s, *cond)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling -> finalizing", // call FinalisingTrafficRouting to restore stable Service
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
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.NextStepIndex = 0
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(s, *cond)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing finalizing1", // call FinalisingTrafficRouting to remove canary service
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				delete(dep1.Annotations, util.InRolloutProgressingAnnotation)
				dep1.Status = apps.DeploymentStatus{
					ObservedGeneration: 2,
					Replicas:           10,
					UpdatedReplicas:    5,
					ReadyReplicas:      10,
					AvailableReplicas:  10,
				}
				rs1 := rsDemo.DeepCopy()
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				s1 := demoService.DeepCopy()
				s1.Spec.Selector[apps.DefaultDeploymentUniqueLabelKey] = "podtemplatehash-v1"
				s2 := demoService.DeepCopy()
				s2.Name = s1.Name + "-canary"
				return []*corev1.Service{s1, s2}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Annotations[v1alpha1.TrafficRoutingAnnotation] = "tr-demo"
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(&obj.Status, *cond)
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
				}
				tr := demoTR.DeepCopy()
				tr.Finalizers = []string{util.ProgressingRolloutFinalizer(rolloutDemo.Name)}
				return obj, br, tr
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.NextStepIndex = 0
				s.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepRestoreStableService
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(s, *cond)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
			expectTr: func() *v1alpha1.TrafficRouting {
				tr := demoTR.DeepCopy()
				return tr
			},
		},
		{
			name: "ReconcileRolloutProgressing finalizing2", // go to the next finalising step
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				delete(dep1.Annotations, util.InRolloutProgressingAnnotation)
				dep1.Status = apps.DeploymentStatus{
					ObservedGeneration: 2,
					Replicas:           10,
					UpdatedReplicas:    5,
					ReadyReplicas:      10,
					AvailableReplicas:  10,
				}
				rs1 := rsDemo.DeepCopy()
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Annotations[v1alpha1.TrafficRoutingAnnotation] = "tr-demo"
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				// given that the selector of stable Service is removed
				// we will go on it the next step, i.e. patch restoreGateway
				obj.Status.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepRestoreStableService
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(&obj.Status, *cond)
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
				}
				tr := demoTR.DeepCopy()
				tr.Finalizers = []string{util.ProgressingRolloutFinalizer(rolloutDemo.Name)}
				return obj, br, tr
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.NextStepIndex = 0
				s.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepRouteTrafficToStable
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(s, *cond)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
			expectTr: func() *v1alpha1.TrafficRouting {
				tr := demoTR.DeepCopy()
				return tr
			},
		},
		{
			name: "ReconcileRolloutProgressing finalizing3",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				delete(dep1.Annotations, util.InRolloutProgressingAnnotation)
				dep1.Status = apps.DeploymentStatus{
					ObservedGeneration: 2,
					Replicas:           10,
					UpdatedReplicas:    5,
					ReadyReplicas:      10,
					AvailableReplicas:  10,
				}
				rs1 := rsDemo.DeepCopy()
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Annotations[v1alpha1.TrafficRoutingAnnotation] = "tr-demo"
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				// because the batchRelease hasn't completed (ie. br.Status.Phase is not completed),
				// it will take more than one reconciles to go on to the next step
				obj.Status.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepResumeWorkload
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(&obj.Status, *cond)
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
				}
				tr := demoTR.DeepCopy()
				tr.Finalizers = []string{util.ProgressingRolloutFinalizer(rolloutDemo.Name)}
				return obj, br, tr
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.NextStepIndex = 0
				s.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepResumeWorkload
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(s, *cond)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
			expectTr: func() *v1alpha1.TrafficRouting {
				tr := demoTR.DeepCopy()
				return tr
			},
		},
		{
			name: "ReconcileRolloutProgressing finalizing4",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				delete(dep1.Annotations, util.InRolloutProgressingAnnotation)
				dep1.Status = apps.DeploymentStatus{
					ObservedGeneration: 2,
					Replicas:           10,
					UpdatedReplicas:    10,
					ReadyReplicas:      10,
					AvailableReplicas:  10,
				}
				rs1 := rsDemo.DeepCopy()
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				// the batchRelease has completed (ie. br.Status.Phase is completed),
				// we expect the finalizing step to be next step, i.e. deleteBatchRelease
				obj.Status.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepResumeWorkload
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(&obj.Status, *cond)
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
				}
				br.Status.Phase = v1beta1.RolloutPhaseCompleted
				return obj, br, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.NextStepIndex = 0
				s.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepReleaseWorkloadControl
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				cond2 := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond2.Reason = v1alpha1.ProgressingReasonFinalising
				cond2.Status = corev1.ConditionTrue
				util.SetRolloutCondition(s, *cond2)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing finalizing -> succeeded",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				delete(dep1.Annotations, util.InRolloutProgressingAnnotation)
				dep1.Status = apps.DeploymentStatus{
					ObservedGeneration: 2,
					Replicas:           10,
					UpdatedReplicas:    10,
					ReadyReplicas:      10,
					AvailableReplicas:  10,
				}
				rs1 := rsDemo.DeepCopy()
				return []*apps.Deployment{dep1}, []*apps.ReplicaSet{rs1}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				// deleteBatchRelease is the last step, and it won't wait a grace time
				// after this step, this release should be succeeded
				obj.Status.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepReleaseWorkloadControl
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.NextStepIndex = 0
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateCompleted
				s.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepTypeEnd
				cond2 := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond2.Reason = v1alpha1.ProgressingReasonCompleted
				cond2.Status = corev1.ConditionFalse
				util.SetRolloutCondition(s, *cond2)
				cond1 := util.NewRolloutCondition(v1beta1.RolloutConditionSucceeded, corev1.ConditionTrue, "", "")
				cond1.LastUpdateTime = metav1.Time{}
				cond1.LastTransitionTime = metav1.Time{}
				util.SetRolloutCondition(s, *cond1)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling -> rollback1",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				dep1.Spec.Template.Spec.Containers[0].Image = "echoserver:v1"
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
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.CurrentStepIndex = 1
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "76569b5df5"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonCancelling
				util.SetRolloutCondition(s, *cond)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling -> rollback2",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				dep1.Spec.Template.Spec.Containers[0].Image = "echoserver:v1"
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
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.CurrentStepIndex = 1
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "76569b5df5"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonCancelling
				util.SetRolloutCondition(s, *cond)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling -> continueRelease1", // add grace time to test the first step: restoring the gateway
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				dep1.Spec.Template.Spec.Containers[0].Image = "echoserver:v3"
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
				c1 := demoIngress.DeepCopy()
				c2 := demoIngress.DeepCopy()
				c2.Name = c2.Name + "-canary"
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{c1, c2}
			},
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.TrafficRoutings[0].GracePeriodSeconds = 1 // add grace time to test fine step in continuous logic
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.CurrentStepIndex = 3
				obj.Status.CanaryStatus.CanaryReplicas = 5
				obj.Status.CanaryStatus.CanaryReadyReplicas = 3
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "88bd5dbfd"
				s.CanaryStatus.CurrentStepIndex = 3
				s.CanaryStatus.CanaryReplicas = 5
				s.CanaryStatus.CanaryReadyReplicas = 3
				s.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepRouteTrafficToStable
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(*s, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(s, *cond)
				s.CurrentStepIndex = s.CanaryStatus.CurrentStepIndex
				s.CurrentStepState = s.CanaryStatus.CurrentStepState
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling -> continueRelease2",
			getObj: func() ([]*apps.Deployment, []*apps.ReplicaSet) {
				dep1 := deploymentDemo.DeepCopy()
				dep1.Spec.Template.Spec.Containers[0].Image = "echoserver:v3"
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
			getRollout: func() (*v1beta1.Rollout, *v1beta1.BatchRelease, *v1alpha1.TrafficRouting) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "88bd5dbfd"
				obj.Status.CanaryStatus.CurrentStepIndex = 3
				obj.Status.CanaryStatus.CanaryReplicas = 5
				obj.Status.CanaryStatus.CanaryReadyReplicas = 3
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepState = v1beta1.CanaryStepStateUpgrade
				obj.Status.CanaryStatus.FinalisingStep = v1beta1.FinalisingStepReleaseWorkloadControl
				cond := util.GetRolloutCondition(obj.Status, v1beta1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil, nil
			},
			expectStatus: func() *v1beta1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.Clear()
				return s
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			deps, rss := cs.getObj()
			rollout, br, tr := cs.getRollout()
			fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout, demoConf.DeepCopy()).Build()
			for _, rs := range rss {
				_ = fc.Create(context.TODO(), rs)
			}
			for _, dep := range deps {
				_ = fc.Create(context.TODO(), dep)
			}
			if br != nil {
				_ = fc.Create(context.TODO(), br)
			}
			if tr != nil {
				_ = fc.Create(context.TODO(), tr)
			}
			ss, in := cs.getNetwork()
			for _, obj := range ss {
				_ = fc.Create(context.TODO(), obj)
			}
			for _, obj := range in {
				_ = fc.Create(context.TODO(), obj)
			}
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
			newStatus := rollout.Status.DeepCopy()
			_, err := r.reconcileRolloutProgressing(rollout, newStatus)
			if err != nil {
				t.Fatalf("reconcileRolloutProgressing failed: %s", err.Error())
			}
			_ = r.updateRolloutStatusInternal(rollout, *newStatus)
			checkRolloutEqual(fc, t, client.ObjectKey{Name: rollout.Name}, cs.expectStatus())
			if cs.expectTr != nil {
				expectTr := cs.expectTr()
				obj := &v1alpha1.TrafficRouting{}
				err = fc.Get(context.TODO(), client.ObjectKey{Name: expectTr.Name}, obj)
				if err != nil {
					t.Fatalf("get object failed: %s", err.Error())
				}
				if !reflect.DeepEqual(obj.Finalizers, expectTr.Finalizers) {
					t.Fatalf("expect(%s), but get(%s)", expectTr.Finalizers, obj.Finalizers)
				}
			}
		})
	}
}

func checkRolloutEqual(c client.WithWatch, t *testing.T, key client.ObjectKey, expect *v1beta1.RolloutStatus) {
	obj := &v1beta1.Rollout{}
	err := c.Get(context.TODO(), key, obj)
	if err != nil {
		t.Fatalf("get object failed: %s", err.Error())
	}
	cStatus := obj.Status.DeepCopy()
	cStatus.Message = ""
	if cStatus.CanaryStatus != nil {
		cStatus.CanaryStatus.LastUpdateTime = nil
	}
	cond1 := util.GetRolloutCondition(*cStatus, v1beta1.RolloutConditionProgressing)
	cond1.Message = ""
	util.SetRolloutCondition(cStatus, *cond1)
	cond2 := util.GetRolloutCondition(*cStatus, v1beta1.RolloutConditionSucceeded)
	if cond2 != nil {
		util.RemoveRolloutCondition(cStatus, v1beta1.RolloutConditionSucceeded)
		cond2.LastUpdateTime = metav1.Time{}
		cond2.LastTransitionTime = metav1.Time{}
		util.SetRolloutCondition(cStatus, *cond2)
	}
	if expect.CanaryStatus != nil && cStatus.CanaryStatus != nil && expect.CanaryStatus.CanaryRevision != cStatus.CanaryStatus.CanaryRevision {
		t.Fatalf("canary revision not equal: expect(%s), but get(%s)", expect.CanaryStatus.CanaryRevision, cStatus.CanaryStatus.CanaryRevision)
	}
	if !reflect.DeepEqual(expect, cStatus) {
		t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expect), util.DumpJSON(cStatus))
	}
}

func TestReCalculateCanaryStepIndex(t *testing.T) {
	cases := []struct {
		name            string
		getObj          func() (*apps.Deployment, *apps.ReplicaSet)
		getRollout      func() *v1beta1.Rollout
		getBatchRelease func() *v1beta1.BatchRelease
		expectStepIndex int32
	}{
		{
			name: "steps changed v1",
			getObj: func() (*apps.Deployment, *apps.ReplicaSet) {
				obj := deploymentDemo.DeepCopy()
				return obj, rsDemo.DeepCopy()
			},
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1beta1.CanaryStep{
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "20%"},
					},
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "50%"},
					},
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
					},
				}
				return obj
			},
			getBatchRelease: func() *v1beta1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("40%"),
					},
					{
						CanaryReplicas: intstr.FromString("60%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				obj.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				return obj
			},
			expectStepIndex: 2,
		},
		{
			name: "steps changed v2",
			getObj: func() (*apps.Deployment, *apps.ReplicaSet) {
				obj := deploymentDemo.DeepCopy()
				return obj, rsDemo.DeepCopy()
			},
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1beta1.CanaryStep{
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "20%"},
					},
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "40%"},
					},
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
					},
				}
				return obj
			},
			getBatchRelease: func() *v1beta1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("40%"),
					},
					{
						CanaryReplicas: intstr.FromString("60%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				obj.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				return obj
			},
			expectStepIndex: 2,
		},
		{
			name: "steps changed v3",
			getObj: func() (*apps.Deployment, *apps.ReplicaSet) {
				obj := deploymentDemo.DeepCopy()
				return obj, rsDemo.DeepCopy()
			},
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1beta1.CanaryStep{
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "40%"},
					},
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "60%"},
					},
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
					},
				}
				return obj
			},
			getBatchRelease: func() *v1beta1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("20%"),
					},
					{
						CanaryReplicas: intstr.FromString("40%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				obj.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(1)
				return obj
			},
			expectStepIndex: 1,
		},
		{
			name: "steps changed v4",
			getObj: func() (*apps.Deployment, *apps.ReplicaSet) {
				obj := deploymentDemo.DeepCopy()
				return obj, rsDemo.DeepCopy()
			},
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1beta1.CanaryStep{
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "10%"},
					},
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "30%"},
					},
					{
						Replicas: &intstr.IntOrString{Type: intstr.String, StrVal: "100%"},
					},
				}
				return obj
			},
			getBatchRelease: func() *v1beta1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("20%"),
					},
					{
						CanaryReplicas: intstr.FromString("40%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				}
				obj.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				return obj
			},
			expectStepIndex: 2,
		},
		{
			name: "steps changed v5",
			getObj: func() (*apps.Deployment, *apps.ReplicaSet) {
				obj := deploymentDemo.DeepCopy()
				return obj, rsDemo.DeepCopy()
			},
			getRollout: func() *v1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1beta1.CanaryStep{
					{
						TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
							Traffic: utilpointer.String("2%"),
						},
						Replicas: &intstr.IntOrString{
							Type:   intstr.String,
							StrVal: "10%",
						},
					},
					{
						TrafficRoutingStrategy: v1beta1.TrafficRoutingStrategy{
							Traffic: utilpointer.String("3%"),
						},
						Replicas: &intstr.IntOrString{
							Type:   intstr.String,
							StrVal: "10%",
						},
					},
				}
				return obj
			},
			getBatchRelease: func() *v1beta1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("10%"),
					},
					{
						CanaryReplicas: intstr.FromString("20%"),
					},
					{
						CanaryReplicas: intstr.FromString("30%"),
					},
				}
				obj.Spec.ReleasePlan.BatchPartition = utilpointer.Int32(0)
				return obj
			},
			expectStepIndex: 1,
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			cli := fake.NewClientBuilder().WithScheme(scheme).Build()
			_ = cli.Create(context.TODO(), cs.getBatchRelease())
			dep, rs := cs.getObj()
			_ = cli.Create(context.TODO(), dep)
			_ = cli.Create(context.TODO(), rs)
			_ = cli.Create(context.TODO(), cs.getRollout())
			reconciler := &RolloutReconciler{
				Client: cli,
				Scheme: scheme,
				finder: util.NewControllerFinder(cli),
			}
			reconciler.canaryManager = &canaryReleaseManager{
				Client:                cli,
				trafficRoutingManager: reconciler.trafficRoutingManager,
				recorder:              reconciler.Recorder,
			}
			rollout := cs.getRollout()
			workload, err := reconciler.finder.GetWorkloadForRef(rollout)
			if err != nil {
				t.Fatalf(err.Error())
			}
			c := &RolloutContext{Rollout: rollout, Workload: workload}
			newStepIndex, err := reconciler.recalculateCanaryStep(c)
			if err != nil {
				t.Fatalf(err.Error())
			}
			if cs.expectStepIndex != newStepIndex {
				t.Fatalf("expect %d, but %d", cs.expectStepIndex, newStepIndex)
			}
		})
	}
}
