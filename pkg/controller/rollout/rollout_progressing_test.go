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
		getRollout   func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease)
		expectStatus func() *v1alpha1.RolloutStatus
	}{
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
			getRollout: func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				return obj, nil
			},
			expectStatus: func() *v1alpha1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "56855c89f9"
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(*s, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(s, *cond)
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
						Controller: utilpointer.BoolPtr(true),
					},
				}
				rs2.Labels["pod-template-hash"] = "pod-template-hash-v2"
				rs2.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return []*apps.Deployment{dep1, dep2}, []*apps.ReplicaSet{rs1, rs2}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "56855c89f9"
				obj.Status.CanaryStatus.CurrentStepIndex = 1
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil
			},
			expectStatus: func() *v1alpha1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "56855c89f9"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(*s, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(s, *cond)
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling -> finalizing",
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
						Controller: utilpointer.BoolPtr(true),
					},
				}
				rs2.Labels["pod-template-hash"] = "pod-template-hash-v2"
				rs2.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return []*apps.Deployment{dep1, dep2}, []*apps.ReplicaSet{rs1, rs2}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "56855c89f9"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(obj.Status, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil
			},
			expectStatus: func() *v1alpha1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "56855c89f9"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(*s, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(s, *cond)
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing finalizing1",
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
			getRollout: func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "56855c89f9"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(obj.Status, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(&obj.Status, *cond)
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
				}
				return obj, br
			},
			expectStatus: func() *v1alpha1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "56855c89f9"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(*s, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(s, *cond)
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing finalizing2",
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
			getRollout: func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "56855c89f9"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(obj.Status, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(&obj.Status, *cond)
				br := batchDemo.DeepCopy()
				br.Spec.ReleasePlan.Batches = []v1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromInt(1),
					},
				}
				br.Status.Phase = v1alpha1.RolloutPhaseCompleted
				return obj, br
			},
			expectStatus: func() *v1alpha1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "56855c89f9"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				cond2 := util.GetRolloutCondition(*s, v1alpha1.RolloutConditionProgressing)
				cond2.Reason = v1alpha1.ProgressingReasonFinalising
				cond2.Status = corev1.ConditionTrue
				util.SetRolloutCondition(s, *cond2)
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
			getRollout: func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "56855c89f9"
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepIndex = 4
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				cond := util.GetRolloutCondition(obj.Status, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonFinalising
				cond.Status = corev1.ConditionTrue
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil
			},
			expectStatus: func() *v1alpha1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "56855c89f9"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 4
				s.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateCompleted
				cond2 := util.GetRolloutCondition(*s, v1alpha1.RolloutConditionProgressing)
				cond2.Reason = v1alpha1.ProgressingReasonCompleted
				cond2.Status = corev1.ConditionFalse
				util.SetRolloutCondition(s, *cond2)
				cond1 := util.NewRolloutCondition(v1alpha1.RolloutConditionSucceeded, corev1.ConditionTrue, "", "")
				cond1.LastUpdateTime = metav1.Time{}
				cond1.LastTransitionTime = metav1.Time{}
				util.SetRolloutCondition(s, *cond1)
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling -> rollback",
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
						Controller: utilpointer.BoolPtr(true),
					},
				}
				rs2.Labels["pod-template-hash"] = "pod-template-hash-v2"
				rs2.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return []*apps.Deployment{dep1, dep2}, []*apps.ReplicaSet{rs1, rs2}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "56855c89f9"
				obj.Status.CanaryStatus.CurrentStepIndex = 1
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil
			},
			expectStatus: func() *v1alpha1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "5d48f79ff8"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(*s, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonCancelling
				util.SetRolloutCondition(s, *cond)
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling -> rollback",
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
						Controller: utilpointer.BoolPtr(true),
					},
				}
				rs2.Labels["pod-template-hash"] = "pod-template-hash-v2"
				rs2.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return []*apps.Deployment{dep1, dep2}, []*apps.ReplicaSet{rs1, rs2}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "56855c89f9"
				obj.Status.CanaryStatus.CurrentStepIndex = 1
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil
			},
			expectStatus: func() *v1alpha1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus.ObservedWorkloadGeneration = 2
				s.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				s.CanaryStatus.StableRevision = "pod-template-hash-v1"
				s.CanaryStatus.CanaryRevision = "5d48f79ff8"
				s.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				s.CanaryStatus.CurrentStepIndex = 1
				s.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(*s, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonCancelling
				util.SetRolloutCondition(s, *cond)
				return s
			},
		},
		{
			name: "ReconcileRolloutProgressing rolling -> continueRelease",
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
						Controller: utilpointer.BoolPtr(true),
					},
				}
				rs2.Labels["pod-template-hash"] = "pod-template-hash-v2"
				rs2.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return []*apps.Deployment{dep1, dep2}, []*apps.ReplicaSet{rs1, rs2}
			},
			getNetwork: func() ([]*corev1.Service, []*netv1.Ingress) {
				return []*corev1.Service{demoService.DeepCopy()}, []*netv1.Ingress{demoIngress.DeepCopy()}
			},
			getRollout: func() (*v1alpha1.Rollout, *v1alpha1.BatchRelease) {
				obj := rolloutDemo.DeepCopy()
				obj.Status.CanaryStatus.ObservedWorkloadGeneration = 2
				obj.Status.CanaryStatus.RolloutHash = "f55bvd874d5f2fzvw46bv966x4bwbdv4wx6bd9f7b46ww788954b8z8w29b7wxfd"
				obj.Status.CanaryStatus.StableRevision = "pod-template-hash-v1"
				obj.Status.CanaryStatus.CanaryRevision = "56855c89f9"
				obj.Status.CanaryStatus.CurrentStepIndex = 3
				obj.Status.CanaryStatus.CanaryReplicas = 5
				obj.Status.CanaryStatus.CanaryReadyReplicas = 3
				obj.Status.CanaryStatus.PodTemplateHash = "pod-template-hash-v2"
				obj.Status.CanaryStatus.CurrentStepState = v1alpha1.CanaryStepStateUpgrade
				cond := util.GetRolloutCondition(obj.Status, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInRolling
				util.SetRolloutCondition(&obj.Status, *cond)
				return obj, nil
			},
			expectStatus: func() *v1alpha1.RolloutStatus {
				s := rolloutDemo.Status.DeepCopy()
				s.CanaryStatus = nil
				cond := util.GetRolloutCondition(*s, v1alpha1.RolloutConditionProgressing)
				cond.Reason = v1alpha1.ProgressingReasonInitializing
				util.SetRolloutCondition(s, *cond)
				return s
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			deps, rss := cs.getObj()
			rollout, br := cs.getRollout()
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
		})
	}
}

