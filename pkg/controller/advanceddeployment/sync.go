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
	"reflect"
	"sort"
	"strconv"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/controller/advanceddeployment/util"
	rolloututil "github.com/openkruise/rollouts/pkg/util"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"k8s.io/utils/integer"
)

// sync is responsible for reconciling deployments on scaling events or when they
// are paused.
func (dc *DeploymentController) sync(ctx context.Context, d *apps.Deployment, rsList []*apps.ReplicaSet) (bool, error) {
	newRS, oldRSs, err := dc.getAllReplicaSetsAndSyncRevision(d, rsList, false)
	if err != nil {
		return false, err
	}
	done, err := dc.scale(ctx, d, newRS, oldRSs)
	if err != nil {
		// If we get an error while trying to scale, the deployment will be requeued
		// so we can abort this resync
		return done, err
	}

	allRSs := append(oldRSs, newRS)
	err = dc.syncDeploymentStatus(ctx, allRSs, newRS, d)
	if err != nil {
		return done, err
	}
	return done, nil
}

// getAllReplicaSetsAndSyncRevision returns all the replica sets for the provided deployment (new and all old), with new RS's and deployment's revision updated.
//
// rsList should come from getReplicaSetsForDeployment(d).
//
//  1. Get all old RSes this deployment targets, and calculate the max revision number among them (maxOldV).
//  2. Get new RS this deployment targets (whose pod template matches deployment's), and update new RS's revision number to (maxOldV + 1),
//     only if its revision number is smaller than (maxOldV + 1). If this step failed, we'll update it in the next deployment sync loop.
//  3. Copy new RS's revision number to deployment (update deployment's revision). If this step failed, we'll update it in the next deployment sync loop.
//
// Note that currently the deployment DeploymentController is using caches to avoid querying the server for reads.
// This may lead to stale reads of replica sets, thus incorrect deployment status.
func (dc *DeploymentController) getAllReplicaSetsAndSyncRevision(d *apps.Deployment, rsList []*apps.ReplicaSet, createIfNotExisted bool) (*apps.ReplicaSet, []*apps.ReplicaSet, error) {
	_, allOldRSs := util.FindOldReplicaSets(d, rsList)

	// Get new replica set with the updated revision number
	newRS, err := dc.getNewReplicaSet(context.TODO(), d, rsList, allOldRSs, createIfNotExisted)
	if err != nil {
		return nil, nil, err
	}

	return newRS, allOldRSs, nil
}

const (
	// limit revision history length to 100 element (~2000 chars)
	maxRevHistoryLengthInChars = 2000
)

