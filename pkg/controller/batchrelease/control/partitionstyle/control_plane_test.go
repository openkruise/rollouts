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

package partitionstyle

import (
	"context"
	"errors"
	"testing"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	controlpkg "github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/util"
)

type fakePartitionController struct {
	buildResult Interface
	buildErr    error
	minReady    bool

	workloadInfo *util.WorkloadInfo
	pods         []*corev1.Pod
	listErr      error
	batchCtx     *batchcontext.BatchContext
	calcErr      error

	initErr      error
	upgradeErr   error
	finalizeErr  error
	reconcileErr error

	buildCalls     int
	initCalls      int
	upgradeCalls   int
	finalizeCalls  int
	reconcileCalls int
	calculateCalls int
	listCalls      int

	statusWriter *MinReadyStatusWriter
}

func (f *fakePartitionController) BuildController() (Interface, error) {
	f.buildCalls++
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	if f.buildResult != nil {
		return f.buildResult, nil
	}
	return f, nil
}

func (f *fakePartitionController) BindMinReadyStatus(release *v1beta1.BatchRelease, status *v1beta1.BatchReleaseStatus, recorder record.EventRecorder) {
	if f.minReady {
		f.statusWriter = NewMinReadyStatusWriter(release, status, recorder)
	}
}

func (f *fakePartitionController) RecordOperationFailed(reason string, err error) {
	if f.statusWriter != nil {
		f.statusWriter.RecordDegraded(reason, err)
	}
}

func (f *fakePartitionController) RecordZeroReplicaBatching() {
	if f.statusWriter != nil {
		f.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatching", "MinReadySeconds strategy has no replicas to upgrade")
	}
}

func (f *fakePartitionController) RecordBatchAdvanced() {
	if f.statusWriter != nil {
		f.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatching", "MinReadySeconds strategy advanced the current batch")
	}
}

func (f *fakePartitionController) RecordZeroReplicaBatchReady() {
	if f.statusWriter != nil {
		f.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")
	}
}

func (f *fakePartitionController) RecordBatchReady() {
	if f.statusWriter != nil {
		f.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyBatching, "MinReadyBatchReady", "MinReadySeconds strategy batch is ready")
	}
}

func (f *fakePartitionController) RecordInitialized() {
	if f.statusWriter != nil {
		f.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyInitialized, "MinReadyInitialized", "MinReadySeconds strategy initialized")
	}
}

func (f *fakePartitionController) RecordFinalized() {
	if f.statusWriter != nil {
		f.statusWriter.RecordNormal(v1beta1.RolloutConditionMinReadyFinalized, "MinReadyFinalized", "MinReadySeconds strategy finalized")
	}
}

func (f *fakePartitionController) ObserveBatchWait() {
	if f.statusWriter == nil {
		return
	}
	status := f.statusWriter.BatchReleaseStatus()
	if status == nil {
		return
	}
	condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyBatching)
	ObserveMinReadyBatchWait(f.statusWriter.BatchRelease(), condition)
}

func (f *fakePartitionController) GetWorkloadInfo() *util.WorkloadInfo {
	if f.workloadInfo != nil {
		return f.workloadInfo
	}
	return testWorkloadInfo(3, 1, "stable", "update")
}

func (f *fakePartitionController) ListOwnedPods() ([]*corev1.Pod, error) {
	f.listCalls++
	return f.pods, f.listErr
}

func (f *fakePartitionController) CalculateBatchContext(release *v1beta1.BatchRelease) (*batchcontext.BatchContext, error) {
	f.calculateCalls++
	if f.calcErr != nil {
		return nil, f.calcErr
	}
	if f.batchCtx != nil {
		return f.batchCtx, nil
	}
	return readyBatchContext(), nil
}

func (f *fakePartitionController) Initialize(ctx context.Context, release *v1beta1.BatchRelease) error {
	f.initCalls++
	return f.initErr
}

func (f *fakePartitionController) UpgradeBatch(context.Context, *batchcontext.BatchContext) error {
	f.upgradeCalls++
	return f.upgradeErr
}

func (f *fakePartitionController) ReconcileMaxUnavailableDrift(context.Context, *batchcontext.BatchContext) error {
	f.reconcileCalls++
	return f.reconcileErr
}

func (f *fakePartitionController) Finalize(context.Context, *v1beta1.BatchRelease) error {
	f.finalizeCalls++
	return f.finalizeErr
}

