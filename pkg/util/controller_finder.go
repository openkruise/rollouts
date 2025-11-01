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

package util

import (
	"context"
	"fmt"
	"sort"
	"strings"

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	rolloutv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
	utilclient "github.com/openkruise/rollouts/pkg/util/client"
)

// Workload is used to return (controller, scale, selector) fields from the
// controller finder functions.
type Workload struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	// replicas
	Replicas int32
	// stable revision
	StableRevision string
	// canary revision
	CanaryRevision string
	// pod template hash is used as service selector hash
	PodTemplateHash string
	// Revision hash key
	RevisionLabelKey string

	// Is it in rollback phase
	IsInRollback bool
	// indicate whether the workload can enter the rollout process
	// 1. workload.Spec.Paused = true
	// 2. the Deployment is not in a stable version (only one version of RS)
	InRolloutProgressing bool

	// whether the status consistent with the spec
	// workload.generation == status.observedGeneration
	IsStatusConsistent bool
}

// ControllerFinderFunc is a function type that maps a pod to a list of
// controllers and their scale.
type ControllerFinderFunc func(namespace string, ref *rolloutv1beta1.ObjectRef) (*Workload, error)

type ControllerFinder struct {
	client.Client
}

func NewControllerFinder(c client.Client) *ControllerFinder {
	return &ControllerFinder{
		Client: c,
	}
}

// +kubebuilder:rbac:groups=apps.kruise.io,resources=clonesets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.kruise.io,resources=clonesets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=replicasets/status,verbs=get;update;patch

func (r *ControllerFinder) GetWorkloadForRef(rollout *rolloutv1beta1.Rollout) (*Workload, error) {
	workloadRef := rollout.Spec.WorkloadRef
	var finders []ControllerFinderFunc
	switch rollout.Spec.Strategy.GetRollingStyle() {
	case rolloutv1beta1.CanaryRollingStyle:
		finders = append(r.canaryStyleFinders(), r.partitionStyleFinders()...)
	case rolloutv1beta1.BlueGreenRollingStyle:
		finders = r.bluegreenStyleFinders()
	default:
		finders = r.partitionStyleFinders()
	}
	for _, finder := range finders {
		workload, err := finder(rollout.Namespace, &workloadRef)
		if workload != nil || err != nil {
			return workload, err
		}
	}

	klog.Errorf("Failed to get workload for rollout %v due to no correct finders", klog.KObj(rollout))
	return nil, nil
}

func (r *ControllerFinder) canaryStyleFinders() []ControllerFinderFunc {
	return []ControllerFinderFunc{r.getDeployment, r.getLeaderWorkerSet}
}

// Note: getStatefulSetLikeWorkload is placed last because it has broader matching criteria.
// If placed earlier in the chain, it may incorrectly match other workload types (e.g., DaemonSets)
// and prevent their specific finders from being reached.
func (r *ControllerFinder) partitionStyleFinders() []ControllerFinderFunc {
	return []ControllerFinderFunc{r.getKruiseCloneSet, r.getAdvancedDeployment, r.getKruiseDaemonSet, r.getNativeDaemonSet, r.getStatefulSetLikeWorkload}
}

func (r *ControllerFinder) bluegreenStyleFinders() []ControllerFinderFunc {
	return []ControllerFinderFunc{r.getKruiseCloneSet, r.getAdvancedDeployment}
}

var (
	ControllerKindRS           = apps.SchemeGroupVersion.WithKind("ReplicaSet")
	ControllerKindDep          = apps.SchemeGroupVersion.WithKind("Deployment")
	ControllerKindSts          = apps.SchemeGroupVersion.WithKind("StatefulSet")
	ControllerKindDS           = apps.SchemeGroupVersion.WithKind("DaemonSet") // Add this for native DaemonSet
	ControllerKruiseKindCS     = appsv1alpha1.SchemeGroupVersion.WithKind("CloneSet")
	ControllerKruiseKindDS     = appsv1alpha1.SchemeGroupVersion.WithKind("DaemonSet")
	ControllerKruiseKindSts    = appsv1beta1.SchemeGroupVersion.WithKind("StatefulSet")
	ControllerKruiseOldKindSts = appsv1alpha1.SchemeGroupVersion.WithKind("StatefulSet")
	// LeaderWorkerSet from kubernetes-sigs/lws
	// API Group: workload.kubernetes.io, Version: v1alpha1, Kind: LeaderWorkerSet
	ControllerLWSKind = schema.GroupVersionKind{Group: "workload.kubernetes.io", Version: "v1alpha1", Kind: "LeaderWorkerSet"}
)