// Returns a replica set that matches the intent of the given deployment. Returns nil if the new replica set doesn't exist yet.
// 1. Get existing new RS (the RS that the given deployment targets, whose pod template is the same as deployment's).
// 2. If there's existing new RS, update its revision number if it's smaller than (maxOldRevision + 1), where maxOldRevision is the max revision number among all old RSes.
// 3. If there's no existing new RS and createIfNotExisted is true, create one with appropriate revision number (maxOldRevision + 1) and replicas.
// Note that the pod-template-hash will be added to adopted RSes and pods.
func (dc *DeploymentController) getNewReplicaSet(_ context.Context, d *apps.Deployment, rsList, oldRSs []*apps.ReplicaSet, createIfNotExisted bool) (*apps.ReplicaSet, error) {
	existingNewRS := util.FindNewReplicaSet(d, rsList)

	// Calculate the max revision number among all old RSes
	maxOldRevision := util.MaxRevision(oldRSs)
	// Calculate revision number for this new replica set
	newRevision := strconv.FormatInt(maxOldRevision+1, 10)

	// Latest replica set exists. We need to sync its annotations (includes copying all but
	// annotationsToSkip from the parent deployment, and update revision, desiredReplicas,
	// and maxReplicas) and also update the revision annotation in the deployment with the
	// latest revision.
	if existingNewRS != nil {
		rsCopy := existingNewRS.DeepCopy()

		// Set existing new replica set's annotation
		annotationsUpdated := util.SetNewReplicaSetAnnotations(d, &dc.strategy, rsCopy, newRevision, true, maxRevHistoryLengthInChars)
		minReadySecondsNeedsUpdate := rsCopy.Spec.MinReadySeconds != d.Spec.MinReadySeconds
		if annotationsUpdated || minReadySecondsNeedsUpdate {
			rsCopy.Spec.MinReadySeconds = d.Spec.MinReadySeconds
			err := dc.client.Update(context.TODO(), rsCopy)
			return rsCopy, err
		}

		// Should use the revision in existingNewRS's annotation, since it set by before
		needsUpdate := util.SetDeploymentRevision(d, rsCopy.Annotations[util.RevisionAnnotation])
		// If no other Progressing condition has been recorded and we need to estimate the progress
		// of this deployment then it is likely that old users started caring about progress. In that
		// case we need to take into account the first time we noticed their new replica set.
		cond := util.GetDeploymentCondition(d.Status, apps.DeploymentProgressing)
		if util.HasProgressDeadline(d) && cond == nil {
			msg := fmt.Sprintf("Found new replica set %q", rsCopy.Name)
			condition := util.NewDeploymentCondition(apps.DeploymentProgressing, corev1.ConditionTrue, util.FoundNewRSReason, msg)
			util.SetDeploymentCondition(&d.Status, *condition)
			needsUpdate = true
		}

		if needsUpdate {
			var err error
			if err = dc.client.Update(context.TODO(), d); err != nil {
				return nil, err
			}
		}
		return rsCopy, nil
	}

	if !createIfNotExisted {
		return nil, nil
	}

	// new ReplicaSet does not exist, create one.
	newRSTemplate := *d.Spec.Template.DeepCopy()
	podTemplateSpecHash := rolloututil.ComputeHash(&newRSTemplate, d.Status.CollisionCount)
	newRSTemplate.Labels = util.CloneAndAddLabel(d.Spec.Template.Labels, apps.DefaultDeploymentUniqueLabelKey, podTemplateSpecHash)
	// Add podTemplateHash label to selector.
	newRSSelector := util.CloneSelectorAndAddLabel(d.Spec.Selector, apps.DefaultDeploymentUniqueLabelKey, podTemplateSpecHash)

	// Create new ReplicaSet
	newRS := apps.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			// Make the name deterministic, to ensure idempotence
			Name:            d.Name + "-" + podTemplateSpecHash,
			Namespace:       d.Namespace,
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(d, controllerKind)},
			Labels:          newRSTemplate.Labels,
		},
		Spec: apps.ReplicaSetSpec{
			Replicas:        new(int32),
			MinReadySeconds: d.Spec.MinReadySeconds,
			Selector:        newRSSelector,
			Template:        newRSTemplate,
		},
	}
	allRSs := append(oldRSs, &newRS)
	newReplicasCount, err := util.NewRSNewReplicas(d, &dc.strategy, allRSs, &newRS)
	if err != nil {
		return nil, err
	}
	newReplicasCount = integer.Int32Min(newReplicasCount, util.NewRSReplicasLimit(dc.strategy.Partition, d))

	*(newRS.Spec.Replicas) = newReplicasCount
	// Set new replica set's annotation
	util.SetNewReplicaSetAnnotations(d, &dc.strategy, &newRS, newRevision, false, maxRevHistoryLengthInChars)
	// Create the new ReplicaSet. If it already exists, then we need to check for possible
	// hash collisions. If there is any other error, we need to report it in the status of
	// the Deployment.
	alreadyExists := false
	createdRS := &newRS
	err = dc.client.Create(context.TODO(), createdRS)
	switch {
	// We may end up hitting this due to a slow cache or a fast resync of the Deployment.
	case errors.IsAlreadyExists(err):
		alreadyExists = true

		// Fetch a copy of the ReplicaSet.
		rs := &apps.ReplicaSet{}
		rsErr := dc.client.Get(context.TODO(), types.NamespacedName{Name: newRS.Name, Namespace: newRS.Namespace}, rs)
		if rsErr != nil {
			return nil, rsErr
		}

		// If the Deployment owns the ReplicaSet and the ReplicaSet's PodTemplateSpec is semantically
		// deep equal to the PodTemplateSpec of the Deployment, it's the Deployment's new ReplicaSet.
		// Otherwise, this is a hash collision and we need to increment the collisionCount field in
		// the status of the Deployment and requeue to try the creation in the next sync.
		controllerRef := metav1.GetControllerOf(rs)
		if controllerRef != nil && controllerRef.UID == d.UID && util.EqualIgnoreHash(&d.Spec.Template, &rs.Spec.Template) {
			createdRS = rs
			err = nil
			break
		}

		// Matching ReplicaSet is not equal - increment the collisionCount in the DeploymentStatus
		// and requeue the Deployment.
		if d.Status.CollisionCount == nil {
			d.Status.CollisionCount = new(int32)
		}
		preCollisionCount := *d.Status.CollisionCount
		*d.Status.CollisionCount++
		// Update the collisionCount for the Deployment and let it requeue by returning the original
		// error.
		dErr := dc.client.Status().Update(context.TODO(), d)
		if dErr == nil {
			klog.V(2).Infof("Found a hash collision for deployment %q - bumping collisionCount (%d->%d) to resolve it", d.Name, preCollisionCount, *d.Status.CollisionCount)
		}
		return nil, err
	case errors.HasStatusCause(err, corev1.NamespaceTerminatingCause):
		// if the namespace is terminating, all subsequent creates will fail and we can safely do nothing
		return nil, err
	case err != nil:
		msg := fmt.Sprintf("Failed to create new replica set %q: %v", newRS.Name, err)
		if util.HasProgressDeadline(d) {
			cond := util.NewDeploymentCondition(apps.DeploymentProgressing, corev1.ConditionFalse, util.FailedRSCreateReason, msg)
			util.SetDeploymentCondition(&d.Status, *cond)
			// We don't really care about this error at this point, since we have a bigger issue to report.
			// TODO: Identify which errors are permanent and switch DeploymentIsFailed to take into account
			// these reasons as well. Related issue: https://github.com/kubernetes/kubernetes/issues/18568
			_ = dc.client.Status().Update(context.TODO(), d)
		}
		dc.eventRecorder.Eventf(d, corev1.EventTypeWarning, util.FailedRSCreateReason, msg)
		return nil, err
	}
	if !alreadyExists && newReplicasCount > 0 {
		dc.eventRecorder.Eventf(d, corev1.EventTypeNormal, "ScalingReplicaSet", "Scaled up replica set %s to %d", createdRS.Name, newReplicasCount)
	}

	needsUpdate := util.SetDeploymentRevision(d, newRevision)
	if !alreadyExists && util.HasProgressDeadline(d) {
		msg := fmt.Sprintf("Created new replica set %q", createdRS.Name)
		condition := util.NewDeploymentCondition(apps.DeploymentProgressing, corev1.ConditionTrue, util.NewReplicaSetReason, msg)
		util.SetDeploymentCondition(&d.Status, *condition)
		needsUpdate = true
	}
	if needsUpdate {
		err = dc.client.Status().Update(context.TODO(), d)
	}
	return createdRS, err
}

