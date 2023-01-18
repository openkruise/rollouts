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

package deployment

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"testing"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"

	rolloutsv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
)

func newDControllerRef(d *apps.Deployment) *metav1.OwnerReference {
	isController := true
	return &metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       d.GetName(),
		UID:        d.GetUID(),
		Controller: &isController,
	}
}

// generateRS creates a replica set, with the input deployment's template as its template
func generateRS(deployment apps.Deployment) apps.ReplicaSet {
	template := deployment.Spec.Template.DeepCopy()
	return apps.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			UID:             randomUID(),
			Name:            randomName("replicaset"),
			Labels:          template.Labels,
			OwnerReferences: []metav1.OwnerReference{*newDControllerRef(&deployment)},
		},
		Spec: apps.ReplicaSetSpec{
			Replicas: new(int32),
			Template: *template,
			Selector: &metav1.LabelSelector{MatchLabels: template.Labels},
		},
	}
}

func randomUID() types.UID {
	return types.UID(strconv.FormatInt(rand.Int63(), 10))
}

func randomName(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, strconv.FormatInt(5, 10))
}

// generateDeployment creates a deployment, with the input image as its template
func generateDeployment(image string) apps.Deployment {
	podLabels := map[string]string{"name": image}
	terminationSec := int64(30)
	enableServiceLinks := v1.DefaultEnableServiceLinks
	return apps.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        image,
			Annotations: make(map[string]string),
		},
		Spec: apps.DeploymentSpec{
			Replicas: func(i int32) *int32 { return &i }(1),
			Selector: &metav1.LabelSelector{MatchLabels: podLabels},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:                   image,
							Image:                  image,
							ImagePullPolicy:        v1.PullAlways,
							TerminationMessagePath: v1.TerminationMessagePathDefault,
						},
					},
					DNSPolicy:                     v1.DNSClusterFirst,
					TerminationGracePeriodSeconds: &terminationSec,
					RestartPolicy:                 v1.RestartPolicyAlways,
					SecurityContext:               &v1.PodSecurityContext{},
					EnableServiceLinks:            &enableServiceLinks,
				},
			},
		},
	}
}

func TestScaleDownLimitForOld(t *testing.T) {
	tDeployment := apps.Deployment{Spec: apps.DeploymentSpec{Replicas: pointer.Int32(10)}}
	tReplicaSet := &apps.ReplicaSet{Spec: apps.ReplicaSetSpec{Replicas: pointer.Int32(5)}}
	tOldRSes := func() []*apps.ReplicaSet {
		return []*apps.ReplicaSet{
			{Spec: apps.ReplicaSetSpec{Replicas: pointer.Int32(2)}},
			{Spec: apps.ReplicaSetSpec{Replicas: pointer.Int32(2)}},
			{Spec: apps.ReplicaSetSpec{Replicas: pointer.Int32(2)}},
		}
	}
	tests := map[string]struct {
		deployment func() *apps.Deployment
		oldRSes    func() []*apps.ReplicaSet
		newRS      func() *apps.ReplicaSet
		partition  intstrutil.IntOrString
		expect     int32
	}{
		"ScaleDownLimit > 0": {
			deployment: func() *apps.Deployment {
				return tDeployment.DeepCopy()
			},
			oldRSes: func() []*apps.ReplicaSet {
				return tOldRSes()
			},
			newRS: func() *apps.ReplicaSet {
				return tReplicaSet.DeepCopy()
			},
			partition: intstrutil.FromInt(5),
			expect:    1,
		},
		"ScaleDownLimit = 0": {
			deployment: func() *apps.Deployment {
				return tDeployment.DeepCopy()
			},
			oldRSes: func() []*apps.ReplicaSet {
				return tOldRSes()
			},
			newRS: func() *apps.ReplicaSet {
				newRS := tReplicaSet.DeepCopy()
				newRS.Spec.Replicas = pointer.Int32(4)
				return newRS
			},
			partition: intstrutil.FromInt(4),
			expect:    0,
		},
		"ScaleDownLimit < 0": {
			deployment: func() *apps.Deployment {
				return tDeployment.DeepCopy()
			},
			oldRSes: func() []*apps.ReplicaSet {
				return tOldRSes()
			},
			newRS: func() *apps.ReplicaSet {
				newRS := tReplicaSet.DeepCopy()
				newRS.Spec.Replicas = pointer.Int32(2)
				return newRS
			},
			partition: intstrutil.FromInt(2),
			expect:    -2,
		},
		"newRS replicas > partition": {
			deployment: func() *apps.Deployment {
				return tDeployment.DeepCopy()
			},
			oldRSes: func() []*apps.ReplicaSet {
				return tOldRSes()
			},
			newRS: func() *apps.ReplicaSet {
				return tReplicaSet.DeepCopy()
			},
			partition: intstrutil.FromInt(2),
			expect:    1,
		},
		"newRS replicas < partition": {
			deployment: func() *apps.Deployment {
				return tDeployment.DeepCopy()
			},
			oldRSes: func() []*apps.ReplicaSet {
				return tOldRSes()
			},
			newRS: func() *apps.ReplicaSet {
				newRS := tReplicaSet.DeepCopy()
				newRS.Spec.Replicas = pointer.Int32(2)
				return newRS
			},
			partition: intstrutil.FromInt(5),
			expect:    1,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			result := ScaleDownLimitForOld(test.oldRSes(), test.newRS(), test.deployment(), test.partition)
			if result != test.expect {
				t.Fatalf("expect %d, but got %d", test.expect, result)
			}
		})
	}
}

