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

package mutating

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	kruiseappsv1beta1 "github.com/openkruise/kruise-api/apps/v1beta1"
	appsv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/util"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestHandleStatefulSet(t *testing.T) {
	cases := []struct {
		name       string
		getObjs    func() (*kruiseappsv1beta1.StatefulSet, *kruiseappsv1beta1.StatefulSet)
		expectObj  func() *kruiseappsv1beta1.StatefulSet
		getRollout func() *appsv1beta1.Rollout
		isError    bool
	}{
		{
			name: "cloneSet image v1->v2, matched rollout",
			getObjs: func() (*kruiseappsv1beta1.StatefulSet, *kruiseappsv1beta1.StatefulSet) {
				oldObj := statefulset.DeepCopy()
				newObj := statefulset.DeepCopy()
				newObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return oldObj, newObj
			},
			expectObj: func() *kruiseappsv1beta1.StatefulSet {
				obj := statefulset.DeepCopy()
				obj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				obj.Annotations[util.InRolloutProgressingAnnotation] = `{"rolloutName":"rollout-demo"}`
				obj.Spec.UpdateStrategy.RollingUpdate.Partition = pointer.Int32(math.MaxInt16)
				return obj
			},
			getRollout: func() *appsv1beta1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.WorkloadRef = appsv1beta1.ObjectRef{
					APIVersion: "apps.kruise.io/v1beta1",
					Kind:       "StatefulSet",
					Name:       "echoserver",
				}
				return obj
			},
		},
	}

	decoder, _ := admission.NewDecoder(scheme)
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			h := UnifiedWorkloadHandler{
				Client:  client,
				Decoder: decoder,
				Finder:  util.NewControllerFinder(client),
			}
			rollout := cs.getRollout()
			if err := client.Create(context.TODO(), rollout); err != nil {
				t.Errorf(err.Error())
			}

			oldObj, newObj := cs.getObjs()
			oldO, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(oldObj)
			newO, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(newObj)
			oldUnstructured := &unstructured.Unstructured{Object: oldO}
			newUnstructured := &unstructured.Unstructured{Object: newO}
			oldUnstructured.SetGroupVersionKind(newObj.GroupVersionKind())
			newUnstructured.SetGroupVersionKind(newObj.GroupVersionKind())
			_, err := h.handleStatefulSetLikeWorkload(newUnstructured, oldUnstructured)
			if cs.isError && err == nil {
				t.Fatal("handleStatefulSetLikeWorkload failed")
			} else if !cs.isError && err != nil {
				t.Fatalf(err.Error())
			}
			newStructured := &kruiseappsv1beta1.StatefulSet{}
			err = runtime.DefaultUnstructuredConverter.FromUnstructured(newUnstructured.Object, newStructured)
			if err != nil {
				t.Fatal("DefaultUnstructuredConvert failed")
			}
			expect := cs.expectObj()
			if !reflect.DeepEqual(newStructured, expect) {
				by, _ := json.Marshal(newStructured)
				t.Fatalf("handlerCloneSet failed, and new(%s)", string(by))
			}
		})
	}
}
