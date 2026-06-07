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

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1beta1"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle"
	partitiondeployment "github.com/openkruise/rollouts/pkg/controller/batchrelease/control/partitionstyle/deployment"
)

var integrationScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(apps.AddToScheme(integrationScheme))
	utilruntime.Must(corev1.AddToScheme(integrationScheme))
	utilruntime.Must(policyv1.AddToScheme(integrationScheme))
	utilruntime.Must(rolloutapi.AddToScheme(integrationScheme))
}

func newIntegrationMinReadyRelease() *v1beta1.BatchRelease {
	return &v1beta1.BatchRelease{
		TypeMeta: metav1.TypeMeta{APIVersion: v1beta1.GroupVersion.String(), Kind: "BatchRelease"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo-release",
			Namespace: "default",
		},
		Spec: v1beta1.BatchReleaseSpec{
			WorkloadRef: v1beta1.ObjectRef{APIVersion: apps.SchemeGroupVersion.String(), Kind: "Deployment", Name: "demo"},
			ReleasePlan: v1beta1.ReleasePlan{
				RollingStyle: v1beta1.PartitionRollingStyle,
				Batches: []v1beta1.ReleaseBatch{
					{CanaryReplicas: intstr.FromString("20%")},
					{CanaryReplicas: intstr.FromString("50%")},
					{CanaryReplicas: intstr.FromString("100%")},
				},
			},
		},
		Status: v1beta1.BatchReleaseStatus{
			Phase:          v1beta1.RolloutPhasePreparing,
			StableRevision: "stable",
			UpdateRevision: "updated",
		},
	}
}

func newIntegrationDeployment() *apps.Deployment {
	progressDeadlineSeconds := int32(60)
	maxUnavailable := intstr.FromString("25%")
	maxSurge := intstr.FromInt(1)
	return &apps.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: apps.SchemeGroupVersion.String(), Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "demo",
			Namespace:       "default",
			ResourceVersion: "1",
			UID:             types.UID("integration-deployment-uid"),
			Labels:          map[string]string{"app": "demo"},
		},
		Spec: apps.DeploymentSpec{
			Replicas:                pointer.Int32(10),
			Selector:                &metav1.LabelSelector{MatchLabels: map[string]string{"app": "demo"}},
			Template:                newIntegrationPodTemplate(),
			MinReadySeconds:         5,
			ProgressDeadlineSeconds: &progressDeadlineSeconds,
			Strategy: apps.DeploymentStrategy{
				Type: apps.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &apps.RollingUpdateDeployment{
					MaxUnavailable: &maxUnavailable,
					MaxSurge:       &maxSurge,
				},
			},
		},
		Status: apps.DeploymentStatus{
			Replicas:          10,
			ReadyReplicas:     10,
			UpdatedReplicas:   0,
			AvailableReplicas: 10,
		},
	}
}

func newInflatedIntegrationDeployment() *apps.Deployment {
	deployment := newIntegrationDeployment()
	progressDeadlineSeconds := partitiondeployment.InflatedProgressDeadlineSeconds
	maxUnavailable := intstr.FromInt(0)
	maxSurge := intstr.FromInt(int(partitiondeployment.InflatedMaxSurgeInt))
	deployment.Spec.MinReadySeconds = partitiondeployment.InflatedMinReadySeconds
	deployment.Spec.ProgressDeadlineSeconds = &progressDeadlineSeconds
	deployment.Spec.Strategy.RollingUpdate = &apps.RollingUpdateDeployment{
		MaxUnavailable: &maxUnavailable,
		MaxSurge:       &maxSurge,
	}
	deployment.Annotations = map[string]string{
		partitiondeployment.AnnotationOriginalMinReadySeconds:         "5",
		partitiondeployment.AnnotationOriginalProgressDeadlineSeconds: "60",
		partitiondeployment.AnnotationOriginalMaxUnavailable:          "25%",
		partitiondeployment.AnnotationOriginalMaxSurge:                "1",
	}
	return deployment
}

func newIntegrationPodTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "demo"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "main",
			Image: "busybox:v2",
		}}},
	}
}

func newIntegrationClient(objects ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(integrationScheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1beta1.BatchRelease{}).
		Build()
}

func newIntegrationUpdatedReplicaSet(deployment *apps.Deployment, updateRevision string, replicas, readyReplicas int32) *apps.ReplicaSet {
	return &apps.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployment.Name + "-" + updateRevision,
			Namespace: deployment.Namespace,
			Labels: map[string]string{
				apps.DefaultDeploymentUniqueLabelKey: updateRevision,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: apps.SchemeGroupVersion.String(),
					Kind:       "Deployment",
					Name:       deployment.Name,
					UID:        deployment.UID,
					Controller: pointer.Bool(true),
				},
			},
		},
		Spec: apps.ReplicaSetSpec{
			Replicas: pointer.Int32(replicas),
			Selector: deployment.Spec.Selector.DeepCopy(),
			Template: deployment.Spec.Template,
		},
		Status: apps.ReplicaSetStatus{
			Replicas:      replicas,
			ReadyReplicas: readyReplicas,
		},
	}
}

