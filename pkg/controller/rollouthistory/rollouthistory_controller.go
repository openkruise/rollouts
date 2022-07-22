/*
Copyright 2022.

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

package rollouthistory

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
)

// RolloutHistoryReconciler reconciles a RolloutHistory object
type RolloutHistoryReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=rollouthistories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=rollouthistories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=rollouts.kruise.io,resources=rollouthistories/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RolloutHistory object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *RolloutHistoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	// get rollout
	rollout := &rolloutv1alpha1.Rollout{}
	err := r.Get(ctx, req.NamespacedName, rollout)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// get RolloutHistory which is not completed and related to the rollout (only one or zero)
	var rollouthistory *rolloutv1alpha1.RolloutHistory
	if rollouthistory, err = r.getRHByRollout(rollout); err != nil {
		// if not find RolloutHistory related this rollout, skip some situations
		if rollout.Status.CanaryStatus == nil || rollout.Spec.Strategy.Canary == nil || rollout.Spec.ObjectRef.WorkloadRef == nil || rollout.Spec.RolloutID == "" || rollout.Status.CanaryStatus.CurrentStepIndex != 0 {
			return ctrl.Result{}, nil
		}
		// create a rollouthistory when user do a new rollout
		if rollouthistory, err = r.createRHByRollout(rollout); err != nil {
			return ctrl.Result{}, err
		}
	}

	// handle rollouthistory according to rollouthistory.status.canaryStepState
	switch rollouthistory.Status.CanaryStepState {
	case rolloutv1alpha1.CanaryStateInit:
		err = r.handleInit(rollout, rollouthistory)
	case rolloutv1alpha1.CanaryStatePending:
		err = r.handlePending(rollout, rollouthistory)
	case rolloutv1alpha1.CanaryStateUpdated:
		err = r.handleUpdated(rollout, rollouthistory)
	case rolloutv1alpha1.CanaryStateTerminated:
		err = r.handleTerminated(rollout, rollouthistory)
	case rolloutv1alpha1.CanaryStateCompleted:
		err = r.handleCompleted(rollout, rollouthistory)
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RolloutHistoryReconciler) SetupWithManager(mgr ctrl.Manager) error {

	return ctrl.NewControllerManagedBy(mgr).
		For(&rolloutv1alpha1.Rollout{}).
		Complete(r)

}