// getKruiseCloneSet returns the kruise cloneSet referenced by the provided controllerRef.
func (r *ControllerFinder) getKruiseCloneSet(namespace string, ref *rolloutv1beta1.ObjectRef) (*Workload, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, ControllerKruiseKindCS.Kind, []string{ControllerKruiseKindCS.Group})
	if !ok {
		return nil, nil
	}
	cloneSet := &appsv1alpha1.CloneSet{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: ref.Name}, cloneSet)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if cloneSet.Generation != cloneSet.Status.ObservedGeneration {
		return &Workload{IsStatusConsistent: false}, nil
	}
	workload := &Workload{
		RevisionLabelKey:   apps.DefaultDeploymentUniqueLabelKey,
		StableRevision:     cloneSet.Status.CurrentRevision[strings.LastIndex(cloneSet.Status.CurrentRevision, "-")+1:],
		CanaryRevision:     cloneSet.Status.UpdateRevision[strings.LastIndex(cloneSet.Status.UpdateRevision, "-")+1:],
		ObjectMeta:         cloneSet.ObjectMeta,
		TypeMeta:           cloneSet.TypeMeta,
		Replicas:           *cloneSet.Spec.Replicas,
		PodTemplateHash:    cloneSet.Status.UpdateRevision[strings.LastIndex(cloneSet.Status.UpdateRevision, "-")+1:],
		IsStatusConsistent: true,
	}

	// not in rollout progressing
	if _, ok = workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}
	// in rollout progressing
	workload.InRolloutProgressing = true
	// Is it in rollback phase
	if cloneSet.Status.CurrentRevision == cloneSet.Status.UpdateRevision && cloneSet.Status.UpdatedReplicas != cloneSet.Status.Replicas {
		workload.IsInRollback = true
	}
	return workload, nil
}

func (r *ControllerFinder) getKruiseDaemonSet(namespace string, ref *rolloutv1beta1.ObjectRef) (*Workload, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, ControllerKruiseKindDS.Kind, []string{ControllerKruiseKindDS.Group})
	if !ok {
		return nil, nil
	}
	daemonSet := &appsv1alpha1.DaemonSet{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: ref.Name}, daemonSet)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if daemonSet.Generation != daemonSet.Status.ObservedGeneration {
		return &Workload{IsStatusConsistent: false}, nil
	}
	workload := &Workload{
		RevisionLabelKey: apps.DefaultDeploymentUniqueLabelKey,
		//StableRevision:     daemonSet.Status.CurrentRevision[strings.LastIndex(cloneSet.Status.CurrentRevision, "-")+1:],
		CanaryRevision:     daemonSet.Status.DaemonSetHash[strings.LastIndex(daemonSet.Status.DaemonSetHash, "-")+1:],
		ObjectMeta:         daemonSet.ObjectMeta,
		TypeMeta:           daemonSet.TypeMeta,
		Replicas:           daemonSet.Status.DesiredNumberScheduled,
		PodTemplateHash:    daemonSet.Status.DaemonSetHash[strings.LastIndex(daemonSet.Status.DaemonSetHash, "-")+1:],
		IsStatusConsistent: true,
	}

	// not in rollout progressing
	if _, ok = workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}
	// in rollout progressing
	workload.InRolloutProgressing = true
	// Is it in rollback phase
	// has no currentRevision
	// if daemonSet.Status.CurrentRevision == cloneSet.Status.UpdateRevision && cloneSet.Status.UpdatedReplicas != cloneSet.Status.Replicas {
	// 	workload.IsInRollback = true
	// }
	return workload, nil
}