func checkRolloutEqual(c client.WithWatch, t *testing.T, key client.ObjectKey, expect *v1alpha1.RolloutStatus) {
	obj := &v1alpha1.Rollout{}
	err := c.Get(context.TODO(), key, obj)
	if err != nil {
		t.Fatalf("get object failed: %s", err.Error())
	}
	cStatus := obj.Status.DeepCopy()
	cStatus.Message = ""
	if cStatus.CanaryStatus != nil {
		cStatus.CanaryStatus.LastUpdateTime = nil
	}
	cond1 := util.GetRolloutCondition(*cStatus, v1alpha1.RolloutConditionProgressing)
	cond1.Message = ""
	util.SetRolloutCondition(cStatus, *cond1)
	cond2 := util.GetRolloutCondition(*cStatus, v1alpha1.RolloutConditionSucceeded)
	if cond2 != nil {
		util.RemoveRolloutCondition(cStatus, v1alpha1.RolloutConditionSucceeded)
		cond2.LastUpdateTime = metav1.Time{}
		cond2.LastTransitionTime = metav1.Time{}
		util.SetRolloutCondition(cStatus, *cond2)
	}
	if !reflect.DeepEqual(expect, cStatus) {
		t.Fatalf("expect(%s), but get(%s)", util.DumpJSON(expect), util.DumpJSON(cStatus))
	}
}

func TestReCalculateCanaryStepIndex(t *testing.T) {
	cases := []struct {
		name            string
		getObj          func() (*apps.Deployment, *apps.ReplicaSet)
		getRollout      func() *v1alpha1.Rollout
		getBatchRelease func() *v1alpha1.BatchRelease
		expectStepIndex int32
	}{
		{
			name: "steps changed v1",
			getObj: func() (*apps.Deployment, *apps.ReplicaSet) {
				obj := deploymentDemo.DeepCopy()
				return obj, rsDemo.DeepCopy()
			},
			getRollout: func() *v1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
					{
						Weight: utilpointer.Int32(20),
					},
					{
						Weight: utilpointer.Int32(50),
					},
					{
						Weight: utilpointer.Int32(100),
					},
				}
				return obj
			},
			getBatchRelease: func() *v1alpha1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1alpha1.ReleaseBatch{
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
			getRollout: func() *v1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
					{
						Weight: utilpointer.Int32(20),
					},
					{
						Weight: utilpointer.Int32(40),
					},
					{
						Weight: utilpointer.Int32(100),
					},
				}
				return obj
			},
			getBatchRelease: func() *v1alpha1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1alpha1.ReleaseBatch{
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
			getRollout: func() *v1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
					{
						Weight: utilpointer.Int32(40),
					},
					{
						Weight: utilpointer.Int32(60),
					},
					{
						Weight: utilpointer.Int32(100),
					},
				}
				return obj
			},
			getBatchRelease: func() *v1alpha1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1alpha1.ReleaseBatch{
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
			getRollout: func() *v1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
					{
						Weight: utilpointer.Int32(10),
					},
					{
						Weight: utilpointer.Int32(30),
					},
					{
						Weight: utilpointer.Int32(100),
					},
				}
				return obj
			},
			getBatchRelease: func() *v1alpha1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1alpha1.ReleaseBatch{
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
			getRollout: func() *v1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.Strategy.Canary.Steps = []v1alpha1.CanaryStep{
					{
						Weight: utilpointer.Int32(2),
						Replicas: &intstr.IntOrString{
							Type:   intstr.String,
							StrVal: "10%",
						},
					},
					{
						Weight: utilpointer.Int32(3),
						Replicas: &intstr.IntOrString{
							Type:   intstr.String,
							StrVal: "10%",
						},
					},
				}
				return obj
			},
			getBatchRelease: func() *v1alpha1.BatchRelease {
				obj := batchDemo.DeepCopy()
				obj.Spec.ReleasePlan.Batches = []v1alpha1.ReleaseBatch{
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
			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			client.Create(context.TODO(), cs.getBatchRelease())
			dep, rs := cs.getObj()
			client.Create(context.TODO(), dep)
			client.Create(context.TODO(), rs)
			client.Create(context.TODO(), cs.getRollout())
			reconciler := &RolloutReconciler{
				Client: client,
				Scheme: scheme,
				finder: util.NewControllerFinder(client),
			}
			reconciler.canaryManager = &canaryReleaseManager{
				Client:                client,
				trafficRoutingManager: reconciler.trafficRoutingManager,
				recorder:              reconciler.Recorder,
			}
			rollout := cs.getRollout()
			workload, err := reconciler.finder.GetWorkloadForRef(rollout)
			if err != nil {
				t.Fatalf(err.Error())
			}
			c := &util.RolloutContext{Rollout: rollout, Workload: workload}
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
