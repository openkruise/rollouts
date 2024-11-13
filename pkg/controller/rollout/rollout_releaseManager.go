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
	"context"
	"fmt"
	"reflect"

	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ReleaseManager interface {
	// execute the core logic of a release step by step
	runCanary(c *RolloutContext) error
	// check the NextStepIndex field in status, if modifies detected, jump to target step
	doCanaryJump(c *RolloutContext) bool
	// called when user accomplishes a release / does a rollback, or disables/removes the Rollout Resource
	doCanaryFinalising(c *RolloutContext) (bool, error)
	// create btach release
	createBatchRelease(rollout *v1beta1.Rollout, rolloutID string, batch int32, isRollback bool) *v1beta1.BatchRelease
	// retrun client
	fetchClient() client.Client
}

func fetchBatchRelease(cli client.Client, ns, name string) (*v1beta1.BatchRelease, error) {
	br := &v1beta1.BatchRelease{}
	// batchRelease.name is equal related rollout.name
	err := cli.Get(context.TODO(), client.ObjectKey{Namespace: ns, Name: name}, br)
	return br, err
}

func removeRolloutProgressingAnnotation(cli client.Client, c *RolloutContext) error {
	if c.Workload == nil {
		return nil
	}
	if _, ok := c.Workload.Annotations[util.InRolloutProgressingAnnotation]; !ok {
		return nil
	}
	workloadRef := c.Rollout.Spec.WorkloadRef
	workloadGVK := schema.FromAPIVersionAndKind(workloadRef.APIVersion, workloadRef.Kind)
	obj := util.GetEmptyWorkloadObject(workloadGVK)
	obj.SetNamespace(c.Workload.Namespace)
	obj.SetName(c.Workload.Name)
	body := fmt.Sprintf(`{"metadata":{"annotations":{"%s":null}}}`, util.InRolloutProgressingAnnotation)
	if err := cli.Patch(context.TODO(), obj, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
		klog.Errorf("rollout(%s/%s) patch workload(%s) failed: %s", c.Rollout.Namespace, c.Rollout.Name, c.Workload.Name, err.Error())
		return err
	}
	klog.Infof("remove rollout(%s/%s) workload(%s) annotation[%s] success", c.Rollout.Namespace, c.Rollout.Name, c.Workload.Name, util.InRolloutProgressingAnnotation)
	return nil
}

// bool means if we need retry; if error is not nil, always retry
func removeBatchRelease(cli client.Client, c *RolloutContext) (bool, error) {
	batch := &v1beta1.BatchRelease{}
	err := cli.Get(context.TODO(), client.ObjectKey{Namespace: c.Rollout.Namespace, Name: c.Rollout.Name}, batch)
	if err != nil && errors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		klog.Errorf("rollout(%s/%s) fetch BatchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name)
		return true, err
	}
	if !batch.DeletionTimestamp.IsZero() {
		klog.Infof("rollout(%s/%s) BatchRelease is terminating, and wait a moment", c.Rollout.Namespace, c.Rollout.Name)
		return true, nil
	}

	//delete batchRelease
	err = cli.Delete(context.TODO(), batch)
	if err != nil {
		klog.Errorf("rollout(%s/%s) delete BatchRelease failed: %s", c.Rollout.Namespace, c.Rollout.Name, err.Error())
		return true, err
	}
	klog.Infof("rollout(%s/%s) deleting BatchRelease, and wait a moment", c.Rollout.Namespace, c.Rollout.Name)
	return true, nil
}

