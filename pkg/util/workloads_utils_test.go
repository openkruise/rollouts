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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	rolloutapi "github.com/openkruise/rollouts/api"
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
func TestGetEmptyWorkloadObject(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		name        string
		gvk         schema.GroupVersionKind
		expectedObj client.Object
		shouldBeNil bool
	}{
		{
			name:        "native daemonset",
			gvk:         ControllerKindDS,
			expectedObj: &appsv1.DaemonSet{},
			shouldBeNil: false,
		},
		{
			name:        "kruise daemonset",
			gvk:         ControllerKruiseKindDS,
			expectedObj: &kruiseappsv1alpha1.DaemonSet{},
			shouldBeNil: false,
		},
		{
			name:        "native deployment",
			gvk:         ControllerKindDep,
			expectedObj: &appsv1.Deployment{},
			shouldBeNil: false,
		},
		{
			name:        "native statefulset",
			gvk:         ControllerKindSts,
			expectedObj: &appsv1.StatefulSet{},
			shouldBeNil: false,
		},
		{
			name:        "kruise cloneset",
			gvk:         ControllerKruiseKindCS,
			expectedObj: &kruiseappsv1alpha1.CloneSet{},
			shouldBeNil: false,
		},
		{
			name:        "kruise statefulset",
			gvk:         ControllerKruiseKindSts,
			expectedObj: &appsv1beta1.StatefulSet{},
			shouldBeNil: false,
		},
		{
			name:        "replicaset",
			gvk:         ControllerKindRS,
			expectedObj: &appsv1.ReplicaSet{},
			shouldBeNil: false,
		},
		{
			name: "unsupported workload",
			gvk: schema.GroupVersionKind{
				Group:   "custom.io",
				Version: "v1",
				Kind:    "CustomWorkload",
			},
			expectedObj: nil,
			shouldBeNil: true,
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			obj := GetEmptyWorkloadObject(cs.gvk)
			if cs.shouldBeNil {
				Expect(obj).Should(BeNil())
			} else {
				Expect(obj).NotTo(BeNil())
				Expect(reflect.TypeOf(obj)).Should(Equal(reflect.TypeOf(cs.expectedObj)))
			}
		})
	}
}

func TestGetEmptyObjectWithKey(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		name        string
		inputObj    client.Object
		expectedObj client.Object
	}{
		{
			name: "native daemonset",
			inputObj: &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-daemonset",
					Namespace: "test-namespace",
				},
			},
			expectedObj: &appsv1.DaemonSet{},
		},
		{
			name: "kruise daemonset",
			inputObj: &kruiseappsv1alpha1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-kruise-daemonset",
					Namespace: "test-namespace",
				},
			},
			expectedObj: &kruiseappsv1alpha1.DaemonSet{},
		},
		{
			name: "native deployment",
			inputObj: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deployment",
					Namespace: "test-namespace",
				},
			},
			expectedObj: &appsv1.Deployment{},
		},
		{
			name: "native statefulset",
			inputObj: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-statefulset",
					Namespace: "test-namespace",
				},
			},
			expectedObj: &appsv1.StatefulSet{},
		},
		{
			name: "kruise cloneset",
			inputObj: &kruiseappsv1alpha1.CloneSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cloneset",
					Namespace: "test-namespace",
				},
			},
			expectedObj: &kruiseappsv1alpha1.CloneSet{},
		},
		{
			name: "pod",
			inputObj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
				},
			},
			expectedObj: &corev1.Pod{},
		},
		{
			name: "service",
			inputObj: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service",
					Namespace: "test-namespace",
				},
			},
			expectedObj: &corev1.Service{},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			emptyObj := GetEmptyObjectWithKey(cs.inputObj)

			// Check that the returned object is of the correct type
			Expect(reflect.TypeOf(emptyObj)).Should(Equal(reflect.TypeOf(cs.expectedObj)))

			// Check that name and namespace are preserved
			Expect(emptyObj.GetName()).Should(Equal(cs.inputObj.GetName()))
			Expect(emptyObj.GetNamespace()).Should(Equal(cs.inputObj.GetNamespace()))
		})
	}
}