// getNativeDaemonSet returns the native DaemonSet referenced by the provided controllerRef.
func (r *ControllerFinder) getNativeDaemonSet(namespace string, ref *rolloutv1beta1.ObjectRef) (*Workload, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, ControllerKindDS.Kind, []string{ControllerKindDS.Group})
	if !ok {
		return nil, nil
	}
	daemonSet := &apps.DaemonSet{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: ref.Name}, daemonSet)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if daemonSet.Generation != daemonSet.Status.ObservedGeneration {
		return &Workload{IsStatusConsistent: false}, nil
	}

	workload := &Workload{
		RevisionLabelKey:   apps.ControllerRevisionHashLabelKey, // Use ControllerRevision hash label key
		ObjectMeta:         daemonSet.ObjectMeta,
		TypeMeta:           daemonSet.TypeMeta,
		Replicas:           daemonSet.Status.DesiredNumberScheduled,
		IsStatusConsistent: true,
	}

	// not in rollout progressing
	if _, ok := workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}

	// Get ControllerRevisions for this DaemonSet
	revisions, err := r.getControllerRevisionsForDaemonSet(daemonSet)
	if err != nil {
		return nil, err
	}

	// Set CanaryRevision and StableRevision based on ControllerRevisions
	if len(revisions) > 0 {
		// CanaryRevision: revision with the highest number (newest)
		latestRevision := revisions[0]
		if hash, exists := latestRevision.Labels[apps.ControllerRevisionHashLabelKey]; exists {
			workload.CanaryRevision = hash
			workload.PodTemplateHash = hash // Use the same hash for pod template
		}

		// StableRevision: revision with the second-highest number (previous)
		if len(revisions) > 1 {
			secondLatestRevision := revisions[1]
			if hash, exists := secondLatestRevision.Labels[apps.ControllerRevisionHashLabelKey]; exists {
				workload.StableRevision = hash
			}
		} else {
			// If there's only one revision, it's both stable and canary
			workload.StableRevision = workload.CanaryRevision
		}
	}

	// in rollout progressing
	workload.InRolloutProgressing = true

	// For rollback detection in native DaemonSet:
	// If stable revision equals canary revision, it means we're rolling back
	if workload.StableRevision != "" && workload.StableRevision == workload.CanaryRevision {
		workload.IsInRollback = true
	}

	// Update DaemonSet annotations for stable and canary revisions
	err = r.patchDaemonSetRevisionAnnotations(daemonSet, workload.StableRevision, workload.CanaryRevision)
	if err != nil {
		return nil, err
	}

	return workload, nil
}

// getPartitionStyleDeployment returns the Advanced Deployment referenced by the provided controllerRef.
func (r *ControllerFinder) getAdvancedDeployment(namespace string, ref *rolloutv1beta1.ObjectRef) (*Workload, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, ControllerKindDep.Kind, []string{ControllerKindDep.Group})
	if !ok {
		return nil, nil
	}
	deployment := &apps.Deployment{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: ref.Name}, deployment)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if deployment.Generation != deployment.Status.ObservedGeneration {
		return &Workload{IsStatusConsistent: false}, nil
	}

	stableRevision := deployment.Labels[rolloutv1alpha1.DeploymentStableRevisionLabel]

	workload := &Workload{
		RevisionLabelKey:   apps.DefaultDeploymentUniqueLabelKey,
		StableRevision:     stableRevision,
		CanaryRevision:     ComputeHash(&deployment.Spec.Template, nil),
		ObjectMeta:         deployment.ObjectMeta,
		TypeMeta:           deployment.TypeMeta,
		Replicas:           *deployment.Spec.Replicas,
		IsStatusConsistent: true,
	}

	// not in rollout progressing
	if _, ok = workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}
	// set pod template hash for canary
	rss, err := r.GetReplicaSetsForDeployment(deployment)
	if err != nil {
		return &Workload{IsStatusConsistent: false}, err
	}
	newRS, _ := FindCanaryAndStableReplicaSet(rss, deployment)
	if newRS != nil {
		workload.PodTemplateHash = newRS.Labels[apps.DefaultDeploymentUniqueLabelKey]
	}
	// in rolling back
	if workload.StableRevision != "" && workload.StableRevision == workload.PodTemplateHash {
		workload.IsInRollback = true
	}
	// in rollout progressing
	workload.InRolloutProgressing = true
	return workload, nil
}