func (f *fakePartitionController) IsMinReadyControl() bool {
	return f.minReady
}

type fakeBatchLabelPatcher struct {
	calls int
	err   error
}

func (f *fakeBatchLabelPatcher) PatchPodBatchLabel(*batchcontext.BatchContext) error {
	f.calls++
	return f.err
}

func TestNewControlPlaneCopiesReleaseAndNormalizesContext(t *testing.T) {
	release := testBatchRelease()
	status := &v1beta1.BatchReleaseStatus{}
	controller := &fakePartitionController{}
	rc := NewControlPlane(nil, func(client.Client, types.NamespacedName, schema.GroupVersionKind) Interface {
		return controller
	}, fake.NewClientBuilder().Build(), record.NewFakeRecorder(10), release, status, types.NamespacedName{}, schema.GroupVersionKind{})

	if rc.ctx == nil {
		t.Fatalf("ctx is nil, want background context")
	}
	if rc.release == release {
		t.Fatalf("release was not deep-copied")
	}
	release.Name = "changed"
	if rc.release.Name == "changed" {
		t.Fatalf("release mutation leaked into control plane copy")
	}

	ctx := context.WithValue(context.Background(), struct{}{}, "value")
	if nonNilContext(ctx) != ctx {
		t.Fatalf("nonNilContext did not preserve non-nil context")
	}
}

func TestControlPlaneInitializeRecordsMinReadyWorkloadInfo(t *testing.T) {
	controller := &fakePartitionController{
		minReady:     true,
		workloadInfo: testWorkloadInfo(5, 2, "stable", "update"),
	}
	status := &v1beta1.BatchReleaseStatus{}
	rc := newTestControlPlane(controller, status)

	if err := rc.Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if controller.initCalls != 1 {
		t.Fatalf("initCalls = %d, want 1", controller.initCalls)
	}
	if status.StableRevision != "stable" || status.UpdateRevision != "update" || status.ObservedWorkloadReplicas != 5 {
		t.Fatalf("status revisions/replicas not updated: %#v", status)
	}
	condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyInitialized)
	if condition == nil || condition.Reason != "MinReadyInitialized" {
		t.Fatalf("MinReadyInitialized condition = %#v", condition)
	}
}

func TestControlPlaneUpgradeBatchMinReadyPaths(t *testing.T) {
	t.Run("no replicas records ready without upgrading", func(t *testing.T) {
		controller := &fakePartitionController{
			minReady:     true,
			workloadInfo: testWorkloadInfo(0, 0, "stable", "update"),
		}
		status := &v1beta1.BatchReleaseStatus{}
		rc := newTestControlPlane(controller, status)

		if err := rc.UpgradeBatch(); err != nil {
			t.Fatalf("UpgradeBatch() error = %v", err)
		}
		if controller.upgradeCalls != 0 {
			t.Fatalf("upgradeCalls = %d, want 0", controller.upgradeCalls)
		}
		condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyBatching)
		if condition == nil || condition.Reason != "MinReadyBatching" {
			t.Fatalf("MinReadyBatching condition = %#v", condition)
		}
	})

	t.Run("successful upgrade patches labels and records normal condition", func(t *testing.T) {
		controller := &fakePartitionController{minReady: true}
		patcher := &fakeBatchLabelPatcher{}
		status := &v1beta1.BatchReleaseStatus{}
		rc := newTestControlPlane(controller, status)
		rc.patcher = patcher

		if err := rc.UpgradeBatch(); err != nil {
			t.Fatalf("UpgradeBatch() error = %v", err)
		}
		if controller.calculateCalls != 1 || controller.upgradeCalls != 1 || patcher.calls != 1 {
			t.Fatalf("calls calculate=%d upgrade=%d patch=%d, want 1/1/1", controller.calculateCalls, controller.upgradeCalls, patcher.calls)
		}
		condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyBatching)
		if condition == nil || condition.Reason != "MinReadyBatching" {
			t.Fatalf("MinReadyBatching condition = %#v", condition)
		}
	})

	t.Run("calculate error records degraded condition", func(t *testing.T) {
		controller := &fakePartitionController{
			minReady: true,
			calcErr:  errors.Join(errors.New("strategy drift"), ErrMinReadyDriftDetected),
		}
		status := &v1beta1.BatchReleaseStatus{}
		rc := newTestControlPlane(controller, status)

		if err := rc.UpgradeBatch(); err == nil {
			t.Fatalf("UpgradeBatch() error = nil, want error")
		}
		condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyDegraded)
		if condition == nil || condition.Reason != "MinReadyDegradedDriftDetected" {
			t.Fatalf("MinReadyDegraded condition = %#v", condition)
		}
		if status.Message == "" {
			t.Fatalf("status.Message is empty, want degraded error")
		}
	})
}

