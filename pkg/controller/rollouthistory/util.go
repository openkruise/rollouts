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

package rollouthistory

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"math/big"
	"strings"

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// For RolloutHistory
const (
	// rolloutIDLabel is designed to distinguish each rollout revision publications.
	// The value of RolloutIDLabel corresponds Rollout.Spec.RolloutID.
	rolloutIDLabel = "rollouts.kruise.io/rollout-id"
	// rolloutName is a label key that will be patched to rolloutHistory.
	// Only when rolloutIDLabel is set, rolloutNameLabel will be patched to rolloutHistory.
	rolloutNameLabel = "rollouts.kruise.io/rollout-name"
)

// controllerFinderFunc2 is a function type that maps <namespace, workloadRef> to a rolloutHistory's WorkloadInfo
type controllerFinderFunc2 func(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*rolloutv1alpha1.WorkloadInfo, error)

type controllerFinderFunc2LabelSelector func(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*v1.LabelSelector, error)
type controllerFinder2 struct {
	client.Client
}

func newControllerFinder2(c client.Client) *controllerFinder2 {
	return &controllerFinder2{
		Client: c,
	}
}

func (r *controllerFinder2) getWorkloadInfoForRef(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*rolloutv1alpha1.WorkloadInfo, error) {
	for _, finder := range r.finders2() {
		workloadInfo, err := finder(namespace, ref)
		if workloadInfo != nil || err != nil {
			return workloadInfo, err
		}
	}
	return nil, nil
}

func (r *controllerFinder2) getLabelSelectorForRef(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*v1.LabelSelector, error) {
	for _, finder := range r.finders2LabelSelector() {
		labelSelector, err := finder(namespace, ref)
		if labelSelector != nil || err != nil {
			return labelSelector, err
		}
	}
	return nil, nil
}

func (r *controllerFinder2) finders2LabelSelector() []controllerFinderFunc2LabelSelector {
	return []controllerFinderFunc2LabelSelector{r.getLabelSelectorWithAdvancedStatefulSet, r.getLabelSelectorWithCloneSet,
		r.getLabelSelectorWithDeployment, r.getLabelSelectorWithNativeStatefulSet}
}

func (r *controllerFinder2) finders2() []controllerFinderFunc2 {
	return []controllerFinderFunc2{r.getWorkloadInfoWithAdvancedStatefulSet, r.getWorkloadInfoWithCloneSet,
		r.getWorkloadInfoWithDeployment, r.getWorkloadInfoWithNativeStatefulSet}
}

// getWorkloadInfoWithAdvancedStatefulSet returns WorkloadInfo with kruise statefulset referenced by the provided controllerRef
func (r *controllerFinder2) getWorkloadInfoWithAdvancedStatefulSet(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*rolloutv1alpha1.WorkloadInfo, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, util.ControllerKruiseKindSts.Kind, []string{util.ControllerKruiseKindSts.Group})
	if !ok {
		return nil, nil
	}
	// get workload
	workload := &appsv1beta1.StatefulSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: ref.Name}, workload)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// assign value
	workloadInfo := &rolloutv1alpha1.WorkloadInfo{}
	workloadInfo.APIVersion = workload.APIVersion
	workloadInfo.Kind = workload.Kind
	workloadInfo.Name = workload.Name
	if workloadInfo.Data.Raw, err = json.Marshal(workload.Spec); err != nil {
		return nil, err
	}

	return workloadInfo, nil
}

// getLabelSelectorWithAdvancedStatefulSet returns selector with kruise statefulset referenced by the provided controllerRef
func (r *controllerFinder2) getLabelSelectorWithAdvancedStatefulSet(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*v1.LabelSelector, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, util.ControllerKruiseKindSts.Kind, []string{util.ControllerKruiseKindSts.Group})
	if !ok {
		return nil, nil
	}
	// get workload
	workload := &appsv1beta1.StatefulSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: ref.Name}, workload)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// assign value
	labelSelector := workload.Spec.Selector

	return labelSelector, nil
}

