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

package deployment

import (
	"context"
	"fmt"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type realStableController struct {
	stableInfo   *util.WorkloadInfo
	stableObject *apps.Deployment
	stableClient client.Client
	stableKey    types.NamespacedName
}

func newStable(cli client.Client, key types.NamespacedName) realStableController {
	return realStableController{stableClient: cli, stableKey: key}
}

func (rc *realStableController) GetStableInfo() *util.WorkloadInfo {
	return rc.stableInfo
}

func (rc *realStableController) Initialize(release *v1beta1.BatchRelease) error {
	if control.IsControlledByBatchRelease(release, rc.stableObject) {
		return nil
	}

	d := util.GetEmptyObjectWithKey(rc.stableObject)
	owner := control.BuildReleaseControlInfo(release)

	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, util.BatchReleaseControlAnnotation, owner)
	return rc.stableClient.Patch(context.TODO(), d, client.RawPatch(types.StrategicMergePatchType, []byte(body)))
}

func (rc *realStableController) Finalize(release *v1beta1.BatchRelease) error {
	if rc.stableObject == nil {
		return nil // no need to process deleted object
	}

	// if batchPartition == nil, workload should be promoted;
	pause := release.Spec.ReleasePlan.BatchPartition != nil
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}},"spec":{"paused":%v}}`,
		util.BatchReleaseControlAnnotation, pause)

	d := util.GetEmptyObjectWithKey(rc.stableObject)
	if err := rc.stableClient.Patch(context.TODO(), d, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
		return err
	}
	if control.ShouldWaitResume(release) {
		return waitAllUpdatedAndReady(d.(*apps.Deployment))
	}
	return nil
}

func waitAllUpdatedAndReady(deployment *apps.Deployment) error {
	if deployment.Spec.Paused {
		return fmt.Errorf("promote error: deployment should not be paused")
	}

	createdReplicas := deployment.Status.Replicas
	updatedReplicas := deployment.Status.UpdatedReplicas
	if createdReplicas != updatedReplicas {
		return fmt.Errorf("promote error: all replicas should be upgraded")
	}

	availableReplicas := deployment.Status.AvailableReplicas
	allowedUnavailable := util.DeploymentMaxUnavailable(deployment)
	if allowedUnavailable+availableReplicas < createdReplicas {
		return fmt.Errorf("promote error: ready replicas should satisfy maxUnavailable")
	}
	return nil
}
