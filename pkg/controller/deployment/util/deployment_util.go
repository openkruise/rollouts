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

package util

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"k8s.io/utils/integer"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
)

const (
	// RevisionAnnotation is the revision annotation of a deployment's replica sets which records its rollout sequence
	RevisionAnnotation = "deployment.kubernetes.io/revision"
	// RevisionHistoryAnnotation maintains the history of all old revisions that a replica set has served for a deployment.
	RevisionHistoryAnnotation = "deployment.kubernetes.io/revision-history"
	// ReplicasAnnotation is the desired replicas for a deployment recorded as an annotation
	// in its replica sets. Helps in separating scaling events from the rollout process and for
	// determining if the new replica set for a deployment is really saturated.
	ReplicasAnnotation = "deployment.kubernetes.io/desired-replicas"
	// MaxReplicasAnnotation is the maximum replicas a deployment can have at a given point, which
	// is deployment.spec.replicas + maxSurge. Used by the underlying replica sets to estimate their
	// proportions in case the deployment has surge replicas.
	MaxReplicasAnnotation = "deployment.kubernetes.io/max-replicas"

	// Reasons for deployment conditions
	//
	// Progressing:

	// ReplicaSetUpdatedReason is added in a deployment when one of its replica sets is updated as part
	// of the rollout process.
	ReplicaSetUpdatedReason = "ReplicaSetUpdated"
	// FailedRSCreateReason is added in a deployment when it cannot create a new replica set.
	FailedRSCreateReason = "ReplicaSetCreateError"
	// NewReplicaSetReason is added in a deployment when it creates a new replica set.
	NewReplicaSetReason = "NewReplicaSetCreated"
	// FoundNewRSReason is added in a deployment when it adopts an existing replica set.
	FoundNewRSReason = "FoundNewReplicaSet"
	// NewRSAvailableReason is added in a deployment when its newest replica set is made available
	// ie. the number of new pods that have passed readiness checks and run for at least minReadySeconds
	// is at least the minimum available pods that need to run for the deployment.
	NewRSAvailableReason = "NewReplicaSetAvailable"
	// TimedOutReason is added in a deployment when its newest replica set fails to show any progress
	// within the given deadline (progressDeadlineSeconds).
	TimedOutReason = "ProgressDeadlineExceeded"
	// PausedDeployReason is added in a deployment when it is paused. Lack of progress shouldn't be
	// estimated once a deployment is paused.
	PausedDeployReason = "DeploymentPaused"
	// ResumedDeployReason is added in a deployment when it is resumed. Useful for not failing accidentally
	// deployments that paused amidst a rollout and are bounded by a deadline.
	ResumedDeployReason = "DeploymentResumed"
	//
	// Available:

	// MinimumReplicasAvailable is added in a deployment when it has its minimum replicas required available.
	MinimumReplicasAvailable = "MinimumReplicasAvailable"
	// MinimumReplicasUnavailable is added in a deployment when it doesn't have the minimum required replicas
	// available.
	MinimumReplicasUnavailable = "MinimumReplicasUnavailable"
)

// NewDeploymentCondition creates a new deployment condition.
func NewDeploymentCondition(condType apps.DeploymentConditionType, status v1.ConditionStatus, reason, message string) *apps.DeploymentCondition {
	return &apps.DeploymentCondition{
		Type:               condType,
		Status:             status,
		LastUpdateTime:     metav1.Now(),
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
}

// GetDeploymentCondition returns the condition with the provided type.
func GetDeploymentCondition(status apps.DeploymentStatus, condType apps.DeploymentConditionType) *apps.DeploymentCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}

// SetDeploymentCondition updates the deployment to include the provided condition. If the condition that
// we are about to add already exists and has the same status and reason then we are not going to update.
func SetDeploymentCondition(status *apps.DeploymentStatus, condition apps.DeploymentCondition) {
	currentCond := GetDeploymentCondition(*status, condition.Type)
	if currentCond != nil && currentCond.Status == condition.Status && currentCond.Reason == condition.Reason {
		return
	}
	// Do not update lastTransitionTime if the status of the condition doesn't change.
	if currentCond != nil && currentCond.Status == condition.Status {
		condition.LastTransitionTime = currentCond.LastTransitionTime
	}
	newConditions := filterOutCondition(status.Conditions, condition.Type)
	status.Conditions = append(newConditions, condition)
}

// RemoveDeploymentCondition removes the deployment condition with the provided type.
func RemoveDeploymentCondition(status *apps.DeploymentStatus, condType apps.DeploymentConditionType) {
	status.Conditions = filterOutCondition(status.Conditions, condType)
}

// filterOutCondition returns a new slice of deployment conditions without conditions with the provided type.
func filterOutCondition(conditions []apps.DeploymentCondition, condType apps.DeploymentConditionType) []apps.DeploymentCondition {
	var newConditions []apps.DeploymentCondition
	for _, c := range conditions {
		if c.Type == condType {
			continue
		}
		newConditions = append(newConditions, c)
	}
	return newConditions
}

// ReplicaSetToDeploymentCondition converts a replica set condition into a deployment condition.
// Useful for promoting replica set failure conditions into deployments.
func ReplicaSetToDeploymentCondition(cond apps.ReplicaSetCondition) apps.DeploymentCondition {
	return apps.DeploymentCondition{
		Type:               apps.DeploymentConditionType(cond.Type),
		Status:             cond.Status,
		LastTransitionTime: cond.LastTransitionTime,
		LastUpdateTime:     cond.LastTransitionTime,
		Reason:             cond.Reason,
		Message:            cond.Message,
	}
}