// getWorkloadInfoWithCloneSet returns WorkloadInfo with kruise cloneset referenced by the provided controllerRef
func (r *controllerFinder2) getWorkloadInfoWithCloneSet(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*rolloutv1alpha1.WorkloadInfo, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, util.ControllerKruiseKindCS.Kind, []string{util.ControllerKruiseKindCS.Group})
	if !ok {
		return nil, nil
	}
	// get workload
	workload := &appsv1alpha1.CloneSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: ref.Name}, workload)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// assign value
	workloadInfo := &rolloutv1alpha1.WorkloadInfo{}
	workloadInfo.APIVersion = workload.APIVersion
	workloadInfo.Kind = workload.Kind
	workloadInfo.Name = workload.Name
	if workloadInfo.Data.Raw, err = json.Marshal(workload.Spec); err != nil {
		return nil, err
	}

	return workloadInfo, nil
}

// getLabelSelectorWithCloneSet returns selector with kruise cloneset referenced by the provided controllerRef
func (r *controllerFinder2) getLabelSelectorWithCloneSet(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*v1.LabelSelector, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, util.ControllerKruiseKindCS.Kind, []string{util.ControllerKruiseKindCS.Group})
	if !ok {
		return nil, nil
	}
	// get workload
	workload := &appsv1alpha1.CloneSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: ref.Name}, workload)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// assign value
	labelSelector := workload.Spec.Selector

	return labelSelector, nil
}

// getWorkloadInfoWithDeployment returns WorkloadInfo with k8s native deployment referenced by the provided controllerRef
func (r *controllerFinder2) getWorkloadInfoWithDeployment(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*rolloutv1alpha1.WorkloadInfo, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, util.ControllerKindDep.Kind, []string{util.ControllerKindDep.Group})
	if !ok {
		return nil, nil
	}
	// get deployment
	workload := &apps.Deployment{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: ref.Name}, workload)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// assign value
	workloadInfo := &rolloutv1alpha1.WorkloadInfo{}
	workloadInfo.APIVersion = workload.APIVersion
	workloadInfo.Kind = workload.Kind
	workloadInfo.Name = workload.Name
	if workloadInfo.Data.Raw, err = json.Marshal(workload.Spec); err != nil {
		return nil, err
	}

	return workloadInfo, nil
}

// getLabelSelectorWithDeployment returns selector with deployment referenced by the provided controllerRef
func (r *controllerFinder2) getLabelSelectorWithDeployment(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*v1.LabelSelector, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, util.ControllerKindDep.Kind, []string{util.ControllerKindDep.Group})
	if !ok {
		return nil, nil
	}
	// get workload
	workload := &apps.Deployment{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: ref.Name}, workload)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// assign value
	labelSelector := workload.Spec.Selector

	return labelSelector, nil
}

// getWorkloadInfoWithNativeStatefulSet returns WorkloadInfo with k8s native statefulset referenced by the provided controllerRef
func (r *controllerFinder2) getWorkloadInfoWithNativeStatefulSet(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*rolloutv1alpha1.WorkloadInfo, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, util.ControllerKindSts.Kind, []string{util.ControllerKindSts.Group})
	if !ok {
		return nil, nil
	}
	// get workload
	workload := &apps.StatefulSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: ref.Name}, workload)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// assign value
	workloadInfo := &rolloutv1alpha1.WorkloadInfo{}
	workloadInfo.APIVersion = workload.APIVersion
	workloadInfo.Kind = workload.Kind
	workloadInfo.Name = workload.Name
	if workloadInfo.Data.Raw, err = json.Marshal(workload.Spec); err != nil {
		return nil, err
	}

	return workloadInfo, nil
}

// getLabelSelectorWithNativeStatefulSet returns selector with deployment referenced by the provided controllerRef
func (r *controllerFinder2) getLabelSelectorWithNativeStatefulSet(namespace string, ref *rolloutv1alpha1.WorkloadRef) (*v1.LabelSelector, error) {
	// This error is irreversible, so there is no need to return error
	ok, _ := verifyGroupKind(ref, util.ControllerKindSts.Kind, []string{util.ControllerKindSts.Group})
	if !ok {
		return nil, nil
	}
	// get workload
	workload := &apps.StatefulSet{}
	err := r.Get(context.TODO(), types.NamespacedName{Namespace: namespace, Name: ref.Name}, workload)
	if err != nil {
		// when error is NotFound, it is ok here.
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	// assign value
	labelSelector := workload.Spec.Selector

	return labelSelector, nil
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

var chars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z",
	"1", "2", "3", "4", "5", "6", "7", "8", "9", "0"}

// randAllString get random string
func randAllString(lenNum int) string {
	str := strings.Builder{}
	for i := 0; i < lenNum; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(36))
		if err != nil {
			return ""
		}
		l := chars[n.Int64()]
		str.WriteString(l)
	}
	return str.String()
}
