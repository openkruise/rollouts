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
	kruisev1aplphal "github.com/openkruise/kruise-api/apps/v1alpha1"
	appsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"testing"
)

var (
	scheme *runtime.Scheme

	deploymentDemo = &apps.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "echoserver",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
			UID:         types.UID("281ba6f7-ff28-4779-940b-e966640c201f"),
		},
		Spec: apps.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "echoserver",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "echoserver",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "echoserver",
							Image: "echoserver:v1",
						},
					},
				},
			},
		},
	}

	rsDemo = &apps.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "ReplicaSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "echoserver-v1",
			Labels: map[string]string{
				"app":               "echoserver",
				"pod-template-hash": "5b494f7bf",
			},
			Annotations: map[string]string{},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(deploymentDemo, schema.GroupVersionKind{
					Group:   apps.SchemeGroupVersion.Group,
					Version: apps.SchemeGroupVersion.Version,
					Kind:    "Deployment",
				}),
			},
		},
		Spec: apps.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "echoserver",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "echoserver",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "echoserver",
							Image: "echoserver:v1",
						},
					},
				},
			},
		},
	}

	cloneSetDemo = &kruisev1aplphal.CloneSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps.kruise.io/v1alpha1",
			Kind:       "CloneSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "echoserver",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
		Spec: kruisev1aplphal.CloneSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "echoserver",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "echoserver",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "echoserver",
							Image: "echoserver:v1",
						},
					},
				},
			},
		},
	}

	rolloutDemo = &appsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "rollout-demo",
			Labels: map[string]string{},
		},
		Spec: appsv1alpha1.RolloutSpec{
			ObjectRef: appsv1alpha1.ObjectRef{
				Type: appsv1alpha1.WorkloadRefType,
				WorkloadRef: &appsv1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "echoserver",
				},
			},
		},
	}
)

func init() {
	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = kruisev1aplphal.AddToScheme(scheme)
	_ = appsv1alpha1.AddToScheme(scheme)
}

func TestHandlerDeployment(t *testing.T) {
	cases := []struct {
		name       string
		getObjs    func() (*apps.Deployment, *apps.Deployment)
		expectObj  func() *apps.Deployment
		getRollout func() *appsv1alpha1.Rollout
		getRs      func() []*apps.ReplicaSet
		isError    bool
	}{
		{
			name: "deployment image v1->v2, matched rollout",
			getObjs: func() (*apps.Deployment, *apps.Deployment) {
				oldObj := deploymentDemo.DeepCopy()
				newObj := deploymentDemo.DeepCopy()
				newObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return oldObj, newObj
			},
			expectObj: func() *apps.Deployment {
				obj := deploymentDemo.DeepCopy()
				obj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				obj.Annotations[util.InRolloutProgressingAnnotation] = `{"RolloutName":"rollout-demo"}`
				obj.Spec.Paused = true
				return obj
			},
			getRs: func() []*apps.ReplicaSet {
				rs := rsDemo.DeepCopy()
				return []*apps.ReplicaSet{rs}
			},
			getRollout: func() *appsv1alpha1.Rollout {
				return rolloutDemo.DeepCopy()
			},
		},
		{
			name: "deployment image v1->v2, no matched rollout",
			getObjs: func() (*apps.Deployment, *apps.Deployment) {
				oldObj := deploymentDemo.DeepCopy()
				newObj := deploymentDemo.DeepCopy()
				newObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return oldObj, newObj
			},
			expectObj: func() *apps.Deployment {
				obj := deploymentDemo.DeepCopy()
				obj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return obj
			},
			getRs: func() []*apps.ReplicaSet {
				rs := rsDemo.DeepCopy()
				return []*apps.ReplicaSet{rs}
			},
			getRollout: func() *appsv1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.ObjectRef.WorkloadRef = &appsv1alpha1.WorkloadRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "other",
				}
				return obj
			},
		},
		{
			name: "deployment image v2->v3, matched rollout, but multiple rss",
			getObjs: func() (*apps.Deployment, *apps.Deployment) {
				oldObj := deploymentDemo.DeepCopy()
				oldObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				newObj := deploymentDemo.DeepCopy()
				newObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v3"
				return oldObj, newObj
			},
			expectObj: func() *apps.Deployment {
				obj := deploymentDemo.DeepCopy()
				obj.Spec.Template.Spec.Containers[0].Image = "echoserver:v3"
				return obj
			},
			getRs: func() []*apps.ReplicaSet {
				rs1 := rsDemo.DeepCopy()
				rs2 := rsDemo.DeepCopy()
				rs2.Name = "echoserver-v2"
				rs2.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return []*apps.ReplicaSet{rs1, rs2}
			},
			getRollout: func() *appsv1alpha1.Rollout {
				return rolloutDemo.DeepCopy()
			},
		},
		{
			name: "set deployment paused = false, matched rollout, in progressing, reject",
			getObjs: func() (*apps.Deployment, *apps.Deployment) {
				oldObj := deploymentDemo.DeepCopy()
				oldObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				oldObj.Annotations[util.InRolloutProgressingAnnotation] = `{"RolloutName":"rollout-demo","RolloutDone":false}`
				oldObj.Spec.Paused = true
				newObj := deploymentDemo.DeepCopy()
				newObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				newObj.Annotations[util.InRolloutProgressingAnnotation] = `{"RolloutName":"rollout-demo","RolloutDone":false}`
				newObj.Spec.Paused = false
				return oldObj, newObj
			},
			expectObj: func() *apps.Deployment {
				obj := deploymentDemo.DeepCopy()
				obj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				obj.Annotations[util.InRolloutProgressingAnnotation] = `{"RolloutName":"rollout-demo","RolloutDone":false}`
				obj.Spec.Paused = true
				return obj
			},
			getRs: func() []*apps.ReplicaSet {
				rs := rsDemo.DeepCopy()
				return []*apps.ReplicaSet{rs}
			},
			getRollout: func() *appsv1alpha1.Rollout {
				return rolloutDemo.DeepCopy()
			},
		},
		{
			name: "set deployment paused = false, matched rollout, in finalising, allow",
			getObjs: func() (*apps.Deployment, *apps.Deployment) {
				oldObj := deploymentDemo.DeepCopy()
				oldObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				oldObj.Annotations[util.InRolloutProgressingAnnotation] = `{"RolloutName":"rollout-demo","RolloutDone":true}`
				oldObj.Spec.Paused = true
				newObj := deploymentDemo.DeepCopy()
				newObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				newObj.Annotations[util.InRolloutProgressingAnnotation] = `{"RolloutName":"rollout-demo","RolloutDone":true}`
				newObj.Spec.Paused = false
				return oldObj, newObj
			},
			expectObj: func() *apps.Deployment {
				obj := deploymentDemo.DeepCopy()
				obj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				obj.Annotations[util.InRolloutProgressingAnnotation] = `{"RolloutName":"rollout-demo","RolloutDone":true}`
				obj.Spec.Paused = true
				return obj
			},
			getRs: func() []*apps.ReplicaSet {
				rs := rsDemo.DeepCopy()
				return []*apps.ReplicaSet{rs}
			},
			getRollout: func() *appsv1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				return obj
			},
		},
	}

	decoder, _ := admission.NewDecoder(scheme)
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			h := WorkloadHandler{
				Client:  client,
				Decoder: decoder,
				Finder:  util.NewControllerFinder(client),
			}
			rollout := cs.getRollout()
			if err := client.Create(context.TODO(), rollout); err != nil {
				t.Errorf(err.Error())
			}
			for _, rs := range cs.getRs() {
				if err := client.Create(context.TODO(), rs); err != nil {
					t.Errorf(err.Error())
				}
			}

			oldObj, newObj := cs.getObjs()
			err := h.handlerDeployment(newObj, oldObj)
			if cs.isError && err == nil {
				t.Fatal("handlerDeployment failed")
			} else if !cs.isError && err != nil {
				t.Fatalf(err.Error())
			}
			if !reflect.DeepEqual(newObj, cs.expectObj()) {
				by, _ := json.Marshal(newObj)
				t.Fatalf("handlerDeployment failed, and new(%s)", string(by))
			}
		})
	}
}