// SetDeploymentRevision updates the revision for a deployment.
func SetDeploymentRevision(deployment *apps.Deployment, revision string) bool {
	updated := false

	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	if deployment.Annotations[RevisionAnnotation] != revision {
		deployment.Annotations[RevisionAnnotation] = revision
		updated = true
	}

	return updated
}

// MaxRevision finds the highest revision in the replica sets
func MaxRevision(allRSs []*apps.ReplicaSet) int64 {
	max := int64(0)
	for _, rs := range allRSs {
		if v, err := Revision(rs); err != nil {
			// Skip the replica sets when it failed to parse their revision information
			klog.V(4).Infof("Error: %v. Couldn't parse revision for replica set %#v, deployment controller will skip it when reconciling revisions.", err, rs)
		} else if v > max {
			max = v
		}
	}
	return max
}

// Revision returns the revision number of the input object.
func Revision(obj runtime.Object) (int64, error) {
	acc, err := meta.Accessor(obj)
	if err != nil {
		return 0, err
	}
	v, ok := acc.GetAnnotations()[RevisionAnnotation]
	if !ok {
		return 0, nil
	}
	return strconv.ParseInt(v, 10, 64)
}

// SetNewReplicaSetAnnotations sets new replica set's annotations appropriately by updating its revision and
// copying required deployment annotations to it; it returns true if replica set's annotation is changed.
func SetNewReplicaSetAnnotations(deployment *apps.Deployment, newRS *apps.ReplicaSet, strategy *rolloutsv1alpha1.DeploymentStrategy, newRevision string, exists bool, revHistoryLimitInChars int) bool {
	// First, copy deployment's annotations (except for apply and revision annotations)
	annotationChanged := copyDeploymentAnnotationsToReplicaSet(deployment, newRS)
	// Then, update replica set's revision annotation
	if newRS.Annotations == nil {
		newRS.Annotations = make(map[string]string)
	}
	oldRevision, ok := newRS.Annotations[RevisionAnnotation]
	// The newRS's revision should be the greatest among all RSes. Usually, its revision number is newRevision (the max revision number
	// of all old RSes + 1). However, it's possible that some of the old RSes are deleted after the newRS revision being updated, and
	// newRevision becomes smaller than newRS's revision. We should only update newRS revision when it's smaller than newRevision.

	oldRevisionInt, err := strconv.ParseInt(oldRevision, 10, 64)
	if err != nil {
		if oldRevision != "" {
			klog.Warningf("Updating replica set revision OldRevision not int %s", err)
			return false
		}
		//If the RS annotation is empty then initialize it to 0
		oldRevisionInt = 0
	}
	newRevisionInt, err := strconv.ParseInt(newRevision, 10, 64)
	if err != nil {
		klog.Warningf("Updating replica set revision NewRevision not int %s", err)
		return false
	}
	if oldRevisionInt < newRevisionInt {
		newRS.Annotations[RevisionAnnotation] = newRevision
		annotationChanged = true
		klog.V(4).Infof("Updating replica set %q revision to %s", newRS.Name, newRevision)
	}
	// If a revision annotation already existed and this replica set was updated with a new revision
	// then that means we are rolling back to this replica set. We need to preserve the old revisions
	// for historical information.
	if ok && oldRevisionInt < newRevisionInt {
		revisionHistoryAnnotation := newRS.Annotations[RevisionHistoryAnnotation]
		oldRevisions := strings.Split(revisionHistoryAnnotation, ",")
		if len(oldRevisions[0]) == 0 {
			newRS.Annotations[RevisionHistoryAnnotation] = oldRevision
		} else {
			totalLen := len(revisionHistoryAnnotation) + len(oldRevision) + 1
			// index for the starting position in oldRevisions
			start := 0
			for totalLen > revHistoryLimitInChars && start < len(oldRevisions) {
				totalLen = totalLen - len(oldRevisions[start]) - 1
				start++
			}
			if totalLen <= revHistoryLimitInChars {
				oldRevisions = append(oldRevisions[start:], oldRevision)
				newRS.Annotations[RevisionHistoryAnnotation] = strings.Join(oldRevisions, ",")
			} else {
				klog.Warningf("Not appending revision due to length limit of %v reached", revHistoryLimitInChars)
			}
		}
	}
	// If the new replica set is about to be created, we need to add replica annotations to it.
	if !exists && SetReplicasAnnotations(newRS, *(deployment.Spec.Replicas), *(deployment.Spec.Replicas)+MaxSurge(deployment, strategy)) {
		annotationChanged = true
	}
	return annotationChanged
}

var annotationsToSkip = map[string]bool{
	v1.LastAppliedConfigAnnotation: true,
	RevisionAnnotation:             true,
	RevisionHistoryAnnotation:      true,
	ReplicasAnnotation:             true,
	MaxReplicasAnnotation:          true,
	apps.DeprecatedRollbackTo:      true,
}

// skipCopyAnnotation returns true if we should skip copying the annotation with the given annotation key
// TODO: How to decide which annotations should / should not be copied?
//
//	See https://github.com/kubernetes/kubernetes/pull/20035#issuecomment-179558615
func skipCopyAnnotation(key string) bool {
	return annotationsToSkip[key]
}

