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
	"fmt"
	"time"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/rollout/batchrelease"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type rolloutContext struct {
	client.Client

	rollout *rolloutv1alpha1.Rollout

	newStatus *rolloutv1alpha1.RolloutStatus

	isComplete bool

	stableService string

	canaryService string

	workload *util.Workload

	batchControl batchrelease.BatchRelease

	recheckTime *time.Time

	recorder record.EventRecorder
}

func newRolloutContext(client client.Client, recorder record.EventRecorder, rollout *rolloutv1alpha1.Rollout, newStatus *rolloutv1alpha1.RolloutStatus, workload *util.Workload) *rolloutContext {
	rolloutCon := &rolloutContext{
		Client:       client,
		rollout:      rollout,
		newStatus:    newStatus,
		batchControl: batchrelease.NewInnerBatchController(client, rollout),
		workload:     workload,
		recorder:     recorder,
	}
	if len(rolloutCon.rollout.Spec.Strategy.Canary.TrafficRoutings) > 0 {
		rolloutCon.stableService = rolloutCon.rollout.Spec.Strategy.Canary.TrafficRoutings[0].Service
		rolloutCon.canaryService = fmt.Sprintf("%s-canary", rolloutCon.stableService)
	}
	return rolloutCon
}

func (r *rolloutContext) reconcile() error {
	// canary strategy
	if r.rollout.Spec.Strategy.Canary != nil {
		klog.Infof("rollout(%s/%s) run Canary action...", r.rollout.Namespace, r.rollout.Name)
		return r.runCanary()
	}
	return nil
}

func (r *rolloutContext) finalising() (bool, error) {
	// canary strategy
	if r.rollout.Spec.Strategy.Canary != nil {
		done, err := r.doCanaryFinalising()
		if err == nil && !done {
			// The finalizer is not finished, wait one second
			expectedTime := time.Now().Add(time.Duration(defaultGracePeriodSeconds) * time.Second)
			r.recheckTime = &expectedTime
		}
		return done, err
	}
	return false, nil
}

func (r *rolloutContext) podRevisionLabelKey() string {
	if r.workload == nil {
		return ""
	}
	return r.workload.RevisionLabelKey
}

//isAllowRun return next allow run time
func (r *rolloutContext) isAllowRun(expectedTime time.Time) (time.Time, bool) {
	return util.TimeInSlice(expectedTime, r.rollout.Spec.TimeSlices)
}
