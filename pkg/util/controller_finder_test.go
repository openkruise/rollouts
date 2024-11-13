package util

import (
	"context"
	"fmt"

	// "reflect"
	"strconv"
	"testing"

	"math/rand"

	appsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutv1alpha1 "github.com/openkruise/rollouts/api/v1alpha1"
	rolloutv1beta1 "github.com/openkruise/rollouts/api/v1beta1"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var namespace string = "unit-test"
var demoRollout rolloutv1beta1.Rollout = rolloutv1beta1.Rollout{
	ObjectMeta: metav1.ObjectMeta{
		Namespace:   namespace,
		Name:        "rollout-demo",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	},
	Spec: rolloutv1beta1.RolloutSpec{
		WorkloadRef: rolloutv1beta1.ObjectRef{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Name:       "deployment-demo",
		},
		Strategy: rolloutv1beta1.RolloutStrategy{
			BlueGreen: &rolloutv1beta1.BlueGreenStrategy{},
		},
	},
	Status: rolloutv1beta1.RolloutStatus{},
}

func TestGetWorkloadForRef(t *testing.T) {
	cases := []struct {
		name           string
		getRollout     func() *rolloutv1beta1.Rollout
		getWorkload    func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet)
		expectWorkload func() *Workload
		err            error
	}{
		{
			name: "cloneset, not in rollout progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "cloneset-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet) {
				return nil, nil, cloneset.DeepCopy()
			},
			expectWorkload: func() *Workload {
				return &Workload{
					TypeMeta: metav1.TypeMeta{
						Kind:       "CloneSet",
						APIVersion: "apps.kruise.io/v1alpha1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "unit-test",
						Name:      "cloneset-demo",
						Annotations: map[string]string{
							"rollouts.kruise.io/unit-test-anno": "true",
						},
						Labels: map[string]string{
							"rollouts.kruise.io/unit-test-label": "true",
							"rollouts.kruise.io/stable-revision": "stable",
						},
					},
					Replicas:           10,
					StableRevision:     "version1",
					CanaryRevision:     "version2",
					PodTemplateHash:    "version2",
					RevisionLabelKey:   "pod-template-hash",
					IsStatusConsistent: true,
				}
			},
		},
		{
			name: "cloneset,in rollout progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "cloneset-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet) {
				cs := cloneset.DeepCopy()
				cs.Annotations[InRolloutProgressingAnnotation] = "true"
				return nil, nil, cs
			},
			expectWorkload: func() *Workload {
				return &Workload{
					TypeMeta: metav1.TypeMeta{
						Kind:       "CloneSet",
						APIVersion: "apps.kruise.io/v1alpha1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "unit-test",
						Name:      "cloneset-demo",
						Annotations: map[string]string{
							"rollouts.kruise.io/unit-test-anno": "true",
						},
						Labels: map[string]string{
							"rollouts.kruise.io/unit-test-label": "true",
							"rollouts.kruise.io/stable-revision": "stable",
						},
					},
					Replicas:             10,
					StableRevision:       "version1",
					CanaryRevision:       "version2",
					PodTemplateHash:      "version2",
					RevisionLabelKey:     "pod-template-hash",
					IsStatusConsistent:   true,
					InRolloutProgressing: true,
				}
			},
		},
		{
			name: "in rollback progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "cloneset-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet) {
				cs := cloneset.DeepCopy()
				cs.Annotations[InRolloutProgressingAnnotation] = "true"
				cs.Status.CurrentRevision = "version2"
				cs.Status.UpdateRevision = "version2"
				cs.Status.UpdatedReplicas = 5
				return nil, nil, cs
			},
			expectWorkload: func() *Workload {
				return &Workload{
					TypeMeta: metav1.TypeMeta{
						Kind:       "CloneSet",
						APIVersion: "apps.kruise.io/v1alpha1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "unit-test",
						Name:      "cloneset-demo",
						Annotations: map[string]string{
							"rollouts.kruise.io/unit-test-anno": "true",
						},
						Labels: map[string]string{
							"rollouts.kruise.io/unit-test-label": "true",
							"rollouts.kruise.io/stable-revision": "stable",
						},
					},
					Replicas:             10,
					StableRevision:       "version2",
					CanaryRevision:       "version2",
					PodTemplateHash:      "version2",
					RevisionLabelKey:     "pod-template-hash",
					IsStatusConsistent:   true,
					InRolloutProgressing: true,
					IsInRollback:         true,
				}
			},
		},
		{
			name: "deployment: not in rollout progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "deployment-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet) {
				dep := deployment.DeepCopy()
				dep.Labels[rolloutv1alpha1.DeploymentStableRevisionLabel] = "stable"
				rs := generateRS(*dep)
				rs.Namespace = namespace
				rs.Spec.Replicas = dep.Spec.Replicas
				rs.Labels[apps.DefaultDeploymentUniqueLabelKey] = "c9dcf87d5"
				return dep, &rs, nil
			},
			expectWorkload: func() *Workload {
				return &Workload{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Deployment",
						APIVersion: "apps/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "unit-test",
						Name:      "deployment-demo",
						Annotations: map[string]string{
							"rollouts.kruise.io/unit-test-anno": "true",
						},
						Labels: map[string]string{
							"rollouts.kruise.io/unit-test-label": "true",
							"rollouts.kruise.io/stable-revision": "stable",
						},
					},
					Replicas:           10,
					StableRevision:     "stable",
					CanaryRevision:     "c9dcf87d5",
					PodTemplateHash:    "",
					RevisionLabelKey:   "pod-template-hash",
					IsStatusConsistent: true,
				}
			},
		},
		{
			name: "in rollout progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "deployment-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet) {
				dep := deployment.DeepCopy()
				dep.Labels[rolloutv1alpha1.DeploymentStableRevisionLabel] = "stable"
				dep.Annotations[InRolloutProgressingAnnotation] = "true"
				rs := generateRS(*dep)
				rs.Namespace = namespace
				rs.Spec.Replicas = dep.Spec.Replicas
				rs.Labels[apps.DefaultDeploymentUniqueLabelKey] = "c9dcf87d5"
				return dep, &rs, nil
			},
			expectWorkload: func() *Workload {
				return &Workload{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Deployment",
						APIVersion: "apps/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "unit-test",
						Name:      "deployment-demo",
					},
					Replicas:             10,
					StableRevision:       "stable",
					CanaryRevision:       "c9dcf87d5",
					PodTemplateHash:      "c9dcf87d5",
					RevisionLabelKey:     "pod-template-hash",
					IsStatusConsistent:   true,
					InRolloutProgressing: true,
				}
			},
		},
		{
			name: "in rollback",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "deployment-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet) {
				dep := deployment.DeepCopy()
				dep.Labels[rolloutv1alpha1.DeploymentStableRevisionLabel] = "stable"
				dep.Annotations[InRolloutProgressingAnnotation] = "true"
				rs := generateRS(*dep)
				rs.Namespace = namespace
				rs.Spec.Replicas = dep.Spec.Replicas
				// the newst revision is stable
				rs.Labels[apps.DefaultDeploymentUniqueLabelKey] = "stable"
				return dep, &rs, nil
			},
			expectWorkload: func() *Workload {
				return &Workload{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Deployment",
						APIVersion: "apps/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "unit-test",
						Name:      "deployment-demo",
					},
					Replicas:             10,
					StableRevision:       "stable",
					CanaryRevision:       "c9dcf87d5",
					PodTemplateHash:      "stable",
					RevisionLabelKey:     "pod-template-hash",
					IsStatusConsistent:   true,
					InRolloutProgressing: true,
					IsInRollback:         true,
				}
			},
		},
		{
			name: "not consistent",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "deployment-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet) {
				dep := deployment.DeepCopy()
				// modify generation
				dep.Generation = 12
				dep.Labels[rolloutv1alpha1.DeploymentStableRevisionLabel] = "stable"
				dep.Annotations[InRolloutProgressingAnnotation] = "true"
				rs := generateRS(*dep)
				rs.Namespace = namespace
				rs.Spec.Replicas = dep.Spec.Replicas
				rs.Labels[apps.DefaultDeploymentUniqueLabelKey] = "c9dcf87d5"
				return dep, &rs, nil
			},
			expectWorkload: func() *Workload {
				return &Workload{
					IsStatusConsistent: false,
				}
			},
		},
	}
	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			rollout := cs.getRollout()
			dp, rs, cloneset := cs.getWorkload()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout).Build()
			if dp != nil {
				_ = cli.Create(context.TODO(), rs)
			}
			if rs != nil {
				_ = cli.Create(context.TODO(), dp)
			}
			if cloneset != nil {
				_ = cli.Create(context.TODO(), cloneset)
			}
			finder := NewControllerFinder(cli)
			workload, err := finder.GetWorkloadForRef(rollout)
			if !checkWorkloadEqual(workload, cs.expectWorkload()) {
				t.Fatal("expected workload not equal got workload")
			}
			if res := checkErrorEqual(err, cs.err); res != "" {
				t.Fatal(res)
			}
		})
	}
}