func TestReconcileNewReplicaSet(t *testing.T) {
	tests := map[string]struct {
		oldRSs     []int32
		newRS      int32
		deployment int32
		partition  intstrutil.IntOrString
		maxSurge   intstrutil.IntOrString
		expect     int32
	}{
		"limited by partition": {
			[]int32{2, 3}, 3, 10,
			intstrutil.FromInt(4), intstrutil.FromInt(0), 4,
		},
		"limited by deployment replicas": {
			[]int32{2, 3}, 3, 10,
			intstrutil.FromInt(10), intstrutil.FromInt(0), 5,
		},
		"surge first": {
			[]int32{10}, 0, 10,
			intstrutil.FromInt(3), intstrutil.FromInt(2), 2,
		},
		"surge first, but limited by partition": {
			[]int32{10}, 0, 10,
			intstrutil.FromInt(2), intstrutil.FromInt(3), 2,
		},
		"partition satisfied, no scale": {
			[]int32{7}, 3, 10,
			intstrutil.FromInt(3), intstrutil.FromInt(3), 3,
		},
		"partition satisfied, no scale even though deployment replicas not reach": {
			[]int32{5}, 3, 10,
			intstrutil.FromInt(3), intstrutil.FromInt(3), 3,
		},
		"new replica set has been greater than partition, no scale down": {
			[]int32{7}, 3, 10,
			intstrutil.FromInt(1), intstrutil.FromInt(3), 3,
		},
		"total replicas are more than deployment desired, no scale down": {
			[]int32{7}, 3, 10,
			intstrutil.FromInt(6), intstrutil.FromInt(0), 3,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset()
			fakeRecord := record.NewFakeRecorder(10)
			dc := &DeploymentController{
				client:        fakeClient,
				eventRecorder: fakeRecord,
				strategy: rolloutsv1alpha1.DeploymentStrategy{
					RollingUpdate: &apps.RollingUpdateDeployment{
						MaxSurge: &test.maxSurge,
					},
					Partition: test.partition,
				},
			}

			var deployment apps.Deployment
			var newRS apps.ReplicaSet
			var allRSs []*apps.ReplicaSet
			{
				deployment = generateDeployment("busybox")
				deployment.Spec.Replicas = pointer.Int32(test.deployment)
				_, err := fakeClient.AppsV1().Deployments(deployment.Namespace).Create(context.TODO(), &deployment, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("got unexpected error: %v", err)
				}
			}
			{
				for index, replicas := range test.oldRSs {
					rs := generateRS(deployment)
					rs.SetName(fmt.Sprintf("rs-%d", index))
					rs.Spec.Replicas = pointer.Int32(replicas)
					allRSs = append(allRSs, &rs)
					_, err := fakeClient.AppsV1().ReplicaSets(rs.Namespace).Create(context.TODO(), &rs, metav1.CreateOptions{})
					if err != nil {
						t.Fatalf("got unexpected error: %v", err)
					}
				}
			}
			{
				newRS = generateRS(deployment)
				newRS.SetName("rs-new")
				newRS.Spec.Replicas = pointer.Int32(test.newRS)
				allRSs = append(allRSs, &newRS)
				_, err := fakeClient.AppsV1().ReplicaSets(newRS.Namespace).Create(context.TODO(), &newRS, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("got unexpected error: %v", err)
				}
			}
			_, err := dc.reconcileNewReplicaSet(context.TODO(), allRSs, &newRS, &deployment)
			if err != nil {
				t.Fatalf("got unexpected error: %v", err)
			}
			result, err := dc.client.AppsV1().ReplicaSets(deployment.Namespace).Get(context.TODO(), newRS.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("got unexpected error: %v", err)
			}
			if *result.Spec.Replicas != test.expect {
				t.Fatalf("expect %d, but got %d", test.expect, *result.Spec.Replicas)
			}
		})
	}
}