// copyDeploymentAnnotationsToReplicaSet copies deployment's annotations to replica set's annotations,
// and returns true if replica set's annotation is changed.
// Note that apply and revision annotations are not copied.
func copyDeploymentAnnotationsToReplicaSet(deployment *apps.Deployment, rs *apps.ReplicaSet) bool {
	rsAnnotationsChanged := false
	if rs.Annotations == nil {
		rs.Annotations = make(map[string]string)
	}
	for k, v := range deployment.Annotations {
		// newRS revision is updated automatically in getNewReplicaSet, and the deployment's revision number is then updated
		// by copying its newRS revision number. We should not copy deployment's revision to its newRS, since the update of
		// deployment revision number may fail (revision becomes stale) and the revision number in newRS is more reliable.
		if _, exist := rs.Annotations[k]; skipCopyAnnotation(k) || (exist && rs.Annotations[k] == v) {
			continue
		}
		rs.Annotations[k] = v
		rsAnnotationsChanged = true
	}
	return rsAnnotationsChanged
}

// FindActiveOrLatest returns the only active or the latest replica set in case there is at most one active
// replica set. If there are more active replica sets, then we should proportionally scale them.
func FindActiveOrLatest(newRS *apps.ReplicaSet, oldRSs []*apps.ReplicaSet) *apps.ReplicaSet {
	if newRS == nil && len(oldRSs) == 0 {
		return nil
	}

	sort.Sort(sort.Reverse(ReplicaSetsByCreationTimestamp(oldRSs)))
	allRSs := FilterActiveReplicaSets(append(oldRSs, newRS))

	switch len(allRSs) {
	case 0:
		// If there is no active replica set then we should return the newest.
		if newRS != nil {
			return newRS
		}
		return oldRSs[0]
	case 1:
		return allRSs[0]
	default:
		return nil
	}
}

// GetReplicasAnnotation returns the number of desired replicas
func GetReplicasAnnotation(rs *apps.ReplicaSet) (int32, bool) {
	return getIntFromAnnotation(rs, ReplicasAnnotation)
}

func getMaxReplicasAnnotation(rs *apps.ReplicaSet) (int32, bool) {
	return getIntFromAnnotation(rs, MaxReplicasAnnotation)
}

func getIntFromAnnotation(rs *apps.ReplicaSet, annotationKey string) (int32, bool) {
	annotationValue, ok := rs.Annotations[annotationKey]
	if !ok {
		return int32(0), false
	}
	intValue, err := strconv.Atoi(annotationValue)
	if err != nil {
		klog.V(2).Infof("Cannot convert the value %q with annotation key %q for the replica set %q", annotationValue, annotationKey, rs.Name)
		return int32(0), false
	}
	return int32(intValue), true
}

// SetReplicasAnnotations sets the desiredReplicas and maxReplicas into the annotations
func SetReplicasAnnotations(rs *apps.ReplicaSet, desiredReplicas, maxReplicas int32) bool {
	updated := false
	if rs.Annotations == nil {
		rs.Annotations = make(map[string]string)
	}
	desiredString := fmt.Sprintf("%d", desiredReplicas)
	if hasString := rs.Annotations[ReplicasAnnotation]; hasString != desiredString {
		rs.Annotations[ReplicasAnnotation] = desiredString
		updated = true
	}
	maxString := fmt.Sprintf("%d", maxReplicas)
	if hasString := rs.Annotations[MaxReplicasAnnotation]; hasString != maxString {
		rs.Annotations[MaxReplicasAnnotation] = maxString
		updated = true
	}
	return updated
}

// ReplicasAnnotationsNeedUpdate return true if ReplicasAnnotations need to be updated
func ReplicasAnnotationsNeedUpdate(rs *apps.ReplicaSet, desiredReplicas, maxReplicas int32) bool {
	if rs.Annotations == nil {
		return true
	}
	desiredString := fmt.Sprintf("%d", desiredReplicas)
	if hasString := rs.Annotations[ReplicasAnnotation]; hasString != desiredString {
		return true
	}
	maxString := fmt.Sprintf("%d", maxReplicas)
	if hasString := rs.Annotations[MaxReplicasAnnotation]; hasString != maxString {
		return true
	}
	return false
}

// MaxUnavailable returns the maximum unavailable pods a rolling deployment can take.
func MaxUnavailable(deployment *apps.Deployment, strategy *rolloutsv1alpha1.DeploymentStrategy) int32 {
	if strategy == nil || strategy.RollingUpdate == nil || *(deployment.Spec.Replicas) == 0 {
		return int32(0)
	}
	// Error caught by validation
	_, maxUnavailable, _ := ResolveFenceposts(strategy.RollingUpdate.MaxSurge, strategy.RollingUpdate.MaxUnavailable, *(deployment.Spec.Replicas))
	if maxUnavailable > *deployment.Spec.Replicas {
		return *deployment.Spec.Replicas
	}
	return maxUnavailable
}

// MinAvailable returns the minimum available pods of a given deployment
func MinAvailable(deployment *apps.Deployment, strategy *rolloutsv1alpha1.DeploymentStrategy) int32 {
	if strategy == nil || strategy.RollingUpdate == nil {
		return int32(0)
	}
	return *(deployment.Spec.Replicas) - MaxUnavailable(deployment, strategy)
}

