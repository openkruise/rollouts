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

	"github.com/openkruise/rollouts/api/v1alpha1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/canarystyle"
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type realController struct {
	realStableController
	realCanaryController
}

func NewController(cli client.Client, key types.NamespacedName) canarystyle.Interface {
	return &realController{
		realStableController: newStable(cli, key),
		realCanaryController: newCanary(cli, key),
	}
}

func (rc *realController) BuildStableController() (canarystyle.StableInterface, error) {
	if rc.stableObject != nil {
		return rc, nil
	}

	object := &apps.Deployment{}
	err := rc.stableClient.Get(context.TODO(), rc.stableKey, object)
	if err != nil {
		return rc, err
	}
	rc.stableObject = object
	rc.stableInfo = util.ParseWorkload(object)
	return rc, nil
}

func (rc *realController) BuildCanaryController(release *v1alpha1.BatchRelease) (canarystyle.CanaryInterface, error) {
	if rc.canaryObject != nil {
		return rc, nil
	}

	ds, err := rc.listDeployment(release, client.InNamespace(rc.stableKey.Namespace), utilclient.DisableDeepCopy)
	if err != nil {
		return rc, err
	}

	template, err := rc.getLatestTemplate()
	if client.IgnoreNotFound(err) != nil {
		return rc, err
	}

	rc.canaryObject = filterCanaryDeployment(util.FilterActiveDeployment(ds), template)
	if rc.canaryObject == nil {
		return rc, control.GenerateNotFoundError(fmt.Sprintf("%v-canary", rc.stableKey), "Deployment")
	}

	rc.canaryInfo = util.ParseWorkload(rc.canaryObject)
	return rc, nil
}

func (rc *realController) CalculateBatchContext(release *v1alpha1.BatchRelease) *batchcontext.BatchContext {
	replicas := *rc.stableObject.Spec.Replicas
	currentBatch := release.Status.CanaryStatus.CurrentBatch
	desiredUpdate := int32(control.CalculateBatchReplicas(release, int(replicas), int(currentBatch)))

	return &batchcontext.BatchContext{
		Replicas:               replicas,
		CurrentBatch:           currentBatch,
		DesiredUpdatedReplicas: desiredUpdate,
		FailureThreshold:       release.Spec.ReleasePlan.FailureThreshold,
		UpdatedReplicas:        rc.canaryObject.Status.Replicas,
		UpdatedReadyReplicas:   rc.canaryObject.Status.AvailableReplicas,
	}
}

func (rc *realController) getLatestTemplate() (*v1.PodTemplateSpec, error) {
	_, err := rc.BuildStableController()
	if err != nil {
		return nil, err
	}
	return &rc.stableObject.Spec.Template, nil
}
