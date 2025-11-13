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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

var nativeDaemonSet = apps.DaemonSet{
	TypeMeta: metav1.TypeMeta{
		APIVersion: apps.SchemeGroupVersion.String(),
		Kind:       "DaemonSet",
	},
	ObjectMeta: metav1.ObjectMeta{
		Namespace:  namespace,
		Name:       "daemonset-demo",
		Generation: 10,
		UID:        uuid.NewUUID(),
		Annotations: map[string]string{
			"rollouts.kruise.io/unit-test-anno": "true",
		},
		Labels: map[string]string{
			"rollouts.kruise.io/unit-test-label": "true",
		},
	},
	Spec: apps.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app": "demo",
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "demo",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "main",
						Image: "busybox:1.32",
					},
				},
			},
		},
	},
	Status: apps.DaemonSetStatus{
		ObservedGeneration:     10,
		DesiredNumberScheduled: 5,
		NumberReady:            4,
		UpdatedNumberScheduled: 3,
		NumberAvailable:        4,
	},
}

func TestGetWorkloadForRef(t *testing.T) {
	cases := []struct {
		name           string
		getRollout     func() *rolloutv1beta1.Rollout
		getWorkload    func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision)
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
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				return nil, nil, cloneset.DeepCopy(), nil, nil
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
			name: "cloneset in rollout progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "cloneset-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				cs := cloneset.DeepCopy()
				cs.Annotations[InRolloutProgressingAnnotation] = "true"
				return nil, nil, cs, nil, nil
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
			name: "cloneset in rollback progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps.kruise.io/v1alpha1",
					Kind:       "CloneSet",
					Name:       "cloneset-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				cs := cloneset.DeepCopy()
				cs.Annotations[InRolloutProgressingAnnotation] = "true"
				cs.Status.CurrentRevision = "version2"
				cs.Status.UpdateRevision = "version2"
				cs.Status.UpdatedReplicas = 5
				return nil, nil, cs, nil, nil
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
			name: "deployment not in rollout progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "deployment-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				dep := deployment.DeepCopy()
				dep.Labels[rolloutv1alpha1.DeploymentStableRevisionLabel] = "stable"
				rs := generateRS(*dep)
				rs.Namespace = namespace
				rs.Spec.Replicas = dep.Spec.Replicas
				rs.Labels[apps.DefaultDeploymentUniqueLabelKey] = "cd68dc9"
				return dep, &rs, nil, nil, nil
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
					CanaryRevision:     "cd68dc9",
					PodTemplateHash:    "",
					RevisionLabelKey:   "pod-template-hash",
					IsStatusConsistent: true,
				}
			},
		},
		{
			name: "deployment in rollout progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "deployment-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				dep := deployment.DeepCopy()
				dep.Labels[rolloutv1alpha1.DeploymentStableRevisionLabel] = "stable"
				dep.Annotations[InRolloutProgressingAnnotation] = "true"
				rs := generateRS(*dep)
				rs.Namespace = namespace
				rs.Spec.Replicas = dep.Spec.Replicas
				rs.Labels[apps.DefaultDeploymentUniqueLabelKey] = "cd68dc9"
				return dep, &rs, nil, nil, nil
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
					CanaryRevision:       "cd68dc9",
					PodTemplateHash:      "cd68dc9",
					RevisionLabelKey:     "pod-template-hash",
					IsStatusConsistent:   true,
					InRolloutProgressing: true,
				}
			},
		},
		{
			name: "deployment in rollback",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "deployment-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				dep := deployment.DeepCopy()
				dep.Labels[rolloutv1alpha1.DeploymentStableRevisionLabel] = "stable"
				dep.Annotations[InRolloutProgressingAnnotation] = "true"
				rs := generateRS(*dep)
				rs.Namespace = namespace
				rs.Spec.Replicas = dep.Spec.Replicas
				// the newst revision is stable
				rs.Labels[apps.DefaultDeploymentUniqueLabelKey] = "stable"
				return dep, &rs, nil, nil, nil
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
					CanaryRevision:       "cd68dc9",
					PodTemplateHash:      "stable",
					RevisionLabelKey:     "pod-template-hash",
					IsStatusConsistent:   true,
					InRolloutProgressing: true,
					IsInRollback:         true,
				}
			},
		},
		{
			name: "deployment not consistent",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "deployment-demo",
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				dep := deployment.DeepCopy()
				// modify generation
				dep.Generation = 12
				dep.Labels[rolloutv1alpha1.DeploymentStableRevisionLabel] = "stable"
				dep.Annotations[InRolloutProgressingAnnotation] = "true"
				rs := generateRS(*dep)
				rs.Namespace = namespace
				rs.Spec.Replicas = dep.Spec.Replicas
				rs.Labels[apps.DefaultDeploymentUniqueLabelKey] = "c9dcf87d5"
				return dep, &rs, nil, nil, nil
			},
			expectWorkload: func() *Workload {
				return &Workload{
					IsStatusConsistent: false,
				}
			},
		},
		{
			name: "native daemonset, not in rollout progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemonset-demo",
				}
				rollout.Spec.Strategy = rolloutv1beta1.RolloutStrategy{
					Canary: &rolloutv1beta1.CanaryStrategy{},
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				return nil, nil, nil, nativeDaemonSet.DeepCopy(), nil
			},
			expectWorkload: func() *Workload {
				return &Workload{
					TypeMeta: metav1.TypeMeta{
						Kind:       "DaemonSet",
						APIVersion: "apps/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "daemonset-demo",
						Annotations: map[string]string{
							"rollouts.kruise.io/unit-test-anno": "true",
						},
						Labels: map[string]string{
							"rollouts.kruise.io/unit-test-label": "true",
						},
					},
					Replicas:           5,
					RevisionLabelKey:   apps.ControllerRevisionHashLabelKey,
					IsStatusConsistent: true,
				}
			},
		},
		{
			name: "native daemonset in rollout progress",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemonset-demo",
				}
				rollout.Spec.Strategy = rolloutv1beta1.RolloutStrategy{
					Canary: &rolloutv1beta1.CanaryStrategy{},
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				ds := nativeDaemonSet.DeepCopy()
				ds.Annotations[InRolloutProgressingAnnotation] = "true"

				// Create ControllerRevisions
				revisions := []*apps.ControllerRevision{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "daemonset-demo-rev2",
							Namespace: namespace,
							Labels: map[string]string{
								apps.ControllerRevisionHashLabelKey: "rev2hash",
							},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion: "apps/v1",
									Kind:       "DaemonSet",
									Name:       "daemonset-demo",
									UID:        ds.UID,
									Controller: pointer.Bool(true),
								},
							},
						},
						Revision: 2,
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "daemonset-demo-rev1",
							Namespace: namespace,
							Labels: map[string]string{
								apps.ControllerRevisionHashLabelKey: "rev1hash",
							},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion: "apps/v1",
									Kind:       "DaemonSet",
									Name:       "daemonset-demo",
									UID:        ds.UID,
									Controller: pointer.Bool(true),
								},
							},
						},
						Revision: 1,
					},
				}
				return nil, nil, nil, ds, revisions
			},
			expectWorkload: func() *Workload {
				return &Workload{
					TypeMeta: metav1.TypeMeta{
						Kind:       "DaemonSet",
						APIVersion: "apps/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "daemonset-demo",
					},
					Replicas:             5,
					StableRevision:       "rev1hash",
					CanaryRevision:       "rev2hash",
					PodTemplateHash:      "rev2hash",
					RevisionLabelKey:     apps.ControllerRevisionHashLabelKey,
					IsStatusConsistent:   true,
					InRolloutProgressing: true,
				}
			},
		},
		{
			name: "native daemonset in rollback",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemonset-demo",
				}
				rollout.Spec.Strategy = rolloutv1beta1.RolloutStrategy{
					Canary: &rolloutv1beta1.CanaryStrategy{},
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				ds := nativeDaemonSet.DeepCopy()
				ds.Annotations[InRolloutProgressingAnnotation] = "true"

				// Create ControllerRevisions where stable and canary are the same (rollback scenario)
				revisions := []*apps.ControllerRevision{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "daemonset-demo-rev1",
							Namespace: namespace,
							Labels: map[string]string{
								apps.ControllerRevisionHashLabelKey: "rev1hash",
							},
							OwnerReferences: []metav1.OwnerReference{
								{
									APIVersion: "apps/v1",
									Kind:       "DaemonSet",
									Name:       "daemonset-demo",
									UID:        ds.UID,
									Controller: pointer.Bool(true),
								},
							},
						},
						Revision: 1,
					},
				}
				return nil, nil, nil, ds, revisions
			},
			expectWorkload: func() *Workload {
				return &Workload{
					TypeMeta: metav1.TypeMeta{
						Kind:       "DaemonSet",
						APIVersion: "apps/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "daemonset-demo",
					},
					Replicas:             5,
					StableRevision:       "rev1hash",
					CanaryRevision:       "rev1hash",
					PodTemplateHash:      "rev1hash",
					RevisionLabelKey:     apps.ControllerRevisionHashLabelKey,
					IsStatusConsistent:   true,
					InRolloutProgressing: true,
					IsInRollback:         true,
				}
			},
		},
		{
			name: "native daemonset not consistent",
			getRollout: func() *rolloutv1beta1.Rollout {
				rollout := demoRollout.DeepCopy()
				rollout.Spec.WorkloadRef = rolloutv1beta1.ObjectRef{
					APIVersion: "apps/v1",
					Kind:       "DaemonSet",
					Name:       "daemonset-demo",
				}
				rollout.Spec.Strategy = rolloutv1beta1.RolloutStrategy{
					Canary: &rolloutv1beta1.CanaryStrategy{},
				}
				return rollout
			},
			getWorkload: func() (*apps.Deployment, *apps.ReplicaSet, *appsv1alpha1.CloneSet, *apps.DaemonSet, []*apps.ControllerRevision) {
				ds := nativeDaemonSet.DeepCopy()
				// modify generation to make it inconsistent
				ds.Generation = 12
				ds.Annotations[InRolloutProgressingAnnotation] = "true"
				return nil, nil, nil, ds, nil
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
			dp, rs, cloneset, daemonset, revisions := cs.getWorkload()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout).Build()
			if dp != nil {
				_ = cli.Create(context.TODO(), dp)
			}
			if rs != nil {
				_ = cli.Create(context.TODO(), rs)
			}
			if cloneset != nil {
				_ = cli.Create(context.TODO(), cloneset)
			}
			if daemonset != nil {
				_ = cli.Create(context.TODO(), daemonset)
			}
			if revisions != nil {
				for _, rev := range revisions {
					_ = cli.Create(context.TODO(), rev)
				}
			}
			finder := NewControllerFinder(cli)
			workload, err := finder.GetWorkloadForRef(rollout)
			if !checkWorkloadEqual(workload, cs.expectWorkload()) {
				t.Fatalf("expected workload not equal got workload: \n expected: %v \n got: %v", DumpJSON(cs.expectWorkload()), DumpJSON(workload))
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

func TestGetControllerRevisionsForDaemonSet(t *testing.T) {
	cases := []struct {
		name                string
		daemonSet           *apps.DaemonSet
		controllerRevisions []*apps.ControllerRevision
		expectedCount       int
		expectedOrder       []int64 // expected revision numbers in order
	}{
		{
			name:                "no controller revisions",
			daemonSet:           nativeDaemonSet.DeepCopy(),
			controllerRevisions: []*apps.ControllerRevision{},
			expectedCount:       0,
			expectedOrder:       []int64{},
		},
		{
			name:      "single controller revision",
			daemonSet: nativeDaemonSet.DeepCopy(),
			controllerRevisions: []*apps.ControllerRevision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-demo-rev1",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "apps/v1",
								Kind:       "DaemonSet",
								Name:       "daemonset-demo",
								UID:        nativeDaemonSet.UID,
								Controller: pointer.Bool(true),
							},
						},
					},
					Revision: 1,
				},
			},
			expectedCount: 1,
			expectedOrder: []int64{1},
		},
		{
			name:      "multiple controller revisions sorted by revision number",
			daemonSet: nativeDaemonSet.DeepCopy(),
			controllerRevisions: []*apps.ControllerRevision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-demo-rev1",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "apps/v1",
								Kind:       "DaemonSet",
								Name:       "daemonset-demo",
								UID:        nativeDaemonSet.UID,
								Controller: pointer.Bool(true),
							},
						},
					},
					Revision: 1,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-demo-rev3",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "apps/v1",
								Kind:       "DaemonSet",
								Name:       "daemonset-demo",
								UID:        nativeDaemonSet.UID,
								Controller: pointer.Bool(true),
							},
						},
					},
					Revision: 3,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-demo-rev2",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "apps/v1",
								Kind:       "DaemonSet",
								Name:       "daemonset-demo",
								UID:        nativeDaemonSet.UID,
								Controller: pointer.Bool(true),
							},
						},
					},
					Revision: 2,
				},
			},
			expectedCount: 3,
			expectedOrder: []int64{3, 2, 1}, // should be sorted in descending order
		},
		{
			name:      "controller revisions with different owners",
			daemonSet: nativeDaemonSet.DeepCopy(),
			controllerRevisions: []*apps.ControllerRevision{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-demo-rev1",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "apps/v1",
								Kind:       "DaemonSet",
								Name:       "daemonset-demo",
								UID:        nativeDaemonSet.UID,
								Controller: pointer.Bool(true),
							},
						},
					},
					Revision: 1,
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-daemonset-rev1",
						Namespace: namespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "apps/v1",
								Kind:       "DaemonSet",
								Name:       "other-daemonset",
								UID:        "different-uid",
								Controller: pointer.Bool(true),
							},
						},
					},
					Revision: 2,
				},
			},
			expectedCount: 1, // only one should match our DaemonSet
			expectedOrder: []int64{1},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			// Create fake client with DaemonSet and ControllerRevisions
			objects := []client.Object{cs.daemonSet}
			for _, rev := range cs.controllerRevisions {
				objects = append(objects, rev)
			}
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			finder := NewControllerFinder(cli)

			// Call the function
			revisions, err := finder.getControllerRevisionsForDaemonSet(cs.daemonSet)

			// Verify results
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(revisions) != cs.expectedCount {
				t.Fatalf("expected %d revisions, got %d", cs.expectedCount, len(revisions))
			}

			// Verify order
			for i, expectedRevision := range cs.expectedOrder {
				if revisions[i].Revision != expectedRevision {
					t.Fatalf("expected revision %d at index %d, got %d", expectedRevision, i, revisions[i].Revision)
				}
			}
		})
	}
}