func TestControlPlaneEnsureBatchPodsReadyAndLabeled(t *testing.T) {
	t.Run("not ready returns readiness error", func(t *testing.T) {
		controller := &fakePartitionController{
			minReady: true,
			batchCtx: &batchcontext.BatchContext{
				Replicas:               3,
				DesiredUpdatedReplicas: 3,
				UpdatedReplicas:        1,
			},
		}
		now := metav1.Now()
		status := &v1beta1.BatchReleaseStatus{
			Conditions: []v1beta1.RolloutCondition{{
				Type:               v1beta1.RolloutConditionMinReadyBatching,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: now,
			}},
		}
		rc := newTestControlPlane(controller, status)

		if err := rc.EnsureBatchPodsReadyAndLabeled(); err == nil {
			t.Fatalf("EnsureBatchPodsReadyAndLabeled() error = nil, want not ready error")
		}
		if controller.calculateCalls != 1 {
			t.Fatalf("calculateCalls = %d, want 1", controller.calculateCalls)
		}
		if controller.reconcileCalls != 1 {
			t.Fatalf("reconcileCalls = %d, want 1", controller.reconcileCalls)
		}
		if degraded := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyDegraded); degraded != nil {
			t.Fatalf("MinReadyDegraded condition = %#v, want nil for normal batch wait", degraded)
		}
	})

	t.Run("ready records batch ready", func(t *testing.T) {
		controller := &fakePartitionController{minReady: true}
		status := &v1beta1.BatchReleaseStatus{}
		rc := newTestControlPlane(controller, status)

		if err := rc.EnsureBatchPodsReadyAndLabeled(); err != nil {
			t.Fatalf("EnsureBatchPodsReadyAndLabeled() error = %v", err)
		}
		condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyBatching)
		if condition == nil || condition.Reason != "MinReadyBatchReady" {
			t.Fatalf("MinReadyBatchReady condition = %#v", condition)
		}
	})

	t.Run("drift reconcile error records degraded condition", func(t *testing.T) {
		controller := &fakePartitionController{
			minReady:     true,
			reconcileErr: errors.Join(errors.New("window drift"), ErrMinReadyDriftDetected),
		}
		status := &v1beta1.BatchReleaseStatus{}
		rc := newTestControlPlane(controller, status)

		if err := rc.EnsureBatchPodsReadyAndLabeled(); err == nil {
			t.Fatalf("EnsureBatchPodsReadyAndLabeled() error = nil, want drift error")
		}
		if controller.reconcileCalls != 1 {
			t.Fatalf("reconcileCalls = %d, want 1", controller.reconcileCalls)
		}
		condition := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyDegraded)
		if condition == nil || condition.Reason != "MinReadyDegradedDriftDetected" {
			t.Fatalf("MinReadyDegraded condition = %#v", condition)
		}
	})
}

func TestControlPlaneFinalizeMinReadyPaths(t *testing.T) {
	t.Run("not found is ignored", func(t *testing.T) {
		controller := &fakePartitionController{
			buildErr: apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, "missing"),
		}
		rc := newTestControlPlane(controller, &v1beta1.BatchReleaseStatus{})

		if err := rc.Finalize(); err != nil {
			t.Fatalf("Finalize() error = %v", err)
		}
	})

	t.Run("successful minReady finalize clears degraded condition", func(t *testing.T) {
		controller := &fakePartitionController{minReady: true}
		status := &v1beta1.BatchReleaseStatus{
			Message: "previous degraded",
			Conditions: []v1beta1.RolloutCondition{{
				Type:   v1beta1.RolloutConditionMinReadyDegraded,
				Status: corev1.ConditionTrue,
				Reason: "MinReadyDegradedDriftDetected",
			}},
		}
		rc := newTestControlPlane(controller, status)

		if err := rc.Finalize(); err != nil {
			t.Fatalf("Finalize() error = %v", err)
		}
		if controller.finalizeCalls != 1 {
			t.Fatalf("finalizeCalls = %d, want 1", controller.finalizeCalls)
		}
		finalized := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyFinalized)
		if finalized == nil || finalized.Reason != "MinReadyFinalized" {
			t.Fatalf("MinReadyFinalized condition = %#v", finalized)
		}
		degraded := util.GetBatchReleaseCondition(*status, v1beta1.RolloutConditionMinReadyDegraded)
		if degraded == nil || degraded.Status != corev1.ConditionFalse {
			t.Fatalf("MinReadyDegraded condition = %#v, want false", degraded)
		}
		if status.Message != "" {
			t.Fatalf("status.Message = %q, want empty", status.Message)
		}
	})
}