// scale scales proportionally in order to mitigate risk. Otherwise, scaling up can increase the size
// of the new replica set and scaling down can decrease the sizes of the old ones, both of which would
// have the effect of hastening the rollout progress, which could produce a higher proportion of unavailable
// replicas in the event of a problem with the rolled out template. Should run only on scaling events or
// when a deployment is paused and not during the normal rollout process.
func (dc *DeploymentController) scale(ctx context.Context, deployment *apps.Deployment, newRS *apps.ReplicaSet, oldRSs []*apps.ReplicaSet) (bool, error) {
	// If there is only one active replica set then we should scale that up to the full count of the
	// deployment. If there is no active replica set, then we should scale up the newest replica set.
	if activeOrLatest := util.FindActiveOrLatest(newRS, oldRSs); activeOrLatest != nil {
		if *(activeOrLatest.Spec.Replicas) == *(deployment.Spec.Replicas) {
			return true, nil
		}
		_, _, err := dc.scaleReplicaSetAndRecordEvent(ctx, activeOrLatest, *(deployment.Spec.Replicas), deployment)
		if err != nil {
			return false, err
		}
		return true, nil
	}

	// If the new replica set is saturated, old replica sets should be fully scaled down.
	// This case handles replica set adoption during a saturated new replica set.
	if util.IsSaturated(deployment, newRS) {
		for _, old := range util.FilterActiveReplicaSets(oldRSs) {
			if _, _, err := dc.scaleReplicaSetAndRecordEvent(ctx, old, 0, deployment); err != nil {
				return false, err
			}
		}
		return true, nil
	}

	// There are old replica sets with pods and the new replica set is not saturated.
	// We need to proportionally scale all replica sets (new and old) in case of a
	// rolling deployment.
	if util.IsRollingUpdate(&dc.strategy) {
		allRSs := util.FilterActiveReplicaSets(append(oldRSs, newRS))
		allRSsReplicas := util.GetReplicaCountForReplicaSets(allRSs)

		allowedSize := int32(0)
		if *(deployment.Spec.Replicas) > 0 {
			allowedSize = *(deployment.Spec.Replicas)
		}

		// Number of additional replicas that can be either added or removed from the total
		// replicas count. These replicas should be distributed proportionally to the active
		// replica sets.
		deploymentReplicasToAdd := allowedSize - allRSsReplicas

		// The additional replicas should be distributed proportionally amongst the active
		// replica sets from the larger to the smaller in size replica set. Scaling direction
		// drives what happens in case we are trying to scale replica sets of the same size.
		// In such a case when scaling up, we should scale up newer replica sets first, and
		// when scaling down, we should scale down older replica sets first.
		var scalingOperation string
		switch {
		case deploymentReplicasToAdd > 0:
			sort.Sort(util.ReplicaSetsBySizeNewer(allRSs))
			scalingOperation = "up"

		case deploymentReplicasToAdd < 0:
			sort.Sort(util.ReplicaSetsBySizeOlder(allRSs))
			scalingOperation = "down"
		}

		// Iterate over all active replica sets and estimate proportions for each of them.
		// The absolute value of deploymentReplicasAdded should never exceed the absolute
		// value of deploymentReplicasToAdd.
		deploymentReplicasAdded := int32(0)
		nameToSize := make(map[string]int32)
		for i := range allRSs {
			rs := allRSs[i]

			// Estimate proportions if we have replicas to add, otherwise simply populate
			// nameToSize with the current sizes for each replica set.
			if deploymentReplicasToAdd != 0 {
				proportion := util.GetProportion(rs, *deployment, &dc.strategy, deploymentReplicasToAdd, deploymentReplicasAdded)

				nameToSize[rs.Name] = *(rs.Spec.Replicas) + proportion
				deploymentReplicasAdded += proportion
			} else {
				nameToSize[rs.Name] = *(rs.Spec.Replicas)
			}
		}

		// Update all replica sets
		for i := range allRSs {
			rs := allRSs[i]

			// Add/remove any leftovers to the largest replica set.
			if i == 0 && deploymentReplicasToAdd != 0 {
				leftover := deploymentReplicasToAdd - deploymentReplicasAdded
				nameToSize[rs.Name] = nameToSize[rs.Name] + leftover
				if nameToSize[rs.Name] < 0 {
					nameToSize[rs.Name] = 0
				}
			}

			// TODO: Use transactions when we have them.
			if _, _, err := dc.scaleReplicaSet(ctx, rs, nameToSize[rs.Name], deployment, scalingOperation); err != nil {
				// Return as soon as we fail, the deployment is requeued
				return false, err
			}
		}
	}
	return true, nil
}