// MaxSurge returns the maximum surge pods a rolling deployment can take.
func MaxSurge(deployment *apps.Deployment, strategy *rolloutsv1alpha1.DeploymentStrategy) int32 {
	if strategy == nil || strategy.RollingUpdate == nil {
		return int32(0)
	}
	// Error caught by validation
	maxSurge, _, _ := ResolveFenceposts(strategy.RollingUpdate.MaxSurge, strategy.RollingUpdate.MaxUnavailable, *(deployment.Spec.Replicas))
	return maxSurge
}

// GetProportion will estimate the proportion for the provided replica set using 1. the current size
// of the parent deployment, 2. the replica count that needs be added on the replica sets of the
// deployment, and 3. the total replicas added in the replica sets of the deployment so far.
func GetProportion(rs *apps.ReplicaSet, d apps.Deployment, strategy *rolloutsv1alpha1.DeploymentStrategy, deploymentReplicasToAdd, deploymentReplicasAdded int32) int32 {
	if rs == nil || *(rs.Spec.Replicas) == 0 || deploymentReplicasToAdd == 0 || deploymentReplicasToAdd == deploymentReplicasAdded {
		return int32(0)
	}

	rsFraction := getReplicaSetFraction(*rs, d, strategy)
	allowed := deploymentReplicasToAdd - deploymentReplicasAdded

	if deploymentReplicasToAdd > 0 {
		// Use the minimum between the replica set fraction and the maximum allowed replicas
		// when scaling up. This way we ensure we will not scale up more than the allowed
		// replicas we can add.
		return integer.Int32Min(rsFraction, allowed)
	}
	// Use the maximum between the replica set fraction and the maximum allowed replicas
	// when scaling down. This way we ensure we will not scale down more than the allowed
	// replicas we can remove.
	return integer.Int32Max(rsFraction, allowed)
}

// getReplicaSetFraction estimates the fraction of replicas a replica set can have in
// 1. a scaling event during a rollout or 2. when scaling a paused deployment.
func getReplicaSetFraction(rs apps.ReplicaSet, d apps.Deployment, strategy *rolloutsv1alpha1.DeploymentStrategy) int32 {
	// If we are scaling down to zero then the fraction of this replica set is its whole size (negative)
	if *(d.Spec.Replicas) == int32(0) {
		return -*(rs.Spec.Replicas)
	}

	deploymentReplicas := *(d.Spec.Replicas) + MaxSurge(&d, strategy)
	annotatedReplicas, ok := getMaxReplicasAnnotation(&rs)
	if !ok {
		// If we cannot find the annotation then fallback to the current deployment size. Note that this
		// will not be an accurate proportion estimation in case other replica sets have different values
		// which means that the deployment was scaled at some point but we at least will stay in limits
		// due to the min-max comparisons in getProportion.
		annotatedReplicas = d.Status.Replicas
	}

	// We should never proportionally scale up from zero which means rs.spec.replicas and annotatedReplicas
	// will never be zero here.
	newRSsize := (float64(*(rs.Spec.Replicas) * deploymentReplicas)) / float64(annotatedReplicas)
	return integer.RoundToInt32(newRSsize) - *(rs.Spec.Replicas)
}

// TODO: switch RsListFunc and podListFunc to full namespacers

// RsListFunc returns the ReplicaSet from the ReplicaSet namespace and the List metav1.ListOptions.
type RsListFunc func(string, metav1.ListOptions) ([]*apps.ReplicaSet, error)

// podListFunc returns the PodList from the Pod namespace and the List metav1.ListOptions.
type podListFunc func(string, metav1.ListOptions) (*v1.PodList, error)

// ListReplicaSets returns a slice of RSes the given deployment targets.
// Note that this does NOT attempt to reconcile ControllerRef (adopt/orphan),
// because only the controller itself should do that.
// However, it does filter out anything whose ControllerRef doesn't match.
func ListReplicaSets(deployment *apps.Deployment, getRSList RsListFunc) ([]*apps.ReplicaSet, error) {
	// TODO: Right now we list replica sets by their labels. We should list them by selector, i.e. the replica set's selector
	//       should be a superset of the deployment's selector, see https://github.com/kubernetes/kubernetes/issues/19830.
	namespace := deployment.Namespace
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}
	options := metav1.ListOptions{LabelSelector: selector.String()}
	all, err := getRSList(namespace, options)
	if err != nil {
		return nil, err
	}
	// Only include those whose ControllerRef matches the Deployment.
	owned := make([]*apps.ReplicaSet, 0, len(all))
	for _, rs := range all {
		if metav1.IsControlledBy(rs, deployment) {
			owned = append(owned, rs)
		}
	}
	return owned, nil
}

// ListPods returns a list of pods the given deployment targets.
// This needs a list of ReplicaSets for the Deployment,
// which can be found with ListReplicaSets().
// Note that this does NOT attempt to reconcile ControllerRef (adopt/orphan),
// because only the controller itself should do that.
// However, it does filter out anything whose ControllerRef doesn't match.
func ListPods(deployment *apps.Deployment, rsList []*apps.ReplicaSet, getPodList podListFunc) (*v1.PodList, error) {
	namespace := deployment.Namespace
	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, err
	}
	options := metav1.ListOptions{LabelSelector: selector.String()}
	all, err := getPodList(namespace, options)
	if err != nil {
		return all, err
	}
	// Only include those whose ControllerRef points to a ReplicaSet that is in
	// turn owned by this Deployment.
	rsMap := make(map[types.UID]bool, len(rsList))
	for _, rs := range rsList {
		rsMap[rs.UID] = true
	}
	owned := &v1.PodList{Items: make([]v1.Pod, 0, len(all.Items))}
	for i := range all.Items {
		pod := &all.Items[i]
		controllerRef := metav1.GetControllerOf(pod)
		if controllerRef != nil && rsMap[controllerRef.UID] {
			owned.Items = append(owned.Items, *pod)
		}
	}
	return owned, nil
}