func newIntegrationUpdatedPods(deployment *apps.Deployment, rs *apps.ReplicaSet, updateRevision, rolloutID string, total, ready int) []*corev1.Pod {
	pods := make([]*corev1.Pod, 0, total)
	for i := 0; i < total; i++ {
		readyCondition := corev1.ConditionFalse
		if i < ready {
			readyCondition = corev1.ConditionTrue
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deployment.Name + "-pod-" + string(rune('a'+i)),
				Namespace: deployment.Namespace,
				Labels: map[string]string{
					"app":                                "demo",
					apps.DefaultDeploymentUniqueLabelKey: updateRevision,
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: apps.SchemeGroupVersion.String(),
					Kind:       "ReplicaSet",
					Name:       rs.Name,
					UID:        rs.UID,
					Controller: pointer.Bool(true),
				}},
			},
			Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
				Type:               corev1.PodReady,
				Status:             readyCondition,
				LastTransitionTime: metav1.NewTime(time.Now().Add(-10 * time.Second)),
			}}},
		}
		if rolloutID != "" {
			pod.Labels[v1beta1.RolloutIDLabel] = rolloutID
		}
		pods = append(pods, pod)
	}
	return pods
}

func appendIntegrationObjects(objects []client.Object, pods []*corev1.Pod) []client.Object {
	for _, pod := range pods {
		objects = append(objects, pod)
	}
	return objects
}

func newIntegrationMinReadyControl(
	cli client.Client,
	recorder record.EventRecorder,
	release *v1beta1.BatchRelease,
	status *v1beta1.BatchReleaseStatus,
	deploymentName string,
) interface {
	Initialize() error
	UpgradeBatch() error
	EnsureBatchPodsReadyAndLabeled() error
	Finalize() error
} {
	return partitionstyle.NewControlPlane(
		partitiondeployment.NewMinReadyController,
		cli,
		recorder,
		release,
		status,
		types.NamespacedName{Namespace: release.Namespace, Name: deploymentName},
		apps.SchemeGroupVersion.WithKind("Deployment"),
	)
}

func fetchIntegrationDeployment(t *testing.T, cli client.Client, deployment *apps.Deployment) *apps.Deployment {
	t.Helper()
	got := &apps.Deployment{}
	key := types.NamespacedName{Namespace: deployment.Namespace, Name: deployment.Name}
	if err := cli.Get(context.TODO(), key, got); err != nil {
		t.Fatalf("Get deployment failed: %v", err)
	}
	return got
}

func assertInflatedDeployment(t *testing.T, deployment *apps.Deployment) {
	t.Helper()
	if deployment.Spec.Strategy.Type != apps.RollingUpdateDeploymentStrategyType {
		t.Fatalf("strategy.type = %q, want RollingUpdate", deployment.Spec.Strategy.Type)
	}
	if deployment.Spec.MinReadySeconds != partitiondeployment.InflatedMinReadySeconds {
		t.Fatalf("minReadySeconds = %d, want %d", deployment.Spec.MinReadySeconds, partitiondeployment.InflatedMinReadySeconds)
	}
	if unavailable := deployment.Spec.Strategy.RollingUpdate.MaxUnavailable; unavailable == nil || unavailable.IntVal != 0 {
		t.Fatalf("maxUnavailable = %v, want 0", unavailable)
	}
	if surge := deployment.Spec.Strategy.RollingUpdate.MaxSurge; surge == nil || surge.IntVal != partitiondeployment.InflatedMaxSurgeInt {
		t.Fatalf("maxSurge = %v, want %d", surge, partitiondeployment.InflatedMaxSurgeInt)
	}
}

func assertOriginalAnnotation(t *testing.T, deployment *apps.Deployment, key, want string) {
	t.Helper()
	if got := deployment.Annotations[key]; got != want {
		t.Fatalf("annotation %s = %q, want %q", key, got, want)
	}
}

func assertIntegrationCondition(
	t *testing.T,
	status *v1beta1.BatchReleaseStatus,
	condType v1beta1.RolloutConditionType,
	condStatus corev1.ConditionStatus,
	reason string,
) {
	t.Helper()
	for _, condition := range status.Conditions {
		if condition.Type == condType && condition.Status == condStatus && condition.Reason == reason {
			return
		}
	}
	t.Fatalf("condition %s with %s/%s not found in %#v", condType, condStatus, reason, status.Conditions)
}

func assertIntegrationEvent(t *testing.T, recorder *record.FakeRecorder, want string) {
	t.Helper()
	for {
		select {
		case event := <-recorder.Events:
			if strings.Contains(event, want) {
				return
			}
		default:
			t.Fatalf("event %q not recorded", want)
		}
	}
}