func TestControlPlaneSyncWorkloadInformationStates(t *testing.T) {
	tests := []struct {
		name       string
		release    func() *v1beta1.BatchRelease
		controller *fakePartitionController
		wantEvent  controlpkg.WorkloadEventType
		wantErr    bool
	}{
		{
			name: "deleted release is ignored",
			release: func() *v1beta1.BatchRelease {
				release := testBatchRelease()
				now := metav1.Now()
				release.DeletionTimestamp = &now
				return release
			},
			controller: &fakePartitionController{},
			wantEvent:  controlpkg.WorkloadNormalState,
		},
		{
			name: "workload gone",
			controller: &fakePartitionController{
				buildErr: apierrors.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, "missing"),
			},
			wantEvent: controlpkg.WorkloadHasGone,
			wantErr:   true,
		},
		{
			name:       "build error",
			controller: &fakePartitionController{buildErr: errors.New("build failed")},
			wantEvent:  controlpkg.WorkloadUnknownState,
			wantErr:    true,
		},
		{
			name: "still reconciling",
			controller: &fakePartitionController{workloadInfo: &util.WorkloadInfo{
				LogKey: "workload",
				ObjectMeta: metav1.ObjectMeta{
					Generation: 2,
				},
				Replicas: 5,
				Status: util.WorkloadStatus{
					Replicas:           5,
					UpdatedReplicas:    2,
					ObservedGeneration: 1,
					StableRevision:     "stable",
					UpdateRevision:     "update",
				},
			}},
			wantEvent: controlpkg.WorkloadStillReconciling,
		},
		{
			name:       "promoted",
			controller: &fakePartitionController{workloadInfo: testWorkloadInfo(5, 5, "stable", "update")},
			wantEvent:  controlpkg.WorkloadNormalState,
		},
		{
			name:       "scaling",
			controller: &fakePartitionController{workloadInfo: testWorkloadInfo(6, 2, "stable", "update")},
			wantEvent:  controlpkg.WorkloadReplicasChanged,
		},
		{
			name:       "rollback",
			controller: &fakePartitionController{workloadInfo: testWorkloadInfo(5, 2, "stable", "stable")},
			wantEvent:  controlpkg.WorkloadRollbackInBatch,
		},
		{
			name:       "revision changed",
			controller: &fakePartitionController{workloadInfo: testWorkloadInfo(5, 2, "stable", "other")},
			wantEvent:  controlpkg.WorkloadPodTemplateChanged,
		},
		{
			name:       "normal",
			controller: &fakePartitionController{workloadInfo: testWorkloadInfo(5, 2, "stable", "update")},
			wantEvent:  controlpkg.WorkloadNormalState,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &v1beta1.BatchReleaseStatus{
				StableRevision:           "stable",
				UpdateRevision:           "update",
				ObservedWorkloadReplicas: 5,
			}
			rc := newTestControlPlane(tt.controller, status)
			if tt.release != nil {
				rc.release = tt.release()
			}

			got, info, err := rc.SyncWorkloadInformation()
			if (err != nil) != tt.wantErr {
				t.Fatalf("SyncWorkloadInformation() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.wantEvent {
				t.Fatalf("event = %s, want %s", got, tt.wantEvent)
			}
			if tt.name == "deleted release is ignored" && info != nil {
				t.Fatalf("info = %#v, want nil for deleted release", info)
			}
		})
	}
}