func TestIsSupportedWorkload(t *testing.T) {
	RegisterFailHandler(Fail)

	cases := []struct {
		name        string
		gvk         schema.GroupVersionKind
		isSupported bool
	}{
		{
			name:        "native daemonset",
			gvk:         ControllerKindDS,
			isSupported: true,
		},
		{
			name:        "kruise daemonset",
			gvk:         ControllerKruiseKindDS,
			isSupported: true,
		},
		{
			name:        "native deployment",
			gvk:         ControllerKindDep,
			isSupported: true,
		},
		{
			name:        "native statefulset",
			gvk:         ControllerKindSts,
			isSupported: true,
		},
		{
			name:        "kruise cloneset",
			gvk:         ControllerKruiseKindCS,
			isSupported: true,
		},
		{
			name:        "kruise statefulset",
			gvk:         ControllerKruiseKindSts,
			isSupported: true,
		},
		{
			name:        "kruise old statefulset",
			gvk:         ControllerKruiseOldKindSts,
			isSupported: true,
		},
		{
			name:        "replicaset",
			gvk:         ControllerKindRS,
			isSupported: true,
		},
		{
			name: "unsupported workload",
			gvk: schema.GroupVersionKind{
				Group:   "custom.io",
				Version: "v1",
				Kind:    "CustomWorkload",
			},
			isSupported: false,
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			supported := IsSupportedWorkload(cs.gvk)
			Expect(supported).Should(Equal(cs.isSupported))
		})
	}
}

func TestNativeDaemonSetIntegration(t *testing.T) {
	RegisterFailHandler(Fail)

	t.Run("native daemonset in ownership chain", func(t *testing.T) {
		// Create a test scenario where a native DaemonSet is part of an ownership chain
		parentWorkload := &kruiseappsv1alpha1.CloneSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "parent-cloneset",
				Namespace: "test-namespace",
				UID:       "parent-uid",
			},
		}
		parentWorkload.SetGroupVersionKind(ControllerKruiseKindCS)

		nativeDaemonSet := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "child-daemonset",
				Namespace: "test-namespace",
				UID:       "child-uid",
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(parentWorkload, parentWorkload.GetObjectKind().GroupVersionKind()),
				},
			},
		}
		nativeDaemonSet.SetGroupVersionKind(ControllerKindDS)

		objects := []client.Object{parentWorkload, nativeDaemonSet}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

		// Test IsOwnedBy
		owned, err := IsOwnedBy(cli, nativeDaemonSet, parentWorkload)
		Expect(err).NotTo(HaveOccurred())
		Expect(owned).Should(BeTrue())

		// Test GetOwnerWorkload
		owner, err := GetOwnerWorkload(cli, nativeDaemonSet)
		Expect(err).NotTo(HaveOccurred())
		Expect(owner).NotTo(BeNil())
		Expect(owner.GetName()).Should(Equal(parentWorkload.GetName()))
		Expect(owner.GetUID()).Should(Equal(parentWorkload.GetUID()))
	})

	t.Run("native daemonset as top-level workload", func(t *testing.T) {
		// Create a test scenario where a native DaemonSet is the top-level workload
		nativeDaemonSet := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "top-level-daemonset",
				Namespace: "test-namespace",
				UID:       "daemonset-uid",
				Annotations: map[string]string{
					InRolloutProgressingAnnotation: "true",
				},
			},
		}
		nativeDaemonSet.SetGroupVersionKind(ControllerKindDS)

		objects := []client.Object{nativeDaemonSet}
		cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

		// Test GetOwnerWorkload - should return the DaemonSet itself
		owner, err := GetOwnerWorkload(cli, nativeDaemonSet)
		Expect(err).NotTo(HaveOccurred())
		Expect(owner).NotTo(BeNil())
		Expect(owner.GetName()).Should(Equal(nativeDaemonSet.GetName()))
		Expect(owner.GetUID()).Should(Equal(nativeDaemonSet.GetUID()))
	})
}
