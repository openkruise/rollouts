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
	"encoding/json"
	"fmt"
	"sort"

	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	expectations "github.com/openkruise/rollouts/pkg/util/expectation"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type realCanaryController struct {
	canaryInfo   *util.WorkloadInfo
	canaryObject *apps.Deployment
	canaryClient client.Client
	objectKey    types.NamespacedName
	canaryPods   []*corev1.Pod
}

func newCanary(cli client.Client, key types.NamespacedName) realCanaryController {
	return realCanaryController{canaryClient: cli, objectKey: key}
}

func (r *realCanaryController) GetCanaryInfo() *util.WorkloadInfo {
	return r.canaryInfo
}

// Delete do not delete canary deployments actually, it only removes the finalizers of
// Deployments. These deployments will be cascaded deleted when BatchRelease is deleted.
func (r *realCanaryController) Delete(release *v1beta1.BatchRelease) error {
	deployments, err := r.listDeployment(release, client.InNamespace(r.objectKey.Namespace), utilclient.DisableDeepCopy)
	if err != nil {
		return err
	}

	for _, d := range deployments {
		if !controllerutil.ContainsFinalizer(d, util.CanaryDeploymentFinalizer) {
			continue
		}
		err = util.UpdateFinalizer(r.canaryClient, d, util.RemoveFinalizerOpType, util.CanaryDeploymentFinalizer)
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		klog.Infof("Successfully remove finalizers for Deplot %v", klog.KObj(d))
	}
	return nil
}

func (r *realCanaryController) UpgradeBatch(ctx *batchcontext.BatchContext) error {
	// desired replicas for canary deployment
	desired := ctx.DesiredUpdatedReplicas
	deployment := util.GetEmptyObjectWithKey(r.canaryObject)

	if r.canaryInfo.Replicas >= desired {
		return nil
	}

	body := fmt.Sprintf(`{"spec":{"replicas":%d}}`, desired)
	return r.canaryClient.Patch(context.TODO(), deployment, client.RawPatch(types.StrategicMergePatchType, []byte(body)))
}

func (r *realCanaryController) Create(release *v1beta1.BatchRelease) error {
	if r.canaryObject != nil {
		return nil // Don't re-create if exists
	}

	// check expectation before creating canary deployment to avoid
	// repeatedly create multiple canary deployment incorrectly.
	controllerKey := client.ObjectKeyFromObject(release).String()
	satisfied, timeoutDuration, rest := expectations.ResourceExpectations.SatisfiedExpectations(controllerKey)
	if !satisfied {
		if timeoutDuration >= expectations.ExpectationTimeout {
			klog.Warningf("Unsatisfied time of expectation exceeds %v, delete key and continue, key: %v, rest: %v",
				expectations.ExpectationTimeout, klog.KObj(release), rest)
			expectations.ResourceExpectations.DeleteExpectations(controllerKey)
		} else {
			return fmt.Errorf("expectation is not satisfied, key: %v, rest: %v", klog.KObj(release), rest)
		}
	}

	// fetch the stable deployment as template to create canary deployment.
	stable := &apps.Deployment{}
	if err := r.canaryClient.Get(context.TODO(), r.objectKey, stable); err != nil {
		return err
	}
	return r.create(release, stable)
}
func (r *realCanaryController) create(release *v1beta1.BatchRelease, template *apps.Deployment) error {
	canary := &apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%v-", r.objectKey.Name),
			Namespace:    r.objectKey.Namespace,
			Labels:       map[string]string{},
			Annotations:  map[string]string{},
		},
	}

	// metadata
	canary.Finalizers = append(canary.Finalizers, util.CanaryDeploymentFinalizer)
	canary.OwnerReferences = append(canary.OwnerReferences, *metav1.NewControllerRef(release, release.GroupVersionKind()))
	canary.Labels[util.CanaryDeploymentLabel] = template.Name
	ownerInfo, _ := json.Marshal(metav1.NewControllerRef(release, release.GroupVersionKind()))
	canary.Annotations[util.BatchReleaseControlAnnotation] = string(ownerInfo)

	// spec
	canary.Spec = *template.Spec.DeepCopy()
	// patch canary pod metadata
	if release.Spec.ReleasePlan.PatchPodTemplateMetadata != nil {
		patch := release.Spec.ReleasePlan.PatchPodTemplateMetadata
		for k, v := range patch.Labels {
			canary.Spec.Template.Labels[k] = v
		}
		if canary.Spec.Template.Annotations == nil {
			canary.Spec.Template.Annotations = map[string]string{}
		}
		for k, v := range patch.Annotations {
			canary.Spec.Template.Annotations[k] = v
		}
	}
	canary.Spec.Replicas = pointer.Int32(0)
	canary.Spec.Paused = false

	if err := r.canaryClient.Create(context.TODO(), canary); err != nil {
		klog.Errorf("Failed to create canary Deployment(%v), error: %v", klog.KObj(canary), err)
		return err
	}

	// add expect to avoid to create repeatedly
	controllerKey := client.ObjectKeyFromObject(release).String()
	expectations.ResourceExpectations.Expect(controllerKey, expectations.Create, string(canary.UID))

	canaryInfo, _ := json.Marshal(canary)
	klog.Infof("Create canary Deployment(%v) successfully, details: %s", klog.KObj(canary), string(canaryInfo))
	return fmt.Errorf("created canary deployment %v succeeded, but waiting informer synced", klog.KObj(canary))
}

func (r *realCanaryController) listDeployment(release *v1beta1.BatchRelease, options ...client.ListOption) ([]*apps.Deployment, error) {
	dList := &apps.DeploymentList{}
	if err := r.canaryClient.List(context.TODO(), dList, options...); err != nil {
		return nil, err
	}

	var ds []*apps.Deployment
	for i := range dList.Items {
		d := &dList.Items[i]
		o := metav1.GetControllerOf(d)
		if o == nil || o.UID != release.UID {
			continue
		}
		ds = append(ds, d)
	}
	return ds, nil
}

// return the latest deployment with the newer creation time
func filterCanaryDeployment(release *v1beta1.BatchRelease, ds []*apps.Deployment, template *corev1.PodTemplateSpec) *apps.Deployment {
	if len(ds) == 0 {
		return nil
	}
	sort.Slice(ds, func(i, j int) bool {
		return ds[i].CreationTimestamp.After(ds[j].CreationTimestamp.Time)
	})
	if template == nil {
		return ds[0]
	}
	var ignoreLabels, ignoreAnno = make([]string, 0), make([]string, 0)
	if release.Spec.ReleasePlan.PatchPodTemplateMetadata != nil {
		patch := release.Spec.ReleasePlan.PatchPodTemplateMetadata
		for k := range patch.Labels {
			ignoreLabels = append(ignoreLabels, k)
		}
		for k := range patch.Annotations {
			ignoreAnno = append(ignoreAnno, k)
		}
	}
	for _, d := range ds {
		if util.EqualIgnoreSpecifyMetadata(template, &d.Spec.Template, ignoreLabels, ignoreAnno) {
			return d
		}
	}
	return nil
}