// getDeployment returns the k8s native deployment referenced by the provided controllerRef.
func (r *ControllerFinder) getDeployment(namespace string, ref *rolloutv1beta1.ObjectRef) (*Workload, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, ControllerKindDep.Kind, []string{ControllerKindDep.Group})
	if !ok {
		return nil, nil
	}
	stable := &apps.Deployment{}
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: ref.Name}, stable)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if stable.Generation != stable.Status.ObservedGeneration {
		return &Workload{IsStatusConsistent: false}, nil
	}
	// stable replicaSet
	stableRs, err := r.GetDeploymentStableRs(stable)
	if err != nil || stableRs == nil {
		return &Workload{IsStatusConsistent: false}, err
	}

	workload := &Workload{
		ObjectMeta:         stable.ObjectMeta,
		TypeMeta:           stable.TypeMeta,
		Replicas:           *stable.Spec.Replicas,
		IsStatusConsistent: true,
		StableRevision:     stableRs.Labels[apps.DefaultDeploymentUniqueLabelKey],
		CanaryRevision:     ComputeHash(&stable.Spec.Template, nil),
		RevisionLabelKey:   apps.DefaultDeploymentUniqueLabelKey,
	}
	// not in rollout progressing
	if _, ok = workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}
	// in rollout progressing
	workload.InRolloutProgressing = true
	if EqualIgnoreHash(&stableRs.Spec.Template, &stable.Spec.Template) {
		workload.IsInRollback = true
		return workload, nil
	}
	// canary deployment
	canary, err := r.getLatestCanaryDeployment(stable)
	if err != nil || canary == nil {
		return workload, err
	}
	canaryRs, err := r.GetDeploymentStableRs(canary)
	if err != nil || canaryRs == nil {
		return workload, err
	}
	workload.PodTemplateHash = canaryRs.Labels[apps.DefaultDeploymentUniqueLabelKey]
	return workload, err
}

// getLeaderWorkerSet returns the LeaderWorkerSet referenced by the provided controllerRef.
// LeaderWorkerSet is used for AI inference workloads with leader-worker pattern.
func (r *ControllerFinder) getLeaderWorkerSet(namespace string, ref *rolloutv1beta1.ObjectRef) (*Workload, error) {
	// Verify this is a LeaderWorkerSet
	ok, _ := verifyGroupKind(ref, ControllerLWSKind.Kind, []string{ControllerLWSKind.Group})
	if !ok {
		return nil, nil
	}

	// Use unstructured to handle LWS since we may not have the typed client
	lws := &unstructured.Unstructured{}
	lws.SetGroupVersionKind(ControllerLWSKind)
	err := r.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: ref.Name}, lws)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// Check generation consistency
	observedGen, found, _ := unstructured.NestedInt64(lws.Object, "status", "observedGeneration")
	generation := lws.GetGeneration()
	if !found || generation != observedGen {
		return &Workload{IsStatusConsistent: false}, nil
	}

	// Extract replicas - LWS typically has replicas in spec
	replicas, found, _ := unstructured.NestedInt64(lws.Object, "spec", "replicas")
	if !found {
		replicas = 0
	}

	// Extract pod template for hash calculation
	templateObj, found, _ := unstructured.NestedMap(lws.Object, "spec", "template")
	if !found {
		return &Workload{IsStatusConsistent: false}, fmt.Errorf("spec.template not found in LeaderWorkerSet")
	}

	// Create pod template from unstructured
	podTemplate := &corev1.PodTemplateSpec{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(templateObj, podTemplate); err != nil {
		return &Workload{IsStatusConsistent: false}, fmt.Errorf("failed to convert template: %w", err)
	}

	canaryRevision := ComputeHash(podTemplate, nil)

	workload := &Workload{
		ObjectMeta: metav1.ObjectMeta{
			Name:        lws.GetName(),
			Namespace:   lws.GetNamespace(),
			UID:         lws.GetUID(),
			Labels:      lws.GetLabels(),
			Annotations: lws.GetAnnotations(),
		},
		TypeMeta: metav1.TypeMeta{
			APIVersion: lws.GetAPIVersion(),
			Kind:       lws.GetKind(),
		},
		Replicas:           int32(replicas),
		IsStatusConsistent: true,
		CanaryRevision:     canaryRevision,
		RevisionLabelKey:   "workload.kubernetes.io/template-hash", // Custom revision label key for LWS
	}

	// Try to extract stable revision from status if available
	if stableRev, found, _ := unstructured.NestedString(lws.Object, "status", "stableRevision"); found {
		workload.StableRevision = stableRev
	}

	// Check if in rollout progressing
	if _, ok := workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}

	workload.InRolloutProgressing = true
	// Check for rollback condition
	if workload.StableRevision != "" && workload.StableRevision == canaryRevision {
		workload.IsInRollback = true
	}

	return workload, nil
}