// bool means if we need retry; if error is not nil, always retry
/*
	what does c.waitReady do:
	c.waitReady is true
		-> finalizingPolicy of batchRelease should be waitResume.
			-> br.Status.Phase will be RolloutPhaseCompleted until all pods of workload is upgraded.
				-> and then, this function return true.
	and vice versa.
	Note, c.waitReady field is true only when the finalizing reason is FinaliseReasonSuccess.
	And, finalizingPolicy field is respected only when it is canary-style.
*/
func finalizingBatchRelease(cli client.Client, c *RolloutContext) (bool, error) {
	br, err := fetchBatchRelease(cli, c.Rollout.Namespace, c.Rollout.Name)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return true, err
	}
	// The Completed phase means batchRelease controller has processed all it
	// should process. If BatchRelease phase is completed, we can do nothing.
	if br.Spec.ReleasePlan.BatchPartition == nil &&
		br.Status.Phase == v1beta1.RolloutPhaseCompleted {
		klog.Infof("rollout(%s/%s) finalizing batchRelease(%s) done", c.Rollout.Namespace, c.Rollout.Name, util.DumpJSON(br.Status))
		return false, nil
	}

	// If BatchPartition is already nil, and waitReady is also satisfied, do nothing
	if br.Spec.ReleasePlan.BatchPartition == nil {
		// if waitReady is true, and currentPolicy is already waitResume, do nothing
		// if waitReady is false, and currentPolicy is already immediate, do nothing
		if currentPolicy := br.Spec.ReleasePlan.FinalizingPolicy; (currentPolicy == v1beta1.WaitResumeFinalizingPolicyType) == c.WaitReady {
			return true, nil
		}
	}

	// Correct finalizing policy.
	policy := v1beta1.ImmediateFinalizingPolicyType
	if c.WaitReady {
		policy = v1beta1.WaitResumeFinalizingPolicyType
	}

	// Patch BatchPartition and FinalizingPolicy, BatchPartition always patch null here.
	body := fmt.Sprintf(`{"spec":{"releasePlan":{"batchPartition":null,"finalizingPolicy":"%s"}}}`, policy)
	if err = cli.Patch(context.TODO(), br, client.RawPatch(types.MergePatchType, []byte(body))); err != nil {
		return true, err
	}
	klog.Infof("rollout(%s/%s) patch batchRelease(%s) success", c.Rollout.Namespace, c.Rollout.Name, body)
	return true, nil
}

func runBatchRelease(m ReleaseManager, rollout *v1beta1.Rollout, rolloutId string, batch int32, isRollback bool) (bool, *v1beta1.BatchRelease, error) {
	cli := m.fetchClient()
	batch = batch - 1
	br, err := fetchBatchRelease(cli, rollout.Namespace, rollout.Name)
	if errors.IsNotFound(err) {
		// create new BatchRelease Crd
		br = m.createBatchRelease(rollout, rolloutId, batch, isRollback)
		if err = cli.Create(context.TODO(), br); err != nil && !errors.IsAlreadyExists(err) {
			klog.Errorf("rollout(%s/%s) create BatchRelease failed: %s", rollout.Namespace, rollout.Name, err.Error())
			return false, nil, err
		}
		klog.Infof("rollout(%s/%s) create BatchRelease(%s) success", rollout.Namespace, rollout.Name, util.DumpJSON(br))
		return false, br, nil
	} else if err != nil {
		klog.Errorf("rollout(%s/%s) fetch BatchRelease failed: %s", rollout.Namespace, rollout.Name, err.Error())
		return false, nil, err
	}

	// check whether batchRelease configuration is the latest
	newBr := m.createBatchRelease(rollout, rolloutId, batch, isRollback)
	if reflect.DeepEqual(br.Spec, newBr.Spec) && reflect.DeepEqual(br.Annotations, newBr.Annotations) {
		klog.Infof("rollout(%s/%s) do batchRelease batch(%d) success", rollout.Namespace, rollout.Name, batch+1)
		return true, br, nil
	}
	// update batchRelease to the latest version
	br.Spec = newBr.Spec
	br.Annotations = newBr.Annotations
	if err := cli.Update(context.TODO(), br); err != nil {
		klog.Errorf("rollout(%s/%s) update batchRelease failed: %s, requeue to retry", rollout.Namespace, rollout.Name, err.Error())
		return false, nil, err
	}
	klog.Infof("rollout(%s/%s) update batchRelease(%s) configuration to latest", rollout.Namespace, rollout.Name, util.DumpJSON(br))
	return false, br, nil
}
