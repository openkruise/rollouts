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
	"reflect"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var scheme *runtime.Scheme

func init() {
	scheme = runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = appsv1beta1.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
	_ = kruiseappsv1alpha1.AddToScheme(scheme)
}

func TestIsOwnedBy(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		name          string
		TopologyBuild func() []client.Object
		Expect        bool
	}{
		{
			name: "direct",
			TopologyBuild: func() []client.Object {
				father := cloneset.DeepCopy()
				son := deployment.DeepCopy()
				son.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(father, father.GetObjectKind().GroupVersionKind()),
				})
				return []client.Object{father, son}
			},
			Expect: true,
		},
		{
			name: "indirect-2",
			TopologyBuild: func() []client.Object {
				father := cloneset.DeepCopy()
				son1 := deployment.DeepCopy()
				son1.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(father, father.GetObjectKind().GroupVersionKind()),
				})
				son2 := nativeStatefulSet.DeepCopy()
				son2.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son1, son1.GetObjectKind().GroupVersionKind()),
				})
				return []client.Object{father, son1, son2}
			},
			Expect: true,
		},
		{
			name: "indirect-3",
			TopologyBuild: func() []client.Object {
				father := cloneset.DeepCopy()
				son1 := deployment.DeepCopy()
				son1.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(father, father.GetObjectKind().GroupVersionKind()),
				})
				son2 := nativeStatefulSet.DeepCopy()
				son2.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son1, son1.GetObjectKind().GroupVersionKind()),
				})
				son3 := advancedStatefulSet.DeepCopy()
				son3.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son2, son2.GetObjectKind().GroupVersionKind()),
				})
				return []client.Object{father, son1, son2, son3}
			},
			Expect: true,
		},
		{
			name: "indirect-3-false",
			TopologyBuild: func() []client.Object {
				father := cloneset.DeepCopy()
				son1 := deployment.DeepCopy()
				son2 := nativeStatefulSet.DeepCopy()
				son2.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son1, son1.GetObjectKind().GroupVersionKind()),
				})
				son3 := advancedStatefulSet.DeepCopy()
				son3.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son2, son2.GetObjectKind().GroupVersionKind()),
				})
				return []client.Object{father, son1, son2, son3}
			},
			Expect: false,
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			objects := cs.TopologyBuild()
			father := objects[0]
			son := objects[len(objects)-1]
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			owned, err := IsOwnedBy(cli, son, father)
			Expect(err).NotTo(HaveOccurred())
			Expect(owned == cs.Expect).Should(BeTrue())
		})
	}
}

func TestGetOwnerWorkload(t *testing.T) {
	RegisterFailHandler(Fail)

	topWorkload := cloneset.DeepCopy()
	topWorkload.Annotations[BatchReleaseControlAnnotation] = "something"
	cases := []struct {
		name          string
		TopologyBuild func() []client.Object
		Expect        bool
	}{
		{
			name: "direct",
			TopologyBuild: func() []client.Object {
				father := topWorkload.DeepCopy()
				son := deployment.DeepCopy()
				son.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(father, father.GetObjectKind().GroupVersionKind()),
				})
				return []client.Object{father, son}
			},
			Expect: true,
		},
		{
			name: "indirect-2",
			TopologyBuild: func() []client.Object {
				father := topWorkload.DeepCopy()
				son1 := deployment.DeepCopy()
				son1.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(father, father.GetObjectKind().GroupVersionKind()),
				})
				son2 := nativeStatefulSet.DeepCopy()
				son2.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son1, son1.GetObjectKind().GroupVersionKind()),
				})
				return []client.Object{father, son1, son2}
			},
			Expect: true,
		},
		{
			name: "indirect-3",
			TopologyBuild: func() []client.Object {
				father := topWorkload.DeepCopy()
				son1 := deployment.DeepCopy()
				son1.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(father, father.GetObjectKind().GroupVersionKind()),
				})
				son2 := nativeStatefulSet.DeepCopy()
				son2.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son1, son1.GetObjectKind().GroupVersionKind()),
				})
				son3 := advancedStatefulSet.DeepCopy()
				son3.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son2, son2.GetObjectKind().GroupVersionKind()),
				})
				return []client.Object{father, son1, son2, son3}
			},
			Expect: true,
		},
		{
			name: "indirect-3-false",
			TopologyBuild: func() []client.Object {
				father := topWorkload.DeepCopy()
				son1 := deployment.DeepCopy()
				son2 := nativeStatefulSet.DeepCopy()
				son2.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son1, son1.GetObjectKind().GroupVersionKind()),
				})
				son3 := advancedStatefulSet.DeepCopy()
				son3.SetOwnerReferences([]metav1.OwnerReference{
					*metav1.NewControllerRef(son2, son2.GetObjectKind().GroupVersionKind()),
				})
				return []client.Object{father, son1, son2, son3}
			},
			Expect: false,
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			objects := cs.TopologyBuild()
			father := objects[0]
			son := objects[len(objects)-1]
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			got, err := GetOwnerWorkload(cli, son)
			Expect(err).NotTo(HaveOccurred())
			if cs.Expect {
				Expect(reflect.DeepEqual(father, got)).Should(BeTrue())
			} else {
				Expect(reflect.DeepEqual(father, got)).Should(BeFalse())
			}
		})
	}
}