func checkErrorEqual(g, e error) string {
	gotError, expectedError := "none", "none"
	if g != nil {
		gotError = g.Error()
	}
	if e != nil {
		gotError = e.Error()
	}
	if gotError != expectedError {
		return fmt.Sprintf("expected error %s, but got error %s,", expectedError, gotError)
	}
	return ""
}

// checkWorkloadEqual compares two Workload pointers for equality.
func checkWorkloadEqual(a, b *Workload) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	// Compare TypeMeta
	if a.Kind != b.Kind || a.APIVersion != b.APIVersion {
		return false
	}

	// Compare ObjectMeta for specified fields (Namespace, Name, Annotations, Labels)
	if !objectMetaEqual(a.ObjectMeta, b.ObjectMeta) {
		return false
	}

	// Compare other fields
	return a.Replicas == b.Replicas &&
		a.StableRevision == b.StableRevision &&
		a.CanaryRevision == b.CanaryRevision &&
		a.PodTemplateHash == b.PodTemplateHash &&
		a.RevisionLabelKey == b.RevisionLabelKey &&
		a.IsInRollback == b.IsInRollback &&
		a.InRolloutProgressing == b.InRolloutProgressing &&
		a.IsStatusConsistent == b.IsStatusConsistent
}

// objectMetaEqual compares the specified fields of ObjectMeta.
func objectMetaEqual(a, b metav1.ObjectMeta) bool {
	return a.Namespace == b.Namespace &&
		a.Name == b.Name
}

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