// EqualIgnoreHash returns true if two given podTemplateSpec are equal, ignoring the diff in value of Labels[pod-template-hash]
// We ignore pod-template-hash because:
//  1. The hash result would be different upon podTemplateSpec API changes
//     (e.g. the addition of a new field will cause the hash code to change)
//  2. The deployment template won't have hash labels
func EqualIgnoreHash(template1, template2 *v1.PodTemplateSpec) bool {
	t1Copy := template1.DeepCopy()
	t2Copy := template2.DeepCopy()
	// Remove hash labels from template.Labels before comparing
	delete(t1Copy.Labels, apps.DefaultDeploymentUniqueLabelKey)
	delete(t2Copy.Labels, apps.DefaultDeploymentUniqueLabelKey)
	return apiequality.Semantic.DeepEqual(t1Copy, t2Copy)
}

// FindNewReplicaSet returns the new RS this given deployment targets (the one with the same pod template).
func FindNewReplicaSet(deployment *apps.Deployment, rsList []*apps.ReplicaSet) *apps.ReplicaSet {
	sort.Sort(ReplicaSetsByCreationTimestamp(rsList))
	for i := range rsList {
		if EqualIgnoreHash(&rsList[i].Spec.Template, &deployment.Spec.Template) {
			// In rare cases, such as after cluster upgrades, Deployment may end up with
			// having more than one new ReplicaSets that have the same template as its template,
			// see https://github.com/kubernetes/kubernetes/issues/40415
			// We deterministically choose the oldest new ReplicaSet.
			return rsList[i]
		}
	}
	// new ReplicaSet does not exist.
	return nil
}

// FindOldReplicaSets returns the old replica sets targeted by the given Deployment, with the given slice of RSes.
// Note that the first set of old replica sets doesn't include the ones with no pods, and the second set of old replica sets include all old replica sets.
func FindOldReplicaSets(deployment *apps.Deployment, rsList []*apps.ReplicaSet) ([]*apps.ReplicaSet, []*apps.ReplicaSet) {
	var requiredRSs []*apps.ReplicaSet
	var allRSs []*apps.ReplicaSet
	newRS := FindNewReplicaSet(deployment, rsList)
	for _, rs := range rsList {
		// Filter out new replica set
		if newRS != nil && rs.UID == newRS.UID {
			continue
		}
		allRSs = append(allRSs, rs)
		if *(rs.Spec.Replicas) != 0 {
			requiredRSs = append(requiredRSs, rs)
		}
	}
	return requiredRSs, allRSs
}

// GetReplicaCountForReplicaSets returns the sum of Replicas of the given replica sets.
func GetReplicaCountForReplicaSets(replicaSets []*apps.ReplicaSet) int32 {
	totalReplicas := int32(0)
	for _, rs := range replicaSets {
		if rs != nil {
			totalReplicas += *(rs.Spec.Replicas)
		}
	}
	return totalReplicas
}

// GetActualReplicaCountForReplicaSets returns the sum of actual replicas of the given replica sets.
func GetActualReplicaCountForReplicaSets(replicaSets []*apps.ReplicaSet) int32 {
	totalActualReplicas := int32(0)
	for _, rs := range replicaSets {
		if rs != nil {
			totalActualReplicas += rs.Status.Replicas
		}
	}
	return totalActualReplicas
}

// GetReadyReplicaCountForReplicaSets returns the number of ready pods corresponding to the given replica sets.
func GetReadyReplicaCountForReplicaSets(replicaSets []*apps.ReplicaSet) int32 {
	totalReadyReplicas := int32(0)
	for _, rs := range replicaSets {
		if rs != nil {
			totalReadyReplicas += rs.Status.ReadyReplicas
		}
	}
	return totalReadyReplicas
}

// GetAvailableReplicaCountForReplicaSets returns the number of available pods corresponding to the given replica sets.
func GetAvailableReplicaCountForReplicaSets(replicaSets []*apps.ReplicaSet) int32 {
	totalAvailableReplicas := int32(0)
	for _, rs := range replicaSets {
		if rs != nil {
			totalAvailableReplicas += rs.Status.AvailableReplicas
		}
	}
	return totalAvailableReplicas
}

// IsRollingUpdate returns true if the strategy type is a rolling update.
func IsRollingUpdate(_ *apps.Deployment) bool {
	return true
}

// DeploymentComplete considers a deployment to be complete once all of its desired replicas
// are updated and available, and no old pods are running.
func DeploymentComplete(deployment *apps.Deployment, newStatus *apps.DeploymentStatus) bool {
	return newStatus.UpdatedReplicas == *(deployment.Spec.Replicas) &&
		newStatus.Replicas == *(deployment.Spec.Replicas) &&
		newStatus.AvailableReplicas == *(deployment.Spec.Replicas) &&
		newStatus.ObservedGeneration >= deployment.Generation
}

