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

package labelpatch

import (
	"context"
	"fmt"

	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type LabelPatcher interface {
	PatchPodBatchLabel(ctx *batchcontext.BatchContext) error
}

func NewLabelPatcher(cli client.Client, logKey klog.ObjectRef) *realPatcher {
	return &realPatcher{Client: cli, logKey: logKey}
}

type realPatcher struct {
	client.Client
	logKey klog.ObjectRef
}

func (r *realPatcher) PatchPodBatchLabel(ctx *batchcontext.BatchContext) error {
	if ctx.RolloutID == "" || len(ctx.Pods) == 0 {
		return nil
	}
	pods := ctx.Pods
	if ctx.FilterFunc != nil {
		pods = ctx.FilterFunc(pods, ctx)
	}
	return r.patchPodBatchLabel(pods, ctx)
}

// PatchPodBatchLabel will patch rollout-id && batch-id to pods
func (r *realPatcher) patchPodBatchLabel(pods []*corev1.Pod, ctx *batchcontext.BatchContext) error {
	// the number of active pods that has been patched successfully.
	patchedUpdatedReplicas := int32(0)
	// the number of target active pods that should be patched batch label.
	plannedUpdatedReplicas := ctx.PlannedUpdatedReplicas

	for _, pod := range pods {
		if !util.IsConsistentWithRevision(pod, ctx.UpdateRevision) {
			continue
		}

		podRolloutID := pod.Labels[util.RolloutIDLabel]
		if pod.DeletionTimestamp.IsZero() && podRolloutID == ctx.RolloutID {
			patchedUpdatedReplicas++
		}
	}

	// all pods that should be patched have been patched
	if patchedUpdatedReplicas >= plannedUpdatedReplicas {
		return nil // return fast
	}

	for _, pod := range pods {
		if pod.DeletionTimestamp.IsZero() {
			// we don't patch label for the active old revision pod
			if !util.IsConsistentWithRevision(pod, ctx.UpdateRevision) {
				continue
			}
			// we don't continue to patch if the goal is met
			if patchedUpdatedReplicas >= ctx.PlannedUpdatedReplicas {
				continue
			}
		}

		// if it has been patched, just ignore
		if pod.Labels[util.RolloutIDLabel] == ctx.RolloutID {
			continue
		}

		clone := util.GetEmptyObjectWithKey(pod)
		by := fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s","%s":"%d"}}}`,
			util.RolloutIDLabel, ctx.RolloutID, util.RolloutBatchIDLabel, ctx.CurrentBatch+1)
		if err := r.Patch(context.TODO(), clone, client.RawPatch(types.StrategicMergePatchType, []byte(by))); err != nil {
			return err
		}

		if pod.DeletionTimestamp.IsZero() {
			patchedUpdatedReplicas++
		}
		klog.Infof("Successfully patch Pod(%v) batchID %d label", klog.KObj(pod), ctx.CurrentBatch+1)
	}

	if patchedUpdatedReplicas >= plannedUpdatedReplicas {
		return nil
	}
	return fmt.Errorf("patched %v pods for %v, however the goal is %d", patchedUpdatedReplicas, r.logKey, plannedUpdatedReplicas)
}
