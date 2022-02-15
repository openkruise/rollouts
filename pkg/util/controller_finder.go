/*
Copyright 2021.

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

	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Workload is used to return (controller, scale, selector) fields from the
// controller finder functions.
type Workload struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	// stable revision
	StableRevision string
	// canary revision
	CanaryRevision string
	// canary replicas
	CanaryReplicas int32
	// canary ready replicas
	CanaryReadyReplicas int32

	// indicate whether the workload can enter the rollout process
	// 1. workload.Spec.Paused = true
	// 2. the Deployment is not in a stable version (only one version of RS)
	// 3. workload.Status.UpdateReplicas = 0
	InRolloutProgressing bool
}

// ControllerFinderFunc is a function type that maps a pod to a list of
// controllers and their scale.
type ControllerFinderFunc func(namespace string, ref *appsv1alpha1.WorkloadRef) (*Workload, error)

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

func (r *ControllerFinder) GetWorkloadForRef(namespace string, ref *appsv1alpha1.WorkloadRef) (*Workload, error) {
	for _, finder := range r.finders() {
		scale, err := finder(namespace, ref)
		if scale != nil || err != nil {
			return scale, err
		}
	}
	return nil, nil
}

func (r *ControllerFinder) finders() []ControllerFinderFunc {
	return []ControllerFinderFunc{r.getKruiseCloneSet, r.getDeployment}
}

var (
	ControllerKindDep      = apps.SchemeGroupVersion.WithKind("Deployment")
	ControllerKruiseKindCS = kruiseappsv1alpha1.SchemeGroupVersion.WithKind("CloneSet")
)

// getKruiseCloneSet returns the kruise cloneSet referenced by the provided controllerRef.
func (r *ControllerFinder) getKruiseCloneSet(namespace string, ref *appsv1alpha1.WorkloadRef) (*Workload, error) {
	// This error is irreversible, so there is no need to return error
	ok, err := verifyGroupKind(ref, ControllerKruiseKindCS.Kind, []string{ControllerKruiseKindCS.Group})
	if !ok {
		return nil, nil
	}
	cloneSet := &kruiseappsv1alpha1.CloneSet{}
	err = r.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: ref.Name}, cloneSet)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	workload := &Workload{
		// echoserver-68966d4654 -> 68966d4654
		StableRevision:      cloneSet.Status.CurrentRevision[strings.LastIndex(cloneSet.Status.CurrentRevision, "-")+1:],
		CanaryRevision:      cloneSet.Status.UpdateRevision[strings.LastIndex(cloneSet.Status.UpdateRevision, "-")+1:],
		CanaryReplicas:      cloneSet.Status.UpdatedReplicas,
		CanaryReadyReplicas: cloneSet.Status.UpdatedReadyReplicas,
		ObjectMeta:          cloneSet.ObjectMeta,
		TypeMeta:            cloneSet.TypeMeta,
	}
	// not in rollout progressing
	if _, ok = workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}
	// in rollout progressing
	workload.InRolloutProgressing = true
	return workload, nil
}

// getKruiseCloneSet returns the kruise cloneSet referenced by the provided controllerRef.
func (r *ControllerFinder) getDeployment(namespace string, ref *appsv1alpha1.WorkloadRef) (*Workload, error) {
	// This error is irreversible, so there is no need to return error
	ok, err := verifyGroupKind(ref, ControllerKindDep.Kind, []string{ControllerKindDep.Group})
	if !ok {
		return nil, nil
	}
	stable := &apps.Deployment{}
	err = r.Get(context.TODO(), client.ObjectKey{Namespace: namespace, Name: ref.Name}, stable)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	workload := &Workload{
		ObjectMeta: stable.ObjectMeta,
		TypeMeta:   stable.TypeMeta,
	}
	stableRs, err := r.GetDeploymentStableRs(stable)
	if err != nil || stableRs == nil {
		return workload, err
	}
	// stable revision
	workload.StableRevision = stableRs.Labels[RsPodRevisionLabelKey]
	// not in rollout progressing
	if _, ok = workload.Annotations[InRolloutProgressingAnnotation]; !ok {
		return workload, nil
	}

	// in rollout progressing
	workload.InRolloutProgressing = true
	// in rollback
	// delete auto-generated labels
	delete(stableRs.Spec.Template.Labels, RsPodRevisionLabelKey)
	if EqualIgnoreHash(&stableRs.Spec.Template, &stable.Spec.Template) {
		workload.CanaryRevision = workload.StableRevision
		return workload, nil
	}

	// canary revision
	workload.CanaryRevision = ComputeHash(&stable.Spec.Template, nil)
	// canary workload status
	canary, err := r.getLatestCanaryDeployment(stable)
	if err != nil {
		return nil, err
	} else if canary != nil {
		workload.CanaryReplicas = canary.Status.Replicas
		workload.CanaryReadyReplicas = canary.Status.ReadyReplicas
		canaryRs, err := r.GetDeploymentStableRs(canary)
		if err != nil || canaryRs == nil {
			return workload, err
		}
		workload.CanaryRevision = canaryRs.Labels[RsPodRevisionLabelKey]
	}
	return workload, err
}

func (r *ControllerFinder) getLatestCanaryDeployment(stable *apps.Deployment) (*apps.Deployment, error) {
	canaryList := &apps.DeploymentList{}
	selector, _ := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{MatchLabels: map[string]string{CanaryDeploymentLabel: string(stable.UID)}})
	err := r.List(context.TODO(), canaryList, &client.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	} else if len(canaryList.Items) == 0 {
		return nil, nil
	}
	sort.Slice(canaryList.Items, func(i, j int) bool {
		return canaryList.Items[j].CreationTimestamp.Before(&canaryList.Items[i].CreationTimestamp)
	})
	return &canaryList.Items[0], nil
}

func (r *ControllerFinder) getReplicaSetsForDeployment(obj *apps.Deployment) ([]apps.ReplicaSet, error) {
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

func (r *ControllerFinder) GetDeploymentStableRs(obj *apps.Deployment) (*apps.ReplicaSet, error) {
	rss, err := r.getReplicaSetsForDeployment(obj)
	if err != nil {
		return nil, err
	}
	if len(rss) != 1 {
		return nil, nil
	}
	return &rss[0], nil
}

func verifyGroupKind(ref *appsv1alpha1.WorkloadRef, expectedKind string, expectedGroups []string) (bool, error) {
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