// DeploymentProgressing reports progress for a deployment. Progress is estimated by comparing the
// current with the new status of the deployment that the controller is observing. More specifically,
// when new pods are scaled up or become ready or available, or old pods are scaled down, then we
// consider the deployment is progressing.
func DeploymentProgressing(deployment *apps.Deployment, newStatus *apps.DeploymentStatus) bool {
	oldStatus := deployment.Status

	// Old replicas that need to be scaled down
	oldStatusOldReplicas := oldStatus.Replicas - oldStatus.UpdatedReplicas
	newStatusOldReplicas := newStatus.Replicas - newStatus.UpdatedReplicas

	return (newStatus.UpdatedReplicas > oldStatus.UpdatedReplicas) ||
		(newStatusOldReplicas < oldStatusOldReplicas) ||
		newStatus.ReadyReplicas > deployment.Status.ReadyReplicas ||
		newStatus.AvailableReplicas > deployment.Status.AvailableReplicas
}

// used for unit testing
var nowFn = func() time.Time { return time.Now() }

// DeploymentTimedOut considers a deployment to have timed out once its condition that reports progress
// is older than progressDeadlineSeconds or a Progressing condition with a TimedOutReason reason already
// exists.
func DeploymentTimedOut(deployment *apps.Deployment, newStatus *apps.DeploymentStatus) bool {
	if !HasProgressDeadline(deployment) {
		return false
	}

	// Look for the Progressing condition. If it doesn't exist, we have no base to estimate progress.
	// If it's already set with a TimedOutReason reason, we have already timed out, no need to check
	// again.
	condition := GetDeploymentCondition(*newStatus, apps.DeploymentProgressing)
	if condition == nil {
		return false
	}
	// If the previous condition has been a successful rollout then we shouldn't try to
	// estimate any progress. Scenario:
	//
	// * progressDeadlineSeconds is smaller than the difference between now and the time
	//   the last rollout finished in the past.
	// * the creation of a new ReplicaSet triggers a resync of the Deployment prior to the
	//   cached copy of the Deployment getting updated with the status.condition that indicates
	//   the creation of the new ReplicaSet.
	//
	// The Deployment will be resynced and eventually its Progressing condition will catch
	// up with the state of the world.
	if condition.Reason == NewRSAvailableReason {
		return false
	}
	if condition.Reason == TimedOutReason {
		return true
	}

	// Look at the difference in seconds between now and the last time we reported any
	// progress or tried to create a replica set, or resumed a paused deployment and
	// compare against progressDeadlineSeconds.
	from := condition.LastUpdateTime
	now := nowFn()
	delta := time.Duration(*deployment.Spec.ProgressDeadlineSeconds) * time.Second
	timedOut := from.Add(delta).Before(now)

	klog.V(4).Infof("Deployment %q timed out (%t) [last progress check: %v - now: %v]", deployment.Name, timedOut, from, now)
	return timedOut
}

// NewRSNewReplicas calculates the number of replicas a deployment's new RS should have.
// When one of the followings is true, we're rolling out the deployment; otherwise, we're scaling it.
// 1) The new RS is saturated: newRS's replicas == deployment's replicas
// 2) Max number of pods allowed is reached: deployment's replicas + maxSurge == all RSs' replicas
func NewRSNewReplicas(deployment *apps.Deployment, allRSs []*apps.ReplicaSet, newRS *apps.ReplicaSet, strategy *rolloutsv1alpha1.DeploymentStrategy) (int32, error) {
	// Find the total number of pods
	currentPodCount := GetReplicaCountForReplicaSets(allRSs)
	switch {
	case currentPodCount > *newRS.Spec.Replicas:
		// Do not scale down due to partition settings.
		scaleUpLimit := NewRSReplicasLimit(strategy.Partition, deployment)
		if *newRS.Spec.Replicas >= scaleUpLimit {
			// Cannot scale up.
			return *(newRS.Spec.Replicas), nil
		}
		// Do not scale up due to exceeded current replicas.
		maxTotalPods := *(deployment.Spec.Replicas) + MaxSurge(deployment, strategy)
		if currentPodCount >= maxTotalPods {
			// Cannot scale up.
			return *(newRS.Spec.Replicas), nil
		}
		// Scale up.
		scaleUpCount := maxTotalPods - currentPodCount
		// Do not exceed the number of desired replicas.
		scaleUpCount = int32(integer.IntMin(int(scaleUpCount), int(*(deployment.Spec.Replicas)-*(newRS.Spec.Replicas))))
		// Do not exceed the number of partition replicas.
		return integer.Int32Min(*(newRS.Spec.Replicas)+scaleUpCount, scaleUpLimit), nil
	default:
		// If there is ONLY ONE active replica set, just be in line with deployment replicas.
		return *(deployment.Spec.Replicas), nil
	}
}

// IsSaturated checks if the new replica set is saturated by comparing its size with its deployment size.
// Both the deployment and the replica set have to believe this replica set can own all of the desired
// replicas in the deployment and the annotation helps in achieving that. All pods of the ReplicaSet
// need to be available.
func IsSaturated(deployment *apps.Deployment, rs *apps.ReplicaSet) bool {
	if rs == nil {
		return false
	}
	desiredString := rs.Annotations[ReplicasAnnotation]
	desired, err := strconv.Atoi(desiredString)
	if err != nil {
		return false
	}
	return *(rs.Spec.Replicas) == *(deployment.Spec.Replicas) &&
		int32(desired) == *(deployment.Spec.Replicas) &&
		rs.Status.AvailableReplicas == *(deployment.Spec.Replicas)
}

