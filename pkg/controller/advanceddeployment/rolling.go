/*
Copyright 2016 The Kubernetes Authors.

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

package advanceddeployment

import (
	"context"
	"fmt"
	"sort"

	"github.com/openkruise/rollouts/pkg/controller/advanceddeployment/util"

	apps "k8s.io/api/apps/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/integer"
)

// rolloutRolling implements the logic for rolling a new replica set.
func (dc *DeploymentController) rolloutRolling(ctx context.Context, d *apps.Deployment, rsList []*apps.ReplicaSet) (bool, error) {
	newRS, oldRSs, err := dc.getAllReplicaSetsAndSyncRevision(d, rsList, true)
	if err != nil {
		return false, err
	}
	allRSs := append(oldRSs, newRS)

	// Scale up, if we can.
	scaledUp, err := dc.reconcileNewReplicaSet(ctx, allRSs, newRS, d)
	if err != nil {
		return false, err
	}
	if scaledUp {
		// Update DeploymentStatus
		klog.V(4).Infof("Scaling up new replicaset for deployment %v", klog.KObj(d))
		return false, dc.syncRolloutStatus(ctx, allRSs, newRS, d)
	}

	// Scale down, if we can.
	scaledDown, err := dc.reconcileOldReplicaSets(ctx, allRSs, util.FilterActiveReplicaSets(oldRSs), newRS, d)
	if err != nil {
		return false, err
	}
	if scaledDown {
		// Update DeploymentStatus
		klog.V(4).Infof("Scaling down old replicaset for deployment %v", klog.KObj(d))
		return false, dc.syncRolloutStatus(ctx, allRSs, newRS, d)
	}

	done := false
	if util.DeploymentRolloutBatchComplete(d, util.NewRSReplicasLimit(dc.strategy.Partition, d)) {
		klog.Infof("upgraded done for deployment %v", klog.KObj(d))
		done = true
	}

	// Sync deployment status
	return done, dc.syncRolloutStatus(ctx, allRSs, newRS, d)
}

// reconcileNewReplicaSet is different from native deployment: consider partition limitation.
func (dc *DeploymentController) reconcileNewReplicaSet(ctx context.Context, allRSs []*apps.ReplicaSet, newRS *apps.ReplicaSet, deployment *apps.Deployment) (bool, error) {
	if *(newRS.Spec.Replicas) == *(deployment.Spec.Replicas) {
		// Scaling not required.
		return false, nil
	}
	if *(newRS.Spec.Replicas) > *(deployment.Spec.Replicas) {
		// Scale down.
		scaled, _, err := dc.scaleReplicaSetAndRecordEvent(ctx, newRS, *(deployment.Spec.Replicas), deployment)
		return scaled, err
	}
	updateLimit := util.NewRSReplicasLimit(dc.strategy.Partition, deployment)
	if *(newRS.Spec.Replicas) >= updateLimit {
		// Scaling not required.
		return false, nil
	}
	newReplicasCount, err := util.NewRSNewReplicas(deployment, &dc.strategy, allRSs, newRS)
	if err != nil {
		return false, err
	}
	// Do not exceed the number of update limitation.
	newReplicasCount = integer.Int32Max(integer.Int32Min(newReplicasCount, updateLimit), 0)
	// Scale up.
	scaled, _, err := dc.scaleReplicaSetAndRecordEvent(ctx, newRS, newReplicasCount, deployment)
	return scaled, err
}

// reconcileOldReplicaSets is different from native deployment: consider partition limitation.
func (dc *DeploymentController) reconcileOldReplicaSets(ctx context.Context, allRSs []*apps.ReplicaSet, oldRSs []*apps.ReplicaSet, newRS *apps.ReplicaSet, deployment *apps.Deployment) (bool, error) {
	oldPodsCount := util.GetReplicaCountForReplicaSets(oldRSs)
	if oldPodsCount == 0 {
		// Can't scale down further
		return false, nil
	}

	allPodsCount := util.GetReplicaCountForReplicaSets(allRSs)
	klog.V(4).Infof("New replica set %s/%s has %d available pods.", newRS.Namespace, newRS.Name, newRS.Status.AvailableReplicas)
	maxUnavailable := util.MaxUnavailable(*deployment, &dc.strategy)

	// Check if we can scale down. We can scale down in the following 2 cases:
	// * Some old replica sets have unhealthy replicas, we could safely scale down those unhealthy replicas since that won't further
	//  increase unavailability.
	// * New replica set has scaled up and it's replicas becomes ready, then we can scale down old replica sets in a further step.
	//
	// maxScaledDown := allPodsCount - minAvailable - newReplicaSetPodsUnavailable
	// take into account not only maxUnavailable and any surge pods that have been created, but also unavailable pods from
	// the newRS, so that the unavailable pods from the newRS would not make us scale down old replica sets in a further
	// step(that will increase unavailability).
	//
	// Concrete example:
	//
	// * 10 replicas
	// * 2 maxUnavailable (absolute number, not percent)
	// * 3 maxSurge (absolute number, not percent)
	//
	// case 1:
	// * Deployment is updated, newRS is created with 3 replicas, oldRS is scaled down to 8, and newRS is scaled up to 5.
	// * The new replica set pods crashloop and never become available.
	// * allPodsCount is 13. minAvailable is 8. newRSPodsUnavailable is 5.
	// * A node fails and causes one of the oldRS pods to become unavailable. However, 13 - 8 - 5 = 0, so the oldRS won't be scaled down.
	// * The user notices the crashloop and does kubectl rollout undo to rollback.
	// * newRSPodsUnavailable is 1, since we rolled back to the good replica set, so maxScaledDown = 13 - 8 - 1 = 4. 4 of the crashlooping pods will be scaled down.
	// * The total number of pods will then be 9 and the newRS can be scaled up to 10.
	//
	// case 2:
	// Same example, but pushing a new pod template instead of rolling back (aka "roll over"):
	// * The new replica set created must start with 0 replicas because allPodsCount is already at 13.
	// * However, newRSPodsUnavailable would also be 0, so the 2 old replica sets could be scaled down by 5 (13 - 8 - 0), which would then
	// allow the new replica set to be scaled up by 5.

	newRSUpdateLimit := util.NewRSReplicasLimit(dc.strategy.Partition, deployment)
	// newRSDesiredCount is the expected replicas of the new replica set under the partition settings.
	newRSDesiredCount := integer.Int32Max(newRSUpdateLimit, *newRS.Spec.Replicas)
	// oldRSDesiredCount is the expected total replicas of the all old replica sets.
	oldRSDesiredCount := *(deployment.Spec.Replicas) - newRSDesiredCount
	// oldRSDesiredDiff is the gap between the reality and the desired.
	maxScaledDownDiff := oldPodsCount - oldRSDesiredCount
	if maxScaledDownDiff <= 0 {
		// Old replica sets do not satisfied as expectation, we scale them up.
		return dc.scaleUpOldReplicaSets(ctx, oldRSs, -maxScaledDownDiff, deployment)
	}

	// conditions for satisfying MaxUnavailable
	minAvailable := *(deployment.Spec.Replicas) - maxUnavailable
	// newRSUnavailablePodCount is the real number of unavailable pods owned by new replica set.
	newRSUnavailablePodCount := *(newRS.Spec.Replicas) - newRS.Status.AvailableReplicas
	// maxScaledDown describe how many pods of old replica sets can be scaled down at this moment.
	maxScaledDown := allPodsCount - minAvailable - newRSUnavailablePodCount
	// Do not exceed the number of the desired pods of old replicas
	maxScaledDown = integer.Int32Min(maxScaledDown, maxScaledDownDiff)
	if maxScaledDown <= 0 {
		// Do have any quota to scale down.
		return false, nil
	}

	klog.InfoS("When Scaling Old ReplicaSets Down:",
		// About the new replica set
		"Replicas(New)", *(newRS.Spec.Replicas),
		"UnavailableReplicas(New)", newRSUnavailablePodCount,
		// About the old replica sets
		"ReplicaS(Old)", oldPodsCount,
		"DesiredReplicas(Old)", oldRSDesiredCount,
		"maxScaledDown(Old)", maxScaledDown,
		// About the deployment
		"Replicas(Deployment)", *(deployment.Spec.Replicas),
		"MaxUnavailable(Deployment)", maxUnavailable,
		"Partition(Deployment)", newRSUpdateLimit)

	// Clean up unhealthy replicas first, otherwise unhealthy replicas will block deployment
	// and cause timeout. See https://github.com/kubernetes/kubernetes/issues/16737
	oldRSs, cleanupCount, err := dc.cleanupUnhealthyReplicas(ctx, oldRSs, deployment, maxScaledDown)
	if err != nil {
		return false, err
	}
	klog.V(4).Infof("Cleaned up unhealthy replicas from old RSes by %d", cleanupCount)

	// Scale down old replica sets, need check maxUnavailable to ensure we can scale down
	allRSs = append(oldRSs, newRS)
	scaledDownCount, err := dc.scaleDownOldReplicaSetsForRollingUpdate(ctx, allRSs, oldRSs, deployment)
	if err != nil {
		return false, err
	}
	klog.V(4).Infof("Scaled down old RSes of deployment %s by %d", deployment.Name, scaledDownCount)

	totalScaledDown := cleanupCount + scaledDownCount
	return totalScaledDown > 0, nil
}

// scaleUpOldReplicaSets is different from native deployment: consider partition limitation.
func (dc *DeploymentController) scaleUpOldReplicaSets(ctx context.Context, oldRSs []*apps.ReplicaSet, scaledUpCount int32, deployment *apps.Deployment) (bool, error) {
	if scaledUpCount <= 0 || len(oldRSs) == 0 {
		return false, nil
	}

	stableRS := util.FindActiveOrStable(dc.findStableReplicaSet(oldRSs), oldRSs)
	if stableRS == nil {
		klog.Warningf("Cannot find any replica set for deployment %v", klog.KObj(deployment))
		return false, nil
	}

	newScale := (*stableRS.Spec.Replicas) + scaledUpCount
	scaled, _, err := dc.scaleReplicaSetAndRecordEvent(ctx, stableRS, newScale, deployment)
	return scaled, err
}

func (dc *DeploymentController) findStableReplicaSet(rss []*apps.ReplicaSet) *apps.ReplicaSet {
	for _, rs := range rss {
		if util.IsConsistentWithRevision(rs, dc.strategy.StableRevision) {
			return rs.DeepCopy()
		}
	}
	return nil
}

// cleanupUnhealthyReplicas will scale down old replica sets with unhealthy replicas, so that all unhealthy replicas will be deleted.
func (dc *DeploymentController) cleanupUnhealthyReplicas(ctx context.Context, oldRSs []*apps.ReplicaSet, deployment *apps.Deployment, maxCleanupCount int32) ([]*apps.ReplicaSet, int32, error) {
	sort.Sort(util.ReplicaSetsByCreationTimestamp(oldRSs))
	// Safely scale down all old replica sets with unhealthy replicas. Replica set will sort the pods in the order
	// such that not-ready < ready, unscheduled < scheduled, and pending < running. This ensures that unhealthy replicas will
	// been deleted first and won't increase unavailability.
	totalScaledDown := int32(0)
	for i, targetRS := range oldRSs {
		if totalScaledDown >= maxCleanupCount {
			break
		}
		if *(targetRS.Spec.Replicas) == 0 {
			// cannot scale down this replica set.
			continue
		}
		klog.V(4).Infof("Found %d available pods in old RS %s/%s", targetRS.Status.AvailableReplicas, targetRS.Namespace, targetRS.Name)
		if *(targetRS.Spec.Replicas) == targetRS.Status.AvailableReplicas {
			// no unhealthy replicas found, no scaling required.
			continue
		}

		scaledDownCount := int32(integer.IntMin(int(maxCleanupCount-totalScaledDown), int(*(targetRS.Spec.Replicas)-targetRS.Status.AvailableReplicas)))
		newReplicasCount := *(targetRS.Spec.Replicas) - scaledDownCount
		if newReplicasCount > *(targetRS.Spec.Replicas) {
			return nil, 0, fmt.Errorf("when cleaning up unhealthy replicas, got invalid request to scale down %s/%s %d -> %d", targetRS.Namespace, targetRS.Name, *(targetRS.Spec.Replicas), newReplicasCount)
		}
		_, updatedOldRS, err := dc.scaleReplicaSetAndRecordEvent(ctx, targetRS, newReplicasCount, deployment)
		if err != nil {
			return nil, totalScaledDown, err
		}
		totalScaledDown += scaledDownCount
		oldRSs[i] = updatedOldRS
	}
	return oldRSs, totalScaledDown, nil
}

// scaleDownOldReplicaSetsForRollingUpdate scales down old replica sets when deployment strategy is "RollingUpdate".
// Need check maxUnavailable to ensure availability
func (dc *DeploymentController) scaleDownOldReplicaSetsForRollingUpdate(ctx context.Context, allRSs []*apps.ReplicaSet, oldRSs []*apps.ReplicaSet, deployment *apps.Deployment) (int32, error) {
	maxUnavailable := util.MaxUnavailable(*deployment, &dc.strategy)

	// Check if we can scale down.
	minAvailable := *(deployment.Spec.Replicas) - maxUnavailable
	// Find the number of available pods.
	availablePodCount := util.GetAvailableReplicaCountForReplicaSets(allRSs)
	if availablePodCount <= minAvailable {
		// Cannot scale down.
		return 0, nil
	}
	klog.V(4).Infof("Found %d available pods in deployment %s, scaling down old RSes", availablePodCount, deployment.Name)

	newRS := util.FindNewReplicaSet(deployment, allRSs)
	sort.Slice(oldRSs, util.WrapLessFucByImportanceNewer(oldRSs, &dc.strategy))

	// oldPodsCount is the number of replicas of all old RSes.
	oldPodsCount := util.GetReplicaCountForReplicaSets(oldRSs)
	// newRSUpdateLimit is the number of pods should be upgraded limited by partition.
	newRSUpdateLimit := util.NewRSReplicasLimit(dc.strategy.Partition, deployment)
	// newRSDesiredCount is the number of the desired pods of new RS.
	newRSDesiredCount := integer.Int32Max(*(newRS.Spec.Replicas), newRSUpdateLimit)
	// oldRSDesiredCount is the number of the desired pods of all old RSes.
	oldRSDesiredCount := *(deployment.Spec.Replicas) - newRSDesiredCount
	// maxScaledDiff is the number that old RSes should scale down.
	maxScaledDiff := integer.Int32Max(oldPodsCount-oldRSDesiredCount, 0)
	// maxScaledDown is the max number of pods that old RSes can scale down.
	maxScaledDown := integer.Int32Min(availablePodCount-minAvailable, maxScaledDiff)

	klog.InfoS("When Scaling Old ReplicaSets Down in Rolling:",
		// About the new replica set
		"Replicas(New)", *(newRS.Spec.Replicas),
		"Desired(New)", newRSDesiredCount,
		// About the old replica sets
		"ReplicaS(Old)", oldPodsCount,
		"DesiredReplicas(Old)", oldRSDesiredCount,
		"maxScaledDown(Old)", maxScaledDown,
		// About the deployment
		"Replicas(Deployment)", *(deployment.Spec.Replicas),
		"MaxUnavailable(Deployment)", maxUnavailable,
		"Partition(Deployment)", newRSUpdateLimit)

	totalScaledDown := int32(0)
	for i := len(oldRSs) - 1; i >= 0; i-- {
		targetRS := oldRSs[i]
		if totalScaledDown >= maxScaledDown {
			// No further scaling required.
			break
		}
		if *(targetRS.Spec.Replicas) == 0 {
			// cannot scale down this ReplicaSet.
			continue
		}
		// Scale down.
		scaleDownCount := int32(integer.IntMin(int(*(targetRS.Spec.Replicas)), int(maxScaledDown-totalScaledDown)))
		newReplicasCount := *(targetRS.Spec.Replicas) - scaleDownCount
		if newReplicasCount > *(targetRS.Spec.Replicas) {
			return 0, fmt.Errorf("when scaling down old RS, got invalid request to scale down %s/%s %d -> %d", targetRS.Namespace, targetRS.Name, *(targetRS.Spec.Replicas), newReplicasCount)
		}
		_, _, err := dc.scaleReplicaSetAndRecordEvent(ctx, targetRS, newReplicasCount, deployment)
		if err != nil {
			return totalScaledDown, err
		}

		totalScaledDown += scaleDownCount
	}

	return totalScaledDown, nil
}