func TestControlPlaneNoNeedUpdateReplicaHelpers(t *testing.T) {
	t.Run("rollback without rollout id returns current updated replicas", func(t *testing.T) {
		controller := &fakePartitionController{}
		status := &v1beta1.BatchReleaseStatus{
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{UpdatedReplicas: 2},
		}
		rc := newTestControlPlane(controller, status)
		rc.release.Annotations = map[string]string{v1alpha1.RollbackInBatchAnnotation: "true"}

		got, err := rc.markNoNeedUpdatePodsIfNeeds()
		if err != nil {
			t.Fatalf("markNoNeedUpdatePodsIfNeeds() error = %v", err)
		}
		if got == nil || *got != 2 {
			t.Fatalf("noNeedUpdateReplicas = %v, want 2", got)
		}
	})

	t.Run("count refreshes status from matching pods", func(t *testing.T) {
		noNeed := int32(0)
		controller := &fakePartitionController{
			pods: []*corev1.Pod{
				testPod("matched", map[string]string{
					apps.ControllerRevisionHashLabelKey: "hash",
					util.NoNeedUpdatePodLabel:           "rollout-1",
				}),
				testPod("different-rollout", map[string]string{
					apps.ControllerRevisionHashLabelKey: "hash",
					util.NoNeedUpdatePodLabel:           "rollout-2",
				}),
				testPod("different-revision", map[string]string{
					apps.ControllerRevisionHashLabelKey: "old",
					util.NoNeedUpdatePodLabel:           "rollout-1",
				}),
			},
		}
		status := &v1beta1.BatchReleaseStatus{
			UpdateRevision: "hash",
			CanaryStatus:   v1beta1.BatchReleaseCanaryStatus{NoNeedUpdateReplicas: &noNeed},
		}
		rc := newTestControlPlane(controller, status)
		rc.release.Spec.ReleasePlan.RolloutID = "rollout-1"
		rc.release.Status.UpdateRevision = "hash"
		rc.release.Status.CanaryStatus.NoNeedUpdateReplicas = &noNeed

		if err := rc.countAndUpdateNoNeedUpdateReplicas(); err != nil {
			t.Fatalf("countAndUpdateNoNeedUpdateReplicas() error = %v", err)
		}
		if *status.CanaryStatus.NoNeedUpdateReplicas != 1 {
			t.Fatalf("status noNeedUpdateReplicas = %d, want 1", *status.CanaryStatus.NoNeedUpdateReplicas)
		}
		if *rc.release.Status.CanaryStatus.NoNeedUpdateReplicas != 1 {
			t.Fatalf("release noNeedUpdateReplicas = %d, want 1", *rc.release.Status.CanaryStatus.NoNeedUpdateReplicas)
		}
	})
}

func newTestControlPlane(controller *fakePartitionController, status *v1beta1.BatchReleaseStatus) *realBatchControlPlane {
	return &realBatchControlPlane{
		Interface:     controller,
		Client:        fake.NewClientBuilder().Build(),
		EventRecorder: record.NewFakeRecorder(20),
		patcher:       &fakeBatchLabelPatcher{},
		ctx:           context.Background(),
		release:       testBatchRelease(),
		newStatus:     status,
	}
}

func testBatchRelease() *v1beta1.BatchRelease {
	return &v1beta1.BatchRelease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "release",
		},
		Spec: v1beta1.BatchReleaseSpec{
			ReleasePlan: v1beta1.ReleasePlan{
				Batches: []v1beta1.ReleaseBatch{
					{CanaryReplicas: intstr.FromInt(1)},
					{CanaryReplicas: intstr.FromInt(3)},
				},
			},
		},
	}
}

func testWorkloadInfo(replicas, updatedReplicas int32, stableRevision, updateRevision string) *util.WorkloadInfo {
	return &util.WorkloadInfo{
		LogKey: "workload",
		ObjectMeta: metav1.ObjectMeta{
			Generation: 1,
		},
		Replicas: replicas,
		Status: util.WorkloadStatus{
			Replicas:           replicas,
			UpdatedReplicas:    updatedReplicas,
			ObservedGeneration: 1,
			StableRevision:     stableRevision,
			UpdateRevision:     updateRevision,
		},
	}
}

func readyBatchContext() *batchcontext.BatchContext {
	return &batchcontext.BatchContext{
		Replicas:               3,
		CurrentBatch:           0,
		UpdatedReplicas:        1,
		UpdatedReadyReplicas:   1,
		PlannedUpdatedReplicas: 1,
		DesiredUpdatedReplicas: 1,
		DesiredPartition:       intstr.FromInt(2),
		CurrentPartition:       intstr.FromInt(3),
		NoNeedUpdatedReplicas:  nil,
		FailureThreshold:       nil,
		Pods:                   nil,
		UpdateRevision:         "update",
		RolloutID:              "",
		DesiredSurge:           intstr.FromInt(0),
		CurrentSurge:           intstr.FromInt(0),
	}
}

func testPod(name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      name,
			Labels:    labels,
		},
	}
}
