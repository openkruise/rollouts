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
	"time"

	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/controllers/rollout/batchrelease"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type rolloutContext struct {
	client.Client

	rollout *appsv1alpha1.Rollout

	newStatus *appsv1alpha1.RolloutStatus

	isComplete bool

	stableService *corev1.Service

	canaryService *corev1.Service

	workload *util.Workload

	batchControl batchrelease.BatchController

	recheckTime *time.Time
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
			expectedTime := time.Now().Add(5 * time.Second)
			r.recheckTime = &expectedTime
		}
		return done, err
	}
	return false, nil
}
