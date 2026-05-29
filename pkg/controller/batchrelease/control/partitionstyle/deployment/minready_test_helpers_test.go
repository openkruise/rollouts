/*
Copyright 2026 The Kruise Authors.

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

package deployment

import (
	"context"
	"fmt"
	"testing"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openkruise/rollouts/api/v1beta1"
)

func newMinReadyDeployment() *apps.Deployment {
	progressDeadline := int32(60)
	maxUnavailable := intstr.FromString("25%")
	maxSurge := intstr.FromInt(1)
	deployment := deploymentDemo.DeepCopy()
	deployment.ResourceVersion = "1"
	deployment.UID = types.UID("minready-deployment-uid")
	deployment.Spec.MinReadySeconds = 7
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadline
	deployment.Spec.Strategy.Type = apps.RollingUpdateDeploymentStrategyType
	deployment.Spec.Strategy.RollingUpdate = &apps.RollingUpdateDeployment{
		MaxUnavailable: &maxUnavailable,
		MaxSurge:       &maxSurge,
	}
	return deployment
}

func newInflatedMinReadyDeployment() *apps.Deployment {
	deployment := newMinReadyDeployment()
	inflateDeploymentStrategy(deployment)
	return deployment
}

func newBuiltMinReadyControl(t *testing.T, deployment *apps.Deployment, objs ...interface{}) *MinReadyControl {
	t.Helper()
	objects := []interface{}{deployment}
	objects = append(objects, objs...)
	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(toClientObjects(t, objects)...)
	rc := NewController(builder.Build(), types.NamespacedName{
		Namespace: deployment.Namespace,
		Name:      deployment.Name,
	}, deployment.GroupVersionKind())
	built, err := (&MinReadyControl{realController: rc.(*realController)}).BuildController()
	if err != nil {
		t.Fatalf("BuildController failed: %v", err)
	}
	return built.(*MinReadyControl)
}

func toClientObjects(t *testing.T, objects []interface{}) []client.Object {
	t.Helper()
	result := make([]client.Object, 0, len(objects))
	for _, object := range objects {
		typed, ok := object.(client.Object)
		if !ok {
			t.Fatalf("object %T does not implement client.Object", object)
		}
		result = append(result, typed)
	}
	return result
}

func fetchMinReadyDeployment(t *testing.T, control *MinReadyControl) *apps.Deployment {
	t.Helper()
	got := &apps.Deployment{}
	key := types.NamespacedName{Namespace: control.object.Namespace, Name: control.object.Name}
	if err := control.client.Get(context.TODO(), key, got); err != nil {
		t.Fatalf("Get deployment failed: %v", err)
	}
	return got
}

func assertMinReadyInflated(t *testing.T, deployment *apps.Deployment) {
	t.Helper()
	if deployment.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
		t.Fatalf("strategy.type = %q, want RollingUpdate", deployment.Spec.Strategy.Type)
	}
	if deployment.Spec.MinReadySeconds != InflatedMinReadySeconds {
		t.Fatalf("minReadySeconds = %d, want %d", deployment.Spec.MinReadySeconds, InflatedMinReadySeconds)
	}
	if deployment.Spec.ProgressDeadlineSeconds == nil || *deployment.Spec.ProgressDeadlineSeconds != InflatedProgressDeadlineSeconds {
		t.Fatalf("progressDeadlineSeconds = %v, want %d", deployment.Spec.ProgressDeadlineSeconds, InflatedProgressDeadlineSeconds)
	}
	if got := deployment.Spec.Strategy.RollingUpdate.MaxUnavailable; got == nil || got.IntVal != 0 {
		t.Fatalf("maxUnavailable = %v, want 0", got)
	}
	if got := deployment.Spec.Strategy.RollingUpdate.MaxSurge; got == nil || got.IntVal != InflatedMaxSurgeInt {
		t.Fatalf("maxSurge = %v, want %d", got, InflatedMaxSurgeInt)
	}
}

func assertAnnotation(t *testing.T, annotations map[string]string, key, want string) {
	t.Helper()
	if got := annotations[key]; got != want {
		t.Fatalf("annotation %s = %q, want %q", key, got, want)
	}
}

func newMinReadyReplicaSet(deployment *apps.Deployment, updateRevision string, replicas, readyReplicas int32) *apps.ReplicaSet {
	return &apps.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", deployment.Name, updateRevision),
			Namespace: deployment.Namespace,
			UID:       types.UID(fmt.Sprintf("rs-%s-%s", deployment.Name, updateRevision)),
			Labels: map[string]string{
				apps.DefaultDeploymentUniqueLabelKey: updateRevision,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: apps.SchemeGroupVersion.String(),
					Kind:       "Deployment",
					Name:       deployment.Name,
					UID:        deployment.UID,
					Controller: pointerBool(true),
				},
			},
		},
		Spec: apps.ReplicaSetSpec{
			Replicas: pointerInt32(replicas),
			Selector: deployment.Spec.Selector.DeepCopy(),
			Template: deployment.Spec.Template,
		},
		Status: apps.ReplicaSetStatus{
			Replicas:      replicas,
			ReadyReplicas: readyReplicas,
		},
	}
}

func newMinReadyUpdatedPods(deployment *apps.Deployment, rs *apps.ReplicaSet, updateRevision, rolloutID string, total, ready int) []*corev1.Pod {
	pods := make([]*corev1.Pod, 0, total)
	for i := 0; i < total; i++ {
		readyCondition := corev1.ConditionFalse
		if i < ready {
			readyCondition = corev1.ConditionTrue
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-pod-%d", deployment.Name, i),
				Namespace: deployment.Namespace,
				Labels: map[string]string{
					"app":                                "busybox",
					apps.DefaultDeploymentUniqueLabelKey: updateRevision,
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: apps.SchemeGroupVersion.String(),
						Kind:       "ReplicaSet",
						Name:       rs.Name,
						UID:        rs.UID,
						Controller: pointerBool(true),
					},
				},
			},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{
					Type:   corev1.PodReady,
					Status: readyCondition,
				}},
			},
		}
		if rolloutID != "" {
			pod.Labels[v1beta1.RolloutIDLabel] = rolloutID
		}
		pods = append(pods, pod)
	}
	return pods
}

func pointerInt32(v int32) *int32 {
	return &v
}

func pointerBool(v bool) *bool {
	return &v
}