func (dc *DeploymentController) scaleReplicaSetAndRecordEvent(ctx context.Context, rs *apps.ReplicaSet, newScale int32, deployment *apps.Deployment) (bool, *apps.ReplicaSet, error) {
	// No need to scale
	if *(rs.Spec.Replicas) == newScale {
		return false, rs, nil
	}
	var scalingOperation string
	if *(rs.Spec.Replicas) < newScale {
		scalingOperation = "up"
	} else {
		scalingOperation = "down"
	}
	scaled, newRS, err := dc.scaleReplicaSet(ctx, rs, newScale, deployment, scalingOperation)
	return scaled, newRS, err
}

func (dc *DeploymentController) scaleReplicaSet(_ context.Context, rs *apps.ReplicaSet, newScale int32, deployment *apps.Deployment, scalingOperation string) (bool, *apps.ReplicaSet, error) {

	sizeNeedsUpdate := *(rs.Spec.Replicas) != newScale

	annotationsNeedUpdate := util.ReplicasAnnotationsNeedUpdate(rs, *(deployment.Spec.Replicas), *(deployment.Spec.Replicas)+util.MaxSurge(*deployment, &dc.strategy))

	scaled := false
	var err error
	if sizeNeedsUpdate || annotationsNeedUpdate {
		oldScale := *(rs.Spec.Replicas)
		rsCopy := rs.DeepCopy()
		*(rsCopy.Spec.Replicas) = newScale
		util.SetReplicasAnnotations(rsCopy, *(deployment.Spec.Replicas), *(deployment.Spec.Replicas)+util.MaxSurge(*deployment, &dc.strategy))
		err = dc.client.Update(context.TODO(), rsCopy)
		rs = rsCopy
		if err == nil && sizeNeedsUpdate {
			scaled = true
			dc.eventRecorder.Eventf(deployment, corev1.EventTypeNormal, "ScalingReplicaSet", "Scaled %s replica set %s to %d from %d", scalingOperation, rs.Name, newScale, oldScale)
		}
	}
	return scaled, rs, err
}