func (r *ControllerFinder) getStatefulSetLikeWorkload(namespace string, ref *rolloutv1beta1.ObjectRef) (*Workload, error) {
	if ref == nil {
		return nil, nil
	}

	key := types.NamespacedName{Name: ref.Name, Namespace: namespace}
	set := GetEmptyWorkloadObject(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
	if set == nil {
		return nil, nil
	}
	err := r.Get(context.TODO(), key, set)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	workloadInfo := ParseWorkload(set)
	if workloadInfo.Generation != workloadInfo.Status.ObservedGeneration {
		return &Workload{IsStatusConsistent: false}, nil
	}
	workload := &Workload{
		RevisionLabelKey:   apps.ControllerRevisionHashLabelKey,
		StableRevision:     workloadInfo.Status.StableRevision,
		CanaryRevision:     workloadInfo.Status.UpdateRevision,
		ObjectMeta:         workloadInfo.ObjectMeta,
		TypeMeta:           workloadInfo.TypeMeta,
		Replicas:           workloadInfo.Replicas,
		PodTemplateHash:    workloadInfo.Status.UpdateRevision,
		IsStatusConsistent: true,
	}

	// not in rollout progressing
	if _, ok := workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}
	// in rollout progressing
	workload.InRolloutProgressing = true
	if workloadInfo.Status.UpdateRevision == workloadInfo.Status.StableRevision && workloadInfo.Status.UpdatedReplicas != workloadInfo.Status.Replicas {
		workload.IsInRollback = true
	}
	return workload, nil
}

func (r *ControllerFinder) getLatestCanaryDeployment(stable *apps.Deployment) (*apps.Deployment, error) {
	canaryList := &apps.DeploymentList{}
	selector, _ := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{CanaryDeploymentLabel: stable.Name}})
	err := r.List(context.TODO(), canaryList, &client.ListOptions{LabelSelector: selector, Namespace: stable.Namespace}, utilclient.DisableDeepCopy)
	if err != nil {
		return nil, err
	} else if len(canaryList.Items) == 0 {
		return nil, nil
	}
	sort.Slice(canaryList.Items, func(i, j int) bool {
		return canaryList.Items[j].CreationTimestamp.Before(&canaryList.Items[i].CreationTimestamp)
	})
	for i := range canaryList.Items {
		obj := &canaryList.Items[i]
		if obj.DeletionTimestamp.IsZero() {
			return obj.DeepCopy(), nil
		}
	}
	return nil, nil
}

func (r *ControllerFinder) GetReplicaSetsForDeployment(obj *apps.Deployment) ([]*apps.ReplicaSet, error) {
	// List ReplicaSets owned by this Deployment
	rsList := &apps.ReplicaSetList{}
	selector, err := metav1.LabelSelectorAsSelector(obj.Spec.Selector)
	if err != nil {
		klog.Errorf("Deployment (%s/%s) get labelSelector failed: %s", obj.Namespace, obj.Name, err.Error())
		return nil, nil
	}
	err = r.List(context.TODO(), rsList, utilclient.DisableDeepCopy,
		&client.ListOptions{Namespace: obj.Namespace, LabelSelector: selector})
	if err != nil {
		return nil, err
	}

	var rss []*apps.ReplicaSet
	for i := range rsList.Items {
		rs := rsList.Items[i]
		if !rs.DeletionTimestamp.IsZero() || (rs.Spec.Replicas != nil && *rs.Spec.Replicas == 0) {
			continue
		}
		if ref := metav1.GetControllerOf(&rs); ref != nil {
			if ref.UID == obj.UID {
				rss = append(rss, &rs)
			}
		}
	}
	return rss, nil
}

