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
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	rolloutapi "github.com/openkruise/rollouts/api"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var scheme *runtime.Scheme

func init() {
	scheme = runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = appsv1beta1.AddToScheme(scheme)
	_ = rolloutapi.AddToScheme(scheme)
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

func TestFilterCanaryAndStableReplicaSet(t *testing.T) {
	RegisterFailHandler(Fail)

	const notExists = "not-exists"
	createTimestamps := []time.Time{
		time.Now().Add(0 * time.Second),
		time.Now().Add(1 * time.Second),
		time.Now().Add(2 * time.Second),
		time.Now().Add(3 * time.Second),
		time.Now().Add(4 * time.Second),
		time.Now().Add(5 * time.Second),
	}
	templateFactory := func(order int64) corev1.PodTemplateSpec {
		return corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Generation: order},
		}
	}
	makeRS := func(name, revision string, createTime time.Time, templateOrder int64, replicas int32) *appsv1.ReplicaSet {
		return &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				CreationTimestamp: metav1.Time{Time: createTime},
				Annotations:       map[string]string{DeploymentRevisionAnnotation: revision},
			},
			Spec: appsv1.ReplicaSetSpec{
				Replicas: pointer.Int32(replicas),
				Template: templateFactory(templateOrder),
			},
		}
	}
	makeD := func(name, revision string, templateOrder int64) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Annotations: map[string]string{DeploymentRevisionAnnotation: revision},
			},
			Spec: appsv1.DeploymentSpec{Template: templateFactory(templateOrder)},
		}
	}

	cases := map[string]struct {
		parameters func() ([]*appsv1.ReplicaSet, *appsv1.Deployment)
		stableName string
		canaryName string
	}{
		"no canary": {
			parameters: func() ([]*appsv1.ReplicaSet, *appsv1.Deployment) {
				rss := []*appsv1.ReplicaSet{
					makeRS("r0", "1", createTimestamps[1], 1, 1),
					makeRS("r1", "0", createTimestamps[0], 0, 0),
				}
				return rss, makeD("d", "0", 2)
			},
			stableName: "r0",
			canaryName: notExists,
		},
		"no stable": {
			parameters: func() ([]*appsv1.ReplicaSet, *appsv1.Deployment) {
				rss := []*appsv1.ReplicaSet{
					makeRS("r0", "0", createTimestamps[0], 0, 1),
				}
				return rss, makeD("d", "0", 0)
			},
			stableName: notExists,
			canaryName: "r0",
		},
		"1 active oldRS": {
			parameters: func() ([]*appsv1.ReplicaSet, *appsv1.Deployment) {
				rss := []*appsv1.ReplicaSet{
					makeRS("r0", "2", createTimestamps[0], 0, 1),
					makeRS("r1", "3", createTimestamps[1], 1, 1),
					makeRS("r1", "1", createTimestamps[3], 3, 0),
					makeRS("r1", "0", createTimestamps[4], 4, 0),
				}
				return rss, makeD("d", "0", 1)
			},
			stableName: "r0",
			canaryName: "r1",
		},
		"many active oldRS": {
			parameters: func() ([]*appsv1.ReplicaSet, *appsv1.Deployment) {
				rss := []*appsv1.ReplicaSet{
					makeRS("r0", "0", createTimestamps[3], 0, 1),
					makeRS("r3", "2", createTimestamps[1], 3, 1),
					makeRS("r2", "3", createTimestamps[0], 2, 1),
					makeRS("r1", "1", createTimestamps[2], 1, 1),
				}
				return rss, makeD("d", "4", 3)
			},
			stableName: "r0",
			canaryName: "r3",
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			rss, d := cs.parameters()
			canary, stable := FindCanaryAndStableReplicaSet(rss, d)
			if canary != nil {
				Expect(canary.Name).Should(Equal(cs.canaryName))
			} else {
				Expect(cs.canaryName).Should(Equal(notExists))
			}
			if stable != nil {
				Expect(stable.Name).Should(Equal(cs.stableName))
			} else {
				Expect(cs.stableName).Should(Equal(notExists))
			}
		})
	}
}
