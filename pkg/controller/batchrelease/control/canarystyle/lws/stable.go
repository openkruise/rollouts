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

package lws

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/util"
)

type realStableController struct {
	stableInfo   *util.WorkloadInfo
	stableObject *unstructured.Unstructured
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
	if rc.stableObject == nil {
		return nil
	}

	if control.IsControlledByBatchRelease(release, rc.stableObject) {
		return nil
	}

	owner := control.BuildReleaseControlInfo(release)
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":"%s"}}}`, util.BatchReleaseControlAnnotation, owner)

	lws := &unstructured.Unstructured{}
	lws.SetGroupVersionKind(util.ControllerLWSKind)
	lws.SetName(rc.stableKey.Name)
	lws.SetNamespace(rc.stableKey.Namespace)

	return rc.stableClient.Patch(context.TODO(), lws, client.RawPatch(types.StrategicMergePatchType, []byte(body)))
}

func (rc *realStableController) Finalize(release *v1beta1.BatchRelease) error {
	if rc.stableObject == nil {
		return nil // no need to process deleted object
	}

	// if batchPartition == nil, workload should be promoted
	pause := release.Spec.ReleasePlan.BatchPartition != nil
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}},"spec":{"paused":%v}}`,
		util.BatchReleaseControlAnnotation, pause)

	lws := &unstructured.Unstructured{}
	lws.SetGroupVersionKind(util.ControllerLWSKind)
	lws.SetName(rc.stableKey.Name)
	lws.SetNamespace(rc.stableKey.Namespace)

	if err := rc.stableClient.Patch(context.TODO(), lws, client.RawPatch(types.StrategicMergePatchType, []byte(body))); err != nil {
		return err
	}

	if control.ShouldWaitResume(release) {
		return waitAllUpdatedAndReady(lws)
	}
	return nil
}

func waitAllUpdatedAndReady(lws *unstructured.Unstructured) error {
	paused, found, _ := unstructured.NestedBool(lws.Object, "spec", "paused")
	if found && paused {
		return fmt.Errorf("promote error: LeaderWorkerSet should not be paused")
	}

	createdReplicas, _, _ := unstructured.NestedInt64(lws.Object, "status", "replicas")
	updatedReplicas, _, _ := unstructured.NestedInt64(lws.Object, "status", "updatedReplicas")
	if createdReplicas != updatedReplicas {
		return fmt.Errorf("promote error: all replicas should be upgraded")
	}

	availableReplicas, _, _ := unstructured.NestedInt64(lws.Object, "status", "readyReplicas")
	// For LWS, we use a simple check - all replicas should be ready
	// This can be enhanced based on LWS-specific requirements
	if availableReplicas < createdReplicas {
		return fmt.Errorf("promote error: all replicas should be ready")
	}
	return nil
}