func (r *ControllerFinder) GetDeploymentStableRs(obj *apps.Deployment) (*apps.ReplicaSet, error) {
	rss, err := r.GetReplicaSetsForDeployment(obj)
	if err != nil {
		return nil, err
	}
	if len(rss) == 0 {
		return nil, nil
	}
	// get oldest rs
	sort.Slice(rss, func(i, j int) bool {
		return rss[i].CreationTimestamp.Before(&rss[j].CreationTimestamp)
	})
	return rss[0], nil
}

func verifyGroupKind(ref *rolloutv1beta1.ObjectRef, expectedKind string, expectedGroups []string) (bool, error) {
	gv, err := schema.ParseGroupVersion(ref.APIVersion)
	if err != nil {
		return false, err
	}

	if ref.Kind != expectedKind {
		return false, nil
	}

	for _, group := range expectedGroups {
		if group == gv.Group {
			return true, nil
		}
	}

	return false, nil
}

// getControllerRevisionsForDaemonSet returns ControllerRevisions for the given DaemonSet,
// sorted by revision number in descending order (newest first)
func (r *ControllerFinder) getControllerRevisionsForDaemonSet(daemonSet *apps.DaemonSet) ([]*apps.ControllerRevision, error) {
	// List all ControllerRevisions in the same namespace
	revisionList := &apps.ControllerRevisionList{}

	err := r.List(context.TODO(), revisionList, utilclient.DisableDeepCopy,
		&client.ListOptions{Namespace: daemonSet.Namespace})
	if err != nil {
		return nil, err
	}

	var revisions []*apps.ControllerRevision
	for i := range revisionList.Items {
		revision := &revisionList.Items[i]
		// Check if this ControllerRevision is owned by the DaemonSet
		if ref := metav1.GetControllerOf(revision); ref != nil {
			if ref.Name == daemonSet.Name && ref.Kind == "DaemonSet" {
				revisions = append(revisions, revision)
			}
		}
	}

	// Sort by revision number in descending order (newest first)
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Revision > revisions[j].Revision
	})

	return revisions, nil
}

// patchDaemonSetRevisionAnnotations patches DaemonSet annotations for stable and canary revisions
func (r *ControllerFinder) patchDaemonSetRevisionAnnotations(daemonSet *apps.DaemonSet, stableRevision, canaryRevision string) error {

	annotationsToUpdate := make(map[string]string)

	// Check and collect canary revision annotation
	if canaryRevision != "" {
		if daemonSet.Annotations == nil {
			annotationsToUpdate[DaemonSetCanaryRevisionAnnotation] = canaryRevision
		} else if existingCanary, exists := daemonSet.Annotations[DaemonSetCanaryRevisionAnnotation]; !exists || existingCanary != canaryRevision {
			annotationsToUpdate[DaemonSetCanaryRevisionAnnotation] = canaryRevision
		}
	}

	// Check and collect stable revision annotation
	if stableRevision != "" {
		if daemonSet.Annotations == nil {
			annotationsToUpdate[DaemonSetStableRevisionAnnotation] = stableRevision
		} else if existingStable, exists := daemonSet.Annotations[DaemonSetStableRevisionAnnotation]; !exists || existingStable != stableRevision {
			annotationsToUpdate[DaemonSetStableRevisionAnnotation] = stableRevision
		}
	}

	// Patch the DaemonSet if annotations need to be changed
	if len(annotationsToUpdate) > 0 {
		patch := client.MergeFrom(daemonSet.DeepCopy())

		if daemonSet.Annotations == nil {
			daemonSet.Annotations = make(map[string]string)
		}

		for key, value := range annotationsToUpdate {
			daemonSet.Annotations[key] = value
		}

		return r.Patch(context.TODO(), daemonSet, patch)
	}

	return nil
}