// ResolveFenceposts resolves both maxSurge and maxUnavailable. This needs to happen in one
// step. For example:
//
// 2 desired, max unavailable 1%, surge 0% - should scale old(-1), then new(+1), then old(-1), then new(+1)
// 1 desired, max unavailable 1%, surge 0% - should scale old(-1), then new(+1)
// 2 desired, max unavailable 25%, surge 1% - should scale new(+1), then old(-1), then new(+1), then old(-1)
// 1 desired, max unavailable 25%, surge 1% - should scale new(+1), then old(-1)
// 2 desired, max unavailable 0%, surge 1% - should scale new(+1), then old(-1), then new(+1), then old(-1)
// 1 desired, max unavailable 0%, surge 1% - should scale new(+1), then old(-1)
func ResolveFenceposts(maxSurge, maxUnavailable *intstrutil.IntOrString, desired int32) (int32, int32, error) {
	surge, err := intstrutil.GetScaledValueFromIntOrPercent(intstrutil.ValueOrDefault(maxSurge, intstrutil.FromInt(0)), int(desired), true)
	if err != nil {
		return 0, 0, err
	}
	unavailable, err := intstrutil.GetScaledValueFromIntOrPercent(intstrutil.ValueOrDefault(maxUnavailable, intstrutil.FromInt(0)), int(desired), false)
	if err != nil {
		return 0, 0, err
	}

	if surge == 0 && unavailable == 0 {
		// Validation should never allow the user to explicitly use zero values for both maxSurge
		// maxUnavailable. Due to rounding down maxUnavailable though, it may resolve to zero.
		// If both fenceposts resolve to zero, then we should set maxUnavailable to 1 on the
		// theory that surge might not work due to quota.
		unavailable = 1
	}

	return int32(surge), int32(unavailable), nil
}

// HasProgressDeadline checks if the Deployment d is expected to surface the reason
// "ProgressDeadlineExceeded" when the Deployment progress takes longer than expected time.
func HasProgressDeadline(d *apps.Deployment) bool {
	return d.Spec.ProgressDeadlineSeconds != nil && *d.Spec.ProgressDeadlineSeconds != math.MaxInt32
}

// HasRevisionHistoryLimit checks if the Deployment d is expected to keep a specified number of
// old replicaSets. These replicaSets are mainly kept with the purpose of rollback.
// The RevisionHistoryLimit can start from 0 (no retained replicasSet). When set to math.MaxInt32,
// the Deployment will keep all revisions.
func HasRevisionHistoryLimit(d *apps.Deployment) bool {
	return d.Spec.RevisionHistoryLimit != nil && *d.Spec.RevisionHistoryLimit != math.MaxInt32
}

// ReplicaSetsByRevision sorts a list of ReplicaSet by revision, using their creation timestamp or name as a tie breaker.
// By using the creation timestamp, this sorts from old to new replica sets.
type ReplicaSetsByRevision []*apps.ReplicaSet

func (o ReplicaSetsByRevision) Len() int      { return len(o) }
func (o ReplicaSetsByRevision) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsByRevision) Less(i, j int) bool {
	revision1, err1 := Revision(o[i])
	revision2, err2 := Revision(o[j])
	if err1 != nil || err2 != nil || revision1 == revision2 {
		return ReplicaSetsByCreationTimestamp(o).Less(i, j)
	}
	return revision1 < revision2
}

/*
	**** Copied from "k8s.io/kubernetes/pkg/controller/controller_utils.go" ****
	-------------------------------- BEGIN --------------------------------------
*/

// ReplicaSetsBySmallerRevision sorts a list of ReplicaSet by revision in desc, using their creation timestamp or name as a tie breaker.
// By using the creation timestamp, this sorts from old to new replica sets.
type ReplicaSetsBySmallerRevision []*apps.ReplicaSet

func (o ReplicaSetsBySmallerRevision) Len() int      { return len(o) }
func (o ReplicaSetsBySmallerRevision) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsBySmallerRevision) Less(i, j int) bool {
	revision1, err1 := Revision(o[i])
	revision2, err2 := Revision(o[j])
	if err1 != nil || err2 != nil || revision1 == revision2 {
		return ReplicaSetsByCreationTimestamp(o).Less(i, j)
	}
	return revision1 > revision2
}

// FilterActiveReplicaSets returns replica sets that have (or at least ought to have) pods.
func FilterActiveReplicaSets(replicaSets []*apps.ReplicaSet) []*apps.ReplicaSet {
	activeFilter := func(rs *apps.ReplicaSet) bool {
		return rs != nil && *(rs.Spec.Replicas) > 0
	}
	return FilterReplicaSets(replicaSets, activeFilter)
}

type filterRS func(rs *apps.ReplicaSet) bool

// FilterReplicaSets returns replica sets that are filtered by filterFn (all returned ones should match filterFn).
func FilterReplicaSets(RSes []*apps.ReplicaSet, filterFn filterRS) []*apps.ReplicaSet {
	var filtered []*apps.ReplicaSet
	for i := range RSes {
		if filterFn(RSes[i]) {
			filtered = append(filtered, RSes[i])
		}
	}
	return filtered
}

