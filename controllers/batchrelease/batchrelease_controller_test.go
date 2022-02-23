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

package batchrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	apimachineryruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const TIME_LAYOUT = "2006-01-02 15:04:05"

var (
	scheme        *runtime.Scheme
	releaseDeploy = &v1alpha1.BatchRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "BatchRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release",
			Namespace: "application",
			UID:       types.UID("87076677"),
		},
		Spec: v1alpha1.BatchReleaseSpec{
			TargetRef: v1alpha1.ObjectRef{
				WorkloadRef: &v1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "sample",
				},
			},
			ReleasePlan: v1alpha1.ReleasePlan{
				Batches: []v1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("10%"),
						PauseSeconds:   100,
					},
					{
						CanaryReplicas: intstr.FromString("50%"),
						PauseSeconds:   100,
					},
					{
						CanaryReplicas: intstr.FromString("80%"),
						PauseSeconds:   100,
					},
				},
			},
		},
	}

	stableDeploy = &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: apps.SchemeGroupVersion.String(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "sample",
			Namespace:  "application",
			UID:        types.UID("87076677"),
			Generation: 2,
			Labels: map[string]string{
				"app":                                "busybox",
				apps.DefaultDeploymentUniqueLabelKey: "update-pod-hash",
			},
		},
		Spec: apps.DeploymentSpec{
			Replicas: pointer.Int32Ptr(100),
			Strategy: apps.DeploymentStrategy{
				Type: apps.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: int32(1)},
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(2)},
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "busybox",
				},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: containers("v2"),
				},
			},
		},
		Status: apps.DeploymentStatus{
			Replicas:          100,
			ReadyReplicas:     100,
			UpdatedReplicas:   0,
			AvailableReplicas: 100,
		},
	}
)

var (
	releaseClone = &v1alpha1.BatchRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "BatchRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release",
			Namespace: "application",
			UID:       types.UID("87076677"),
		},
		Spec: v1alpha1.BatchReleaseSpec{
			TargetRef: v1alpha1.ObjectRef{
				WorkloadRef: &v1alpha1.WorkloadRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "sample",
				},
			},
			ReleasePlan: v1alpha1.ReleasePlan{
				Batches: []v1alpha1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("10%"),
						PauseSeconds:   100,
					},
					{
						CanaryReplicas: intstr.FromString("50%"),
						PauseSeconds:   100,
					},
					{
						CanaryReplicas: intstr.FromString("80%"),
						PauseSeconds:   100,
					},
				},
			},
		},
	}

	stableClone = &kruiseappsv1alpha1.CloneSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kruiseappsv1alpha1.SchemeGroupVersion.String(),
			Kind:       "CloneSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "sample",
			Namespace:  "application",
			UID:        types.UID("87076677"),
			Generation: 1,
			Labels: map[string]string{
				"app": "busybox",
			},
		},
		Spec: kruiseappsv1alpha1.CloneSetSpec{
			Replicas: pointer.Int32Ptr(100),
			UpdateStrategy: kruiseappsv1alpha1.CloneSetUpdateStrategy{
				Partition:      &intstr.IntOrString{Type: intstr.Int, IntVal: int32(1)},
				MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: int32(2)},
				MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: int32(2)},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "busybox",
				},
			},
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: containers("v2"),
				},
			},
		},
		Status: kruiseappsv1alpha1.CloneSetStatus{
			Replicas:             100,
			ReadyReplicas:        100,
			UpdatedReplicas:      0,
			UpdatedReadyReplicas: 0,
			ObservedGeneration:   1,
		},
	}
)

func init() {
	scheme = runtime.NewScheme()
	apimachineryruntime.Must(apps.AddToScheme(scheme))
	apimachineryruntime.Must(v1alpha1.AddToScheme(scheme))
	apimachineryruntime.Must(kruiseappsv1alpha1.AddToScheme(scheme))

	controlInfo, _ := json.Marshal(metav1.NewControllerRef(releaseDeploy, releaseDeploy.GroupVersionKind()))
	stableDeploy.Annotations = map[string]string{
		util.BatchReleaseControlAnnotation: string(controlInfo),
	}

	canaryTemplate := stableClone.Spec.Template.DeepCopy()
	stableTemplate := canaryTemplate.DeepCopy()
	stableTemplate.Spec.Containers = containers("v1")
	stableClone.Status.CurrentRevision = util.ComputeHash(stableTemplate, nil)
	stableClone.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
}