// syncDeploymentStatus checks if the status is up-to-date and sync it if necessary
func (dc *DeploymentController) syncDeploymentStatus(_ context.Context, allRSs []*apps.ReplicaSet, newRS *apps.ReplicaSet, d *apps.Deployment) error {
	newStatus := calculateStatus(allRSs, newRS, d, &dc.strategy)

	if reflect.DeepEqual(d.Status, newStatus) {
		return nil
	}

	newDeployment := d
	newDeployment.Status = newStatus
	return dc.client.Status().Update(context.TODO(), newDeployment)
}

// calculateStatus calculates the latest status for the provided deployment by looking into the provided replica sets.
func calculateStatus(allRSs []*apps.ReplicaSet, newRS *apps.ReplicaSet, deployment *apps.Deployment, strategy *rolloutsv1alpha1.AdvancedDeploymentStrategy) apps.DeploymentStatus {
	availableReplicas := util.GetAvailableReplicaCountForReplicaSets(allRSs)
	totalReplicas := util.GetReplicaCountForReplicaSets(allRSs)
	unavailableReplicas := totalReplicas - availableReplicas
	// If unavailableReplicas is negative, then that means the Deployment has more available replicas running than
	// desired, e.g. whenever it scales down. In such a case we should simply default unavailableReplicas to zero.
	if unavailableReplicas < 0 {
		unavailableReplicas = 0
	}

	status := apps.DeploymentStatus{
		// TODO: Ensure that if we start retrying status updates, we won't pick up a new Generation value.
		ObservedGeneration:  deployment.Generation,
		Replicas:            util.GetActualReplicaCountForReplicaSets(allRSs),
		UpdatedReplicas:     util.GetActualReplicaCountForReplicaSets([]*apps.ReplicaSet{newRS}),
		ReadyReplicas:       util.GetReadyReplicaCountForReplicaSets(allRSs),
		AvailableReplicas:   availableReplicas,
		UnavailableReplicas: unavailableReplicas,
		CollisionCount:      deployment.Status.CollisionCount,
	}

	// Copy conditions one by one so we won't mutate the original object.
	conditions := deployment.Status.Conditions
	for i := range conditions {
		status.Conditions = append(status.Conditions, conditions[i])
	}

	if availableReplicas >= *(deployment.Spec.Replicas)-util.MaxUnavailable(*deployment, strategy) {
		minAvailability := util.NewDeploymentCondition(apps.DeploymentAvailable, corev1.ConditionTrue, util.MinimumReplicasAvailable, "Deployment has minimum availability.")
		util.SetDeploymentCondition(&status, *minAvailability)
	} else {
		noMinAvailability := util.NewDeploymentCondition(apps.DeploymentAvailable, corev1.ConditionFalse, util.MinimumReplicasUnavailable, "Deployment does not have minimum availability.")
		util.SetDeploymentCondition(&status, *noMinAvailability)
	}

	return status
}

// isScalingEvent checks whether the provided deployment has been updated with a scaling event
// by looking at the desired-replicas annotation in the active replica sets of the deployment.
//
// rsList should come from getReplicaSetsForDeployment(d).
func (dc *DeploymentController) isScalingEvent(d *apps.Deployment, rsList []*apps.ReplicaSet) (bool, error) {
	newRS, oldRSs, err := dc.getAllReplicaSetsAndSyncRevision(d, rsList, false)
	if err != nil {
		return false, err
	}
	allRSs := append(oldRSs, newRS)
	for _, rs := range util.FilterActiveReplicaSets(allRSs) {
		desired, ok := util.GetDesiredReplicasAnnotation(rs)
		if !ok {
			continue
		}
		if desired != *(d.Spec.Replicas) {
			return true, nil
		}
	}
	return false, nil
}