// PodKey returns a key unique to the given pod within a cluster.
// It's used so we consistently use the same key scheme in this module.
// It does exactly what cache.MetaNamespaceKeyFunc would have done
// except there's not possibility for error since we know the exact type.
func PodKey(pod *v1.Pod) string {
	return fmt.Sprintf("%v/%v", pod.Namespace, pod.Name)
}

// ControllersByCreationTimestamp sorts a list of ReplicationControllers by creation timestamp, using their names as a tie breaker.
type ControllersByCreationTimestamp []*v1.ReplicationController

func (o ControllersByCreationTimestamp) Len() int      { return len(o) }
func (o ControllersByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ControllersByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(&o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(&o[j].CreationTimestamp)
}

// ReplicaSetsByCreationTimestamp sorts a list of ReplicaSet by creation timestamp, using their names as a tie breaker.
type ReplicaSetsByCreationTimestamp []*apps.ReplicaSet

func (o ReplicaSetsByCreationTimestamp) Len() int      { return len(o) }
func (o ReplicaSetsByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(&o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(&o[j].CreationTimestamp)
}

// ReplicaSetsBySizeOlder sorts a list of ReplicaSet by size in descending order, using their creation timestamp or name as a tie breaker.
// By using the creation timestamp, this sorts from old to new replica sets.
type ReplicaSetsBySizeOlder []*apps.ReplicaSet

func (o ReplicaSetsBySizeOlder) Len() int      { return len(o) }
func (o ReplicaSetsBySizeOlder) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsBySizeOlder) Less(i, j int) bool {
	if *(o[i].Spec.Replicas) == *(o[j].Spec.Replicas) {
		return ReplicaSetsByCreationTimestamp(o).Less(i, j)
	}
	return *(o[i].Spec.Replicas) > *(o[j].Spec.Replicas)
}

// ReplicaSetsBySizeNewer sorts a list of ReplicaSet by size in descending order, using their creation timestamp or name as a tie breaker.
// By using the creation timestamp, this sorts from new to old replica sets.
type ReplicaSetsBySizeNewer []*apps.ReplicaSet

func (o ReplicaSetsBySizeNewer) Len() int      { return len(o) }
func (o ReplicaSetsBySizeNewer) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsBySizeNewer) Less(i, j int) bool {
	if *(o[i].Spec.Replicas) == *(o[j].Spec.Replicas) {
		return ReplicaSetsByCreationTimestamp(o).Less(j, i)
	}
	return *(o[i].Spec.Replicas) > *(o[j].Spec.Replicas)
}

/*
	--------------------------------- END ---------------------------------------
	**** Copied from "k8s.io/kubernetes/pkg/controller/controller_utils.go" ****
*/

// IsUnderRolloutControl return true if this deployment should be controlled by our controller.
func IsUnderRolloutControl(deployment *apps.Deployment) bool {
	if deployment.Annotations[util.BatchReleaseControlAnnotation] == "" {
		return false
	}
	if deployment.Spec.Strategy.Type != apps.RecreateDeploymentStrategyType {
		return false
	}
	return deployment.Spec.Paused
}

// NewRSReplicasLimit return a limited replicas of new RS calculated via partition.
func NewRSReplicasLimit(partition intstrutil.IntOrString, deployment *apps.Deployment) int32 {
	replicas := int(*deployment.Spec.Replicas)
	replicaLimit, _ := intstrutil.GetScaledValueFromIntOrPercent(&partition, replicas, true)
	replicaLimit = integer.IntMax(integer.IntMin(replicaLimit, replicas), 0)
	if replicas > 1 && partition.Type == intstrutil.String && partition.String() != "100%" {
		replicaLimit = integer.IntMin(replicaLimit, replicas-1)
	}
	return int32(replicaLimit)
}

// DeploymentRolloutSatisfied return nil if deployment has satisfied partition and replicas,
// or will return an error.
func DeploymentRolloutSatisfied(deployment *apps.Deployment, partition intstrutil.IntOrString) error {
	if deployment.Status.ObservedGeneration < deployment.Generation {
		return fmt.Errorf("deployment %v observed generation %d less than generation %d",
			klog.KObj(deployment), deployment.Status.ObservedGeneration, deployment.Generation)
	}
	if deployment.Status.Replicas != *(deployment.Spec.Replicas) {
		return fmt.Errorf("deployment %v status replicas %d not equals to replicas %d",
			klog.KObj(deployment), deployment.Status.Replicas, *deployment.Spec.Replicas)
	}
	newRSReplicasLimit := NewRSReplicasLimit(partition, deployment)
	if deployment.Status.UpdatedReplicas < newRSReplicasLimit {
		return fmt.Errorf("deployment %v updated replicas %d less than partition %d",
			klog.KObj(deployment), deployment.Status.UpdatedReplicas, newRSReplicasLimit)
	}
	return nil
}

// NewRSReplicasLowerBound ensure that newReplicasLowerBound is greater than 0 when create newRS
// unless deployment is 0 or MaxSurge > 0, this is because if we set new replicas as 0, the native
// deployment controller will flight with ours.
func NewRSReplicasLowerBound(deployment *apps.Deployment, strategy *rolloutsv1alpha1.DeploymentStrategy) int32 {
	if MaxSurge(deployment, strategy) > 0 {
		return int32(0)
	}
	return integer.Int32Min(int32(1), *deployment.Spec.Replicas)
}