func TestPatchDaemonSetRevisionAnnotations(t *testing.T) {
	cases := []struct {
		name                string
		daemonSet           *apps.DaemonSet
		stableRevision      string
		canaryRevision      string
		expectPatch         bool
		expectedAnnotations map[string]string
	}{
		{
			name:           "add annotations to daemonset without annotations",
			daemonSet:      nativeDaemonSet.DeepCopy(),
			stableRevision: "stable-hash",
			canaryRevision: "canary-hash",
			expectPatch:    true,
			expectedAnnotations: map[string]string{
				"rollouts.kruise.io/unit-test-anno": "true",
				DaemonSetRevisionAnnotation:         `{"canary-revision":"canary-hash","stable-revision":"stable-hash"}`,
			},
		},
		{
			name: "update existing annotations",
			daemonSet: func() *apps.DaemonSet {
				ds := nativeDaemonSet.DeepCopy()
				SetDaemonSetRevision(ds.Annotations, "old-canary", "old-stable")
				return ds
			}(),
			stableRevision: "new-stable",
			canaryRevision: "new-canary",
			expectPatch:    true,
			expectedAnnotations: map[string]string{
				"rollouts.kruise.io/unit-test-anno": "true",
				DaemonSetRevisionAnnotation:         `{"canary-revision":"new-canary","stable-revision":"new-stable"}`,
			},
		},
		{
			name: "no patch needed when annotations are already correct",
			daemonSet: func() *apps.DaemonSet {
				ds := nativeDaemonSet.DeepCopy()
				SetDaemonSetRevision(ds.Annotations, "correct-canary", "correct-stable")
				return ds
			}(),
			stableRevision: "correct-stable",
			canaryRevision: "correct-canary",
			expectPatch:    false,
			expectedAnnotations: map[string]string{
				"rollouts.kruise.io/unit-test-anno": "true",
				DaemonSetRevisionAnnotation:         `{"canary-revision":"correct-canary","stable-revision":"correct-stable"}`,
			},
		},
		{
			name: "add only canary revision",
			daemonSet: func() *apps.DaemonSet {
				ds := nativeDaemonSet.DeepCopy()
				SetDaemonSetRevision(ds.Annotations, "", "existing-stable")
				return ds
			}(),
			stableRevision: "existing-stable", // keep existing stable
			canaryRevision: "new-canary",
			expectPatch:    true,
			expectedAnnotations: map[string]string{
				"rollouts.kruise.io/unit-test-anno": "true",
				DaemonSetRevisionAnnotation:         `{"canary-revision":"new-canary","stable-revision":"existing-stable"}`,
			},
		},
	}

	for _, cs := range cases {
		t.Run(cs.name, func(t *testing.T) {
			// Create fake client with DaemonSet
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cs.daemonSet).Build()
			finder := NewControllerFinder(cli)

			// Call the function
			err := finder.patchDaemonSetRevisionAnnotations(cs.daemonSet, cs.stableRevision, cs.canaryRevision)

			// Verify no error
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify annotations
			for key, expectedValue := range cs.expectedAnnotations {
				if actualValue, exists := cs.daemonSet.Annotations[key]; !exists {
					t.Fatalf("expected annotation %s to exist", key)
				} else if actualValue != expectedValue {
					t.Fatalf("expected annotation %s to be %s, got %s", key, expectedValue, actualValue)
				}
			}
		})
	}
}