func TestReconcile_CloneSet(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name          string
		GetRelease    func() client.Object
		GetCloneSet   func() []client.Object
		ExpectedBatch int32
		ExpectedPhase v1alpha1.RolloutPhase
		ExpectedState v1alpha1.ReleasingBatchStateType
	}{
		// Following cases of Linear Transaction on State Machine
		{
			Name: "IfNeedProgress=false, Input-Phase=Initial, Output-Phase=Healthy",
			GetRelease: func() client.Object {
				return setPhase(releaseClone, v1alpha1.RolloutPhaseInitial)
			},
			GetCloneSet: func() []client.Object {
				clone := stableClone.DeepCopy()
				clone.Annotations = nil
				return []client.Object{
					clone,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseHealthy,
		},
		{
			Name: "IfNeedProgress=false, Input-Phase=Healthy, Output-Phase=Healthy",
			GetRelease: func() client.Object {
				return setPhase(releaseClone, v1alpha1.RolloutPhaseHealthy)
			},
			GetCloneSet: func() []client.Object {
				return []client.Object{
					stableClone.DeepCopy(),
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseHealthy,
		},
		{
			Name: "IfNeedProgress=true, Input-Phase=Healthy, Output-Phase=Preparing",
			GetRelease: func() client.Object {
				return setPhase(releaseClone, v1alpha1.RolloutPhaseHealthy)
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2")
				canary := getCanaryWithStage(stable, "v2", -1, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhasePreparing,
		},
		{
			Name: "Preparing, Input-Phase=Preparing, Output-Phase=Progressing",
			GetRelease: func() client.Object {
				release := setPhase(releaseClone, v1alpha1.RolloutPhasePreparing)
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2")
				canary := getCanaryWithStage(stable, "v2", -1, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
		},
		{
			Name: "Progressing, stage=0, Input-State=Initialize, Output-State=Verify",
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.InitializeBatchState)
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2")
				canary := getCanaryWithStage(stable, "v2", -1, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.VerifyBatchState,
		},
		{
			Name: "Progressing, stage=0, Input-State=DoCanary, Output-State=Verify",
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.DoCanaryBatchState)
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2")
				canary := getCanaryWithStage(stable, "v2", -1, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.VerifyBatchState,
		},
		{
			Name: "Progressing, stage=0, Input-State=Verify, Output-State=BatchReady",
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.VerifyBatchState)
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: "Progressing, stage=0->1, Input-State=BatchReady, Output-State=Initialize",
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = *getOldTime()
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.InitializeBatchState,
			ExpectedBatch: 1,
		},
		{
			Name: "Progressing, stage=0->1, Input-State=BatchReady, Output-State=BatchReady",
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: "Special Case: Scaling, Input-State=BatchReady, Output-State=Initialize",
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2").(*kruiseappsv1alpha1.CloneSet)
				stable.Spec.Replicas = pointer.Int32Ptr(200)
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.InitializeBatchState,
		},
		{
			Name: `Special Case: RollBack, Input-Phase=Progressing, Output-Phase=Abort`,
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v1")
				canary := getCanaryWithStage(stable, "v1", 0, true).(*kruiseappsv1alpha1.CloneSet)
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canary.Status.CurrentRevision = util.ComputeHash(stableTemplate, nil)
				canary.Status.UpdateRevision = util.ComputeHash(stableTemplate, nil)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseAbort,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: `Special Case: Deletion, Input-Phase=Progressing, Output-Phase=Terminating`,
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				release.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseTerminating,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: `Special Case: Cancelled, Input-Phase=Progressing, Output-Phase=Terminating`,
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				release.Spec.Cancelled = true
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseTerminating,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: `Special Case: Continuous Release, Input-Phase=Progressing, Output-Phase=Initial`,
			GetRelease: func() client.Object {
				release := setState(releaseClone, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetCloneSet: func() []client.Object {
				stable := getStableWithReady(stableClone, "v3")
				canary := getCanaryWithStage(stable, "v3", 0, true).(*kruiseappsv1alpha1.CloneSet)
				stableTemplate := stableClone.Spec.Template.DeepCopy()
				canaryTemplate := stableClone.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v3")
				canary.Status.CurrentRevision = util.ComputeHash(stableTemplate, nil)
				canary.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return []client.Object{
					canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseInitial,
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			release := cs.GetRelease()
			clonesets := cs.GetCloneSet()
			rec := record.NewFakeRecorder(100)
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release).
				WithObjects(clonesets...).Build()
			reconciler := &BatchReleaseReconciler{
				Client:   cli,
				recorder: rec,
				Scheme:   scheme,
				executor: NewReleasePlanExecutor(cli, rec),
			}

			key := client.ObjectKeyFromObject(release)
			request := reconcile.Request{NamespacedName: key}
			result, err := reconciler.Reconcile(context.TODO(), request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).Should(BeNumerically(">=", int64(0)))

			newRelease := v1alpha1.BatchRelease{}
			Expect(cli.Get(context.TODO(), key, &newRelease)).NotTo(HaveOccurred())
			Expect(newRelease.Status.Phase).Should(Equal(cs.ExpectedPhase))
			Expect(newRelease.Status.CanaryStatus.CurrentBatch).Should(Equal(cs.ExpectedBatch))
			Expect(newRelease.Status.CanaryStatus.CurrentBatchState).Should(Equal(cs.ExpectedState))
		})
	}
}

func TestReconcile_Deployment(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		Name           string
		GetRelease     func() client.Object
		GetDeployments func() []client.Object
		ExpectedBatch  int32
		ExpectedPhase  v1alpha1.RolloutPhase
		ExpectedState  v1alpha1.ReleasingBatchStateType
	}{
		// Following cases of Linear Transaction on State Machine
		{
			Name: "IfNeedProgress=false, Input-Phase=Initial, Output-Phase=Healthy",
			GetRelease: func() client.Object {
				return setPhase(releaseDeploy, v1alpha1.RolloutPhaseInitial)
			},
			GetDeployments: func() []client.Object {
				return []client.Object{
					stableDeploy.DeepCopy(),
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseHealthy,
		},
		{
			Name: "IfNeedProgress=false, Input-Phase=Healthy, Output-Phase=Healthy",
			GetRelease: func() client.Object {
				return setPhase(releaseDeploy, v1alpha1.RolloutPhaseHealthy)
			},
			GetDeployments: func() []client.Object {
				return []client.Object{
					stableDeploy.DeepCopy(),
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseHealthy,
		},
		{
			Name: "IfNeedProgress=true, Input-Phase=Healthy, Output-Phase=Preparing",
			GetRelease: func() client.Object {
				return setPhase(releaseDeploy, v1alpha1.RolloutPhaseHealthy)
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				canary := getCanaryWithStage(stable, "v2", -1, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhasePreparing,
		},
		{
			Name: "Preparing, Input-Phase=Preparing, Output-Phase=Progressing",
			GetRelease: func() client.Object {
				return setPhase(releaseDeploy, v1alpha1.RolloutPhasePreparing)
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2")
				canary := getCanaryWithStage(stable, "v2", -1, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
		},
		{
			Name: "Progressing, stage=0, Input-State=Initialize, Output-State=Verify",
			GetRelease: func() client.Object {
				return setState(releaseDeploy, v1alpha1.InitializeBatchState)
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2")
				canary := getCanaryWithStage(stable, "v2", -1, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.VerifyBatchState,
		},
		{
			Name: "Progressing, stage=0, Input-State=DoCanary, Output-State=Verify",
			GetRelease: func() client.Object {
				return setState(releaseDeploy, v1alpha1.DoCanaryBatchState)
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2")
				canary := getCanaryWithStage(stable, "v2", -1, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.VerifyBatchState,
		},
		{
			Name: "Progressing, stage=0, Input-State=Verify, Output-State=BatchReady",
			GetRelease: func() client.Object {
				return setState(releaseDeploy, v1alpha1.VerifyBatchState)
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: "Progressing, stage=0->1, Input-State=BatchReady, Output-State=Initialize",
			GetRelease: func() client.Object {
				release := setState(releaseDeploy, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = *getOldTime()
				return release
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.InitializeBatchState,
			ExpectedBatch: 1,
		},
		{
			Name: "Progressing, stage=0->1, Input-State=BatchReady, Output-State=BatchReady",
			GetRelease: func() client.Object {
				release := setState(releaseDeploy, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				return release
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: "Special Case: Scaling, Input-State=BatchReady, Output-State=Initialize",
			GetRelease: func() client.Object {
				release := setState(releaseDeploy, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				return release
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2").(*apps.Deployment)
				stable.Spec.Replicas = pointer.Int32Ptr(200)
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseProgressing,
			ExpectedState: v1alpha1.InitializeBatchState,
		},
		{
			Name: `Special Case: RollBack, Input-Phase=Progressing, Output-Phase=Abort`,
			GetRelease: func() client.Object {
				release := setState(releaseDeploy, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableDeploy.Spec.Template.DeepCopy()
				canaryTemplate := stableDeploy.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v1")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseAbort,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: `Special Case: Deletion, Input-Phase=Progressing, Output-Phase=Terminating`,
			GetRelease: func() client.Object {
				release := setState(releaseDeploy, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableDeploy.Spec.Template.DeepCopy()
				canaryTemplate := stableDeploy.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				release.DeletionTimestamp = &metav1.Time{Time: time.Now()}
				return release
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseTerminating,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: `Special Case: Cancelled, Input-Phase=Progressing, Output-Phase=Terminating`,
			GetRelease: func() client.Object {
				release := setState(releaseDeploy, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableDeploy.Spec.Template.DeepCopy()
				canaryTemplate := stableDeploy.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				release.Spec.Cancelled = true
				return release
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v2")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseTerminating,
			ExpectedState: v1alpha1.ReadyBatchState,
		},
		{
			Name: `Special Case: Continuous Release, Input-Phase=Progressing, Output-Phase=Initial`,
			GetRelease: func() client.Object {
				release := setState(releaseDeploy, v1alpha1.ReadyBatchState)
				release.Status.CanaryStatus.BatchReadyTime = metav1.Now()
				stableTemplate := stableDeploy.Spec.Template.DeepCopy()
				canaryTemplate := stableDeploy.Spec.Template.DeepCopy()
				stableTemplate.Spec.Containers = containers("v1")
				canaryTemplate.Spec.Containers = containers("v2")
				release.Status.StableRevision = util.ComputeHash(stableTemplate, nil)
				release.Status.UpdateRevision = util.ComputeHash(canaryTemplate, nil)
				return release
			},
			GetDeployments: func() []client.Object {
				stable := getStableWithReady(stableDeploy, "v3")
				canary := getCanaryWithStage(stable, "v2", 0, true)
				return []client.Object{
					stable, canary,
				}
			},
			ExpectedPhase: v1alpha1.RolloutPhaseInitial,
		},
	}

	for _, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			release := cs.GetRelease()
			deployments := cs.GetDeployments()
			rec := record.NewFakeRecorder(100)
			cliBuilder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release).
				WithObjects(deployments...)

			if len(deployments) > 0 {
				cliBuilder = cliBuilder.WithObjects(makeStableReplicaSets(deployments[0])...)
			}

			if len(deployments) > 1 {
				cliBuilder = cliBuilder.WithObjects(makeCanaryReplicaSets(deployments[1:]...)...)
			}

			cli := cliBuilder.Build()
			reconciler := &BatchReleaseReconciler{
				Client:   cli,
				recorder: rec,
				Scheme:   scheme,
				executor: NewReleasePlanExecutor(cli, rec),
			}

			key := client.ObjectKeyFromObject(release)
			request := reconcile.Request{NamespacedName: key}
			result, err := reconciler.Reconcile(context.TODO(), request)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).Should(BeNumerically(">=", int64(0)))

			newRelease := v1alpha1.BatchRelease{}
			Expect(cli.Get(context.TODO(), key, &newRelease)).NotTo(HaveOccurred())
			Expect(newRelease.Status.Phase).Should(Equal(cs.ExpectedPhase))
			Expect(newRelease.Status.CanaryStatus.CurrentBatch).Should(Equal(cs.ExpectedBatch))
			Expect(newRelease.Status.CanaryStatus.CurrentBatchState).Should(Equal(cs.ExpectedState))
		})
	}
}

func containers(version string) []corev1.Container {
	return []corev1.Container{
		{
			Name:  "busybox",
			Image: fmt.Sprintf("busybox:%v", version),
		},
	}
}

func setPhase(release *v1alpha1.BatchRelease, phase v1alpha1.RolloutPhase) *v1alpha1.BatchRelease {
	r := release.DeepCopy()
	r.Status.Phase = phase
	switch phase {
	case v1alpha1.RolloutPhaseInitial, v1alpha1.RolloutPhaseHealthy:
	default:
		r.Status.ObservedWorkloadReplicas = 100
		r.Status.ObservedReleasePlanHash = hashReleasePlanBatches(&release.Spec.ReleasePlan)
	}
	return r
}

func setState(release *v1alpha1.BatchRelease, state v1alpha1.ReleasingBatchStateType) *v1alpha1.BatchRelease {
	r := release.DeepCopy()
	r.Status.Phase = v1alpha1.RolloutPhaseProgressing
	r.Status.CanaryStatus.CurrentBatchState = state
	r.Status.ObservedWorkloadReplicas = 100
	r.Status.ObservedReleasePlanHash = hashReleasePlanBatches(&release.Spec.ReleasePlan)
	return r
}

func getStableWithReady(workload client.Object, version string) client.Object {
	switch workload.(type) {
	case *apps.Deployment:
		deploy := workload.(*apps.Deployment)
		d := deploy.DeepCopy()
		d.Spec.Paused = true
		d.ResourceVersion = strconv.Itoa(rand.Intn(100000000000))
		d.Spec.Template.Spec.Containers = containers(version)
		d.Status.ObservedGeneration = deploy.Generation
		return d

	case *kruiseappsv1alpha1.CloneSet:
		clone := workload.(*kruiseappsv1alpha1.CloneSet)
		c := clone.DeepCopy()
		c.ResourceVersion = strconv.Itoa(rand.Intn(100000000000))
		c.Spec.UpdateStrategy.Paused = true
		c.Spec.UpdateStrategy.Partition = &intstr.IntOrString{Type: intstr.String, StrVal: "100%"}
		c.Spec.Template.Spec.Containers = containers(version)
		c.Status.ObservedGeneration = clone.Generation
		return c
	}
	return nil
}

func getCanaryWithStage(workload client.Object, version string, stage int, ready bool) client.Object {
	var err error
	var stageReplicas int

	switch workload.(type) {
	case *apps.Deployment:
		deploy := workload.(*apps.Deployment)

		if stage >= 0 {
			stageReplicas, err = intstr.GetScaledValueFromIntOrPercent(
				&releaseDeploy.Spec.ReleasePlan.Batches[stage].CanaryReplicas, int(*deploy.Spec.Replicas), true)
			Expect(err).NotTo(HaveOccurred())
			if !ready {
				stageReplicas -= 5
			}
		}

		d := deploy.DeepCopy()
		d.Name += "-canary-324785678"
		d.UID = uuid.NewUUID()
		d.Spec.Paused = false
		d.ResourceVersion = strconv.Itoa(rand.Intn(100000000000))
		d.Labels[util.CanaryDeploymentLabelKey] = "87076677"
		d.Finalizers = []string{util.CanaryDeploymentFinalizer}
		d.Spec.Replicas = pointer.Int32Ptr(int32(stageReplicas))
		d.Spec.Template.Spec.Containers = containers(version)
		d.Status.Replicas = int32(stageReplicas)
		d.Status.ReadyReplicas = int32(stageReplicas)
		d.Status.UpdatedReplicas = int32(stageReplicas)
		d.Status.AvailableReplicas = int32(stageReplicas)
		d.Status.ObservedGeneration = deploy.Generation
		d.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(releaseDeploy, releaseDeploy.GroupVersionKind())}
		controlInfo, _ := json.Marshal(metav1.NewControllerRef(releaseDeploy, releaseDeploy.GroupVersionKind()))
		d.Annotations = map[string]string{
			util.BatchReleaseControlAnnotation: string(controlInfo),
		}
		return d

	case *kruiseappsv1alpha1.CloneSet:
		clone := workload.(*kruiseappsv1alpha1.CloneSet)

		if stage >= 0 {
			stageReplicas, err = intstr.GetScaledValueFromIntOrPercent(
				&releaseClone.Spec.ReleasePlan.Batches[stage].CanaryReplicas, int(*clone.Spec.Replicas), true)
			Expect(err).NotTo(HaveOccurred())
			if !ready {
				stageReplicas -= 5
			}
		}

		c := clone
		c.ResourceVersion = strconv.Itoa(rand.Intn(100000000000))
		c.Spec.UpdateStrategy.Paused = false
		c.Spec.UpdateStrategy.Partition = &intstr.IntOrString{Type: intstr.Int, IntVal: *c.Spec.Replicas - int32(stageReplicas)}
		c.Spec.Template.Spec.Containers = containers(version)
		c.Status.Replicas = *c.Spec.Replicas
		c.Status.UpdatedReplicas = int32(stageReplicas)
		c.Status.UpdatedReadyReplicas = int32(stageReplicas)
		c.Status.ReadyReplicas = c.Status.Replicas
		c.Status.ObservedGeneration = clone.Generation
		controlInfo, _ := json.Marshal(metav1.NewControllerRef(releaseClone, releaseClone.GroupVersionKind()))
		c.Annotations = map[string]string{
			util.BatchReleaseControlAnnotation: string(controlInfo),
		}
		return c
	}

	return nil
}

func makeCanaryReplicaSets(deploys ...client.Object) []client.Object {
	var rss []client.Object
	for _, d := range deploys {
		deploy := d.(*apps.Deployment)
		labels := deploy.Spec.Selector.DeepCopy().MatchLabels
		labels[apps.DefaultDeploymentUniqueLabelKey] = util.ComputeHash(&deploy.Spec.Template, nil)
		rss = append(rss, &apps.ReplicaSet{
			TypeMeta: metav1.TypeMeta{
				APIVersion: apps.SchemeGroupVersion.String(),
				Kind:       "ReplicaSet",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploy.Name + rand.String(5),
				Namespace: deploy.Namespace,
				UID:       uuid.NewUUID(),
				Labels:    labels,
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(deploy, deploy.GroupVersionKind()),
				},
			},
			Spec: apps.ReplicaSetSpec{
				Replicas: deploy.Spec.Replicas,
				Selector: deploy.Spec.Selector.DeepCopy(),
				Template: *deploy.Spec.Template.DeepCopy(),
			},
		})
	}
	return rss
}

func makeStableReplicaSets(deploys ...client.Object) []client.Object {
	var rss []client.Object
	stableTemplate := stableDeploy.Spec.Template.DeepCopy()
	stableTemplate.Spec.Containers = containers("v1")
	for _, d := range deploys {
		deploy := d.(*apps.Deployment)
		labels := deploy.Spec.Selector.DeepCopy().MatchLabels
		labels[apps.DefaultDeploymentUniqueLabelKey] = util.ComputeHash(stableTemplate, nil)
		rss = append(rss, &apps.ReplicaSet{
			TypeMeta: metav1.TypeMeta{
				APIVersion: apps.SchemeGroupVersion.String(),
				Kind:       "ReplicaSet",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploy.Name + rand.String(5),
				Namespace: deploy.Namespace,
				UID:       uuid.NewUUID(),
				Labels:    labels,
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(deploy, deploy.GroupVersionKind()),
				},
			},
			Spec: apps.ReplicaSetSpec{
				Replicas: deploy.Spec.Replicas,
				Selector: deploy.Spec.Selector.DeepCopy(),
				Template: *stableTemplate,
			},
		})
	}
	return rss
}

func getOldTime() *metav1.Time {
	time, _ := time.Parse(TIME_LAYOUT, "2018-09-10 00:00:00")
	return &metav1.Time{Time: time}
}
