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
	"sort"
	"strings"

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Workload is used to return (controller, scale, selector) fields from the
// controller finder functions.
type Workload struct {
	metav1.ObjectMeta

	// replicas
	Replicas int32
	// stable revision
	StableRevision string
	// canary revision
	CanaryRevision string
	// pod template hash is used as service selector hash
	PodTemplateHash string
	// canary replicas
	CanaryReplicas int32
	// canary ready replicas
	CanaryReadyReplicas int32

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
type ControllerFinderFunc func(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*Workload, error)

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

func (r *ControllerFinder) GetWorkloadForRef(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*Workload, error) {
	for _, finder := range r.finders() {
		scale, err := finder(namespace, ref)
		if scale != nil || err != nil {
			return scale, err
		}
	}
	return nil, nil
}

func (r *ControllerFinder) finders() []ControllerFinderFunc {
	return []ControllerFinderFunc{r.getKruiseCloneSet, r.getDeployment, r.getStatefulSetLikeWorkload}
}

var (
	ControllerKindDep      = apps.SchemeGroupVersion.WithKind("Deployment")
	ControllerKindSts      = apps.SchemeGroupVersion.WithKind("StatefulSet")
	ControllerKruiseKindCS = appsv1alpha1.SchemeGroupVersion.WithKind("CloneSet")
)

// getKruiseCloneSet returns the kruise cloneSet referenced by the provided controllerRef.
func (r *ControllerFinder) getKruiseCloneSet(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*Workload, error) {
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
		StableRevision:      cloneSet.Status.CurrentRevision[strings.LastIndex(cloneSet.Status.CurrentRevision, "-")+1:],
		CanaryRevision:      cloneSet.Status.UpdateRevision[strings.LastIndex(cloneSet.Status.UpdateRevision, "-")+1:],
		CanaryReplicas:      cloneSet.Status.UpdatedReplicas,
		CanaryReadyReplicas: cloneSet.Status.UpdatedReadyReplicas,
		ObjectMeta:          cloneSet.ObjectMeta,
		Replicas:            *cloneSet.Spec.Replicas,
		PodTemplateHash:     cloneSet.Status.UpdateRevision[strings.LastIndex(cloneSet.Status.UpdateRevision, "-")+1:],
		IsStatusConsistent:  true,
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

// getDeployment returns the k8s native deployment referenced by the provided controllerRef.
func (r *ControllerFinder) getDeployment(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*Workload, error) {
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
	stableRs, err := r.getDeploymentStableRs(stable)
	if err != nil || stableRs == nil {
		return &Workload{IsStatusConsistent: false}, err
	}

	workload := &Workload{
		ObjectMeta:         stable.ObjectMeta,
		Replicas:           *stable.Spec.Replicas,
		IsStatusConsistent: true,
		StableRevision:     stableRs.Labels[apps.DefaultDeploymentUniqueLabelKey],
		CanaryRevision:     ComputeHash(&stable.Spec.Template, nil),
	}
	// not in rollout progressing
	if _, ok = workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}

	// in rollout progressing
	workload.InRolloutProgressing = true
	// workload is continuous release, indicates rollback(v1 -> v2 -> v1)
	// delete auto-generated labels
	delete(stableRs.Spec.Template.Labels, apps.DefaultDeploymentUniqueLabelKey)
	if EqualIgnoreHash(&stableRs.Spec.Template, &stable.Spec.Template) {
		workload.IsInRollback = true
		return workload, nil
	}

	// canary deployment
	canary, err := r.getLatestCanaryDeployment(stable)
	if err != nil || canary == nil {
		return workload, err
	}
	workload.CanaryReplicas = canary.Status.Replicas
	workload.CanaryReadyReplicas = canary.Status.ReadyReplicas
	canaryRs, err := r.getDeploymentStableRs(canary)
	if err != nil || canaryRs == nil {
		return workload, err
	}
	workload.PodTemplateHash = canaryRs.Labels[apps.DefaultDeploymentUniqueLabelKey]
	return workload, err
}

func (r *ControllerFinder) getStatefulSetLikeWorkload(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*Workload, error) {
	if ref == nil || ref.Kind != AStatefulSetGVK.Kind {
		return nil, nil
	}

	unifiedObject := &unstructured.Unstructured{}
	unifiedObjectKey := types.NamespacedName{Name: ref.Name, Namespace: namespace}
	unifiedObject.SetGroupVersionKind(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
	err := r.Get(context.TODO(), unifiedObjectKey, unifiedObject)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	workloadInfo := ParseWorkloadInfo(unifiedObject, unifiedObjectKey)
	if workloadInfo.Metadata.Generation != workloadInfo.Status.ObservedGeneration {
		return &Workload{IsStatusConsistent: false}, nil
	}
	workload := &Workload{
		StableRevision:      workloadInfo.Status.StableRevision,
		CanaryRevision:      workloadInfo.Status.UpdateRevision,
		CanaryReplicas:      workloadInfo.Status.UpdatedReplicas,
		CanaryReadyReplicas: workloadInfo.Status.UpdatedReadyReplicas,
		ObjectMeta:          *workloadInfo.Metadata,
		Replicas:            *workloadInfo.Replicas,
		PodTemplateHash:     workloadInfo.Status.UpdateRevision,
		IsStatusConsistent:  true,
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
	err := r.List(context.TODO(), canaryList, &client.ListOptions{LabelSelector: selector})
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
			return obj, nil
		}
	}
	return nil, nil
}

func (r *ControllerFinder) GetReplicaSetsForDeployment(obj *apps.Deployment) ([]apps.ReplicaSet, error) {
	// List ReplicaSets owned by this Deployment
	rsList := &apps.ReplicaSetList{}
	selector, err := metav1.LabelSelectorAsSelector(obj.Spec.Selector)
	if err != nil {
		klog.Errorf("Deployment (%s/%s) get labelSelector failed: %s", obj.Namespace, obj.Name, err.Error())
		return nil, nil
	}
	err = r.List(context.TODO(), rsList, &client.ListOptions{Namespace: obj.Namespace, LabelSelector: selector})
	if err != nil {
		return nil, err
	}

	rss := make([]apps.ReplicaSet, 0)
	for i := range rsList.Items {
		rs := rsList.Items[i]
		if !rs.DeletionTimestamp.IsZero() || (rs.Spec.Replicas != nil && *rs.Spec.Replicas == 0) {
			continue
		}
		if ref := metav1.GetControllerOf(&rs); ref != nil {
			if ref.UID == obj.UID {
				rss = append(rss, rs)
			}
		}
	}
	return rss, nil
}

func (r *ControllerFinder) getDeploymentStableRs(obj *apps.Deployment) (*apps.ReplicaSet, error) {
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
	return &rss[0], nil
}

func verifyGroupKind(ref *rolloutv1alpha1.WorkloadRef, expectedKind string, expectedGroups []string) (bool, error) {
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