func TestHandlerCloneSet(t *testing.T) {
	cases := []struct {
		name       string
		getObjs    func() (*kruisev1aplphal.CloneSet, *kruisev1aplphal.CloneSet)
		expectObj  func() *kruisev1aplphal.CloneSet
		getRollout func() *appsv1alpha1.Rollout
		isError    bool
	}{
		{
			name: "cloneSet image v1->v2, matched rollout",
			getObjs: func() (*kruisev1aplphal.CloneSet, *kruisev1aplphal.CloneSet) {
				oldObj := cloneSetDemo.DeepCopy()
				newObj := cloneSetDemo.DeepCopy()
				newObj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				return oldObj, newObj
			},
			expectObj: func() *kruisev1aplphal.CloneSet {
				obj := cloneSetDemo.DeepCopy()
				obj.Spec.Template.Spec.Containers[0].Image = "echoserver:v2"
				obj.Annotations[util.InRolloutProgressingAnnotation] = `{"RolloutName":"rollout-demo"}`
				obj.Spec.UpdateStrategy.Paused = true
				return obj
			},
			getRollout: func() *appsv1alpha1.Rollout {
				obj := rolloutDemo.DeepCopy()
				obj.Spec.ObjectRef.WorkloadRef = &appsv1alpha1.WorkloadRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
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
			h := WorkloadHandler{
				Client:  client,
				Decoder: decoder,
				Finder:  util.NewControllerFinder(client),
			}
			rollout := cs.getRollout()
			if err := client.Create(context.TODO(), rollout); err != nil {
				t.Errorf(err.Error())
			}

			oldObj, newObj := cs.getObjs()
			err := h.handlerCloneSet(newObj, oldObj)
			if cs.isError && err == nil {
				t.Fatal("handlerCloneSet failed")
			} else if !cs.isError && err != nil {
				t.Fatalf(err.Error())
			}
			if !reflect.DeepEqual(newObj, cs.expectObj()) {
				by, _ := json.Marshal(newObj)
				t.Fatalf("handlerCloneSet failed, and new(%s)", string(by))
			}
		})
	}
}