func TestReconcileOldReplicaSet(t *testing.T) {
	tests := map[string]struct {
		oldRSsReplicas    []int32
		newRSReplicas     int32
		dReplicas         int32
		oldRSsAvailable   []int32
		newRSAvailable    int32
		dAvailable        int32
		partition         intstrutil.IntOrString
		maxSurge          intstrutil.IntOrString
		maxUnavailable    intstrutil.IntOrString
		expectOldReplicas int32
	}{
		"scale down: all unavailable, limited by partition": {
			[]int32{6, 4}, 0, 10,
			[]int32{0, 0}, 0, 0,
			intstrutil.FromInt(4), intstrutil.FromInt(2), intstrutil.FromString("50%"),
			6,
		},
		"scale down: all available, limited by maxUnavailable": {
			[]int32{6, 4}, 0, 10,
			[]int32{6, 4}, 0, 10,
			intstrutil.FromInt(4), intstrutil.FromInt(2), intstrutil.FromString("20%"),
			8,
		},
		"scale down: scale unavailable first, then scale available, limited by maxUnavailable": {
			[]int32{6, 4}, 0, 10,
			[]int32{6, 2}, 0, 8,
			intstrutil.FromInt(5), intstrutil.FromInt(0), intstrutil.FromString("40%"),
			6,
		},
		"scale down: scale unavailable first, then scale available, limited by partition": {
			[]int32{6, 4}, 0, 10,
			[]int32{6, 2}, 0, 8,
			intstrutil.FromInt(3), intstrutil.FromInt(0), intstrutil.FromString("40%"),
			7,
		},
		"no op: newRS replicas more than partition, limited by newRS replicas": {
			[]int32{0, 5}, 5, 10,
			[]int32{0, 5}, 5, 10,
			intstrutil.FromInt(3), intstrutil.FromInt(0), intstrutil.FromString("40%"),
			5,
		},
		"no op: limited by unavailable newRS": {
			[]int32{3, 4}, 3, 10,
			[]int32{3, 4}, 0, 7,
			intstrutil.FromInt(5), intstrutil.FromInt(0), intstrutil.FromString("30%"),
			7,
		},
		"scale up oldRS": {
			[]int32{3, 0}, 3, 10,
			[]int32{3, 0}, 3, 6,
			intstrutil.FromInt(3), intstrutil.FromInt(0), intstrutil.FromString("30%"),
			7,
		},
		"scale down oldRS": {
			[]int32{3}, 3, 5,
			[]int32{3}, 3, 6,
			intstrutil.FromString("60%"), intstrutil.FromString("25%"), intstrutil.FromString("25%"),
			2,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fakeClient := fake.NewSimpleClientset()
			fakeRecord := record.NewFakeRecorder(10)
			dc := &DeploymentController{
				client:        fakeClient,
				eventRecorder: fakeRecord,
				strategy: rolloutsv1alpha1.DeploymentStrategy{
					RollingUpdate: &apps.RollingUpdateDeployment{
						MaxSurge:       &test.maxSurge,
						MaxUnavailable: &test.maxUnavailable,
					},
					Partition: test.partition,
				},
			}

			var deployment apps.Deployment
			var newRS apps.ReplicaSet
			var allRSs []*apps.ReplicaSet
			var oldRSs []*apps.ReplicaSet
			{
				deployment = generateDeployment("busybox:latest")
				deployment.Spec.Replicas = pointer.Int32(test.dReplicas)
				deployment.Status.ReadyReplicas = test.newRSReplicas
				availableReplicas := test.newRSAvailable
				for _, available := range test.oldRSsAvailable {
					availableReplicas += available
				}
				deployment.Status.UpdatedReplicas = test.newRSReplicas
				deployment.Status.Replicas = availableReplicas
				deployment.Status.AvailableReplicas = availableReplicas
				_, err := fakeClient.AppsV1().Deployments(deployment.Namespace).Create(context.TODO(), &deployment, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("got unexpected error: %v", err)
				}
			}
			{
				for index, replicas := range test.oldRSsReplicas {
					rs := generateRS(deployment)
					rs.SetName(fmt.Sprintf("rs-%d", index))
					rs.Spec.Replicas = pointer.Int32(replicas)
					rs.Status.Replicas = replicas
					rs.Spec.Template.Spec.Containers[0].Image = fmt.Sprintf("old-version-%d", index)
					rs.Status.ReadyReplicas = test.oldRSsAvailable[index]
					rs.Status.AvailableReplicas = test.oldRSsAvailable[index]
					allRSs = append(allRSs, &rs)
					oldRSs = append(oldRSs, &rs)
					_, err := fakeClient.AppsV1().ReplicaSets(rs.Namespace).Create(context.TODO(), &rs, metav1.CreateOptions{})
					if err != nil {
						t.Fatalf("got unexpected error: %v", err)
					}
				}
			}
			{
				newRS = generateRS(deployment)
				newRS.SetName("rs-new")
				newRS.Spec.Replicas = pointer.Int32(test.newRSReplicas)
				newRS.Status.Replicas = test.newRSReplicas
				newRS.Status.ReadyReplicas = test.newRSAvailable
				newRS.Status.AvailableReplicas = test.newRSAvailable
				allRSs = append(allRSs, &newRS)
				_, err := fakeClient.AppsV1().ReplicaSets(newRS.Namespace).Create(context.TODO(), &newRS, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("got unexpected error: %v", err)
				}
			}
			_, err := dc.reconcileOldReplicaSets(context.TODO(), allRSs, oldRSs, &newRS, &deployment)
			if err != nil {
				t.Fatalf("got unexpected error: %v", err)
			}
			rss, err := dc.client.AppsV1().ReplicaSets(deployment.Namespace).List(context.TODO(), metav1.ListOptions{})
			if err != nil {
				t.Fatalf("got unexpected error: %v", err)
			}
			result := int32(0)
			for _, rs := range rss.Items {
				if rs.GetName() != "rs-new" {
					result += *rs.Spec.Replicas
				}
			}
			if result != test.expectOldReplicas {
				t.Fatalf("expect %d, but got %d", test.expectOldReplicas, result)
			}
		})
	}
}
