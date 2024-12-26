package daemonset

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kruiseappsv1alpha1 "github.com/openkruise/kruise-api/apps/v1alpha1"
	rolloutapi "github.com/openkruise/rollouts/api"
	"github.com/openkruise/rollouts/api/v1beta1"
	batchcontext "github.com/openkruise/rollouts/pkg/controller/batchrelease/context"
	"github.com/openkruise/rollouts/pkg/controller/batchrelease/labelpatch"
	"github.com/openkruise/rollouts/pkg/util"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	scheme = runtime.NewScheme()

	daemonKey = types.NamespacedName{
		Namespace: "default",
		Name:      "daemonset",
	}
	daemonDemo = &kruiseappsv1alpha1.DaemonSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps.kruise.io/v1alpha1",
			Kind:       "DaemonSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       daemonKey.Name,
			Namespace:  daemonKey.Namespace,
			Generation: 1,
			Labels: map[string]string{
				"app": "busybox",
			},
			Annotations: map[string]string{
				"type": "unit-test",
			},
		},
		Spec: kruiseappsv1alpha1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "busybox",
				},
			},

			UpdateStrategy: kruiseappsv1alpha1.DaemonSetUpdateStrategy{
				Type: "RollingUpdate",
				RollingUpdate: &kruiseappsv1alpha1.RollingUpdateDaemonSet{
					Paused:         pointer.Bool(true),
					Partition:      pointer.Int32(10),
					MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "busybox",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "busybox",
							Image: "busybox:latest",
						},
					},
				},
			},
		},
		Status: kruiseappsv1alpha1.DaemonSetStatus{
			CurrentNumberScheduled: 10,
			NumberMisscheduled:     0,
			DesiredNumberScheduled: 10,
			NumberReady:            10,
			ObservedGeneration:     1,
			UpdatedNumberScheduled: 10,
			NumberAvailable:        10,
			CollisionCount:         pointer.Int32(1),
		},
	}

	releaseDemo = &v1beta1.BatchRelease{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rollouts.kruise.io/v1alpha1",
			Kind:       "BatchRelease",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "release",
			Namespace: daemonKey.Namespace,
			UID:       uuid.NewUUID(),
		},
		Spec: v1beta1.BatchReleaseSpec{
			ReleasePlan: v1beta1.ReleasePlan{
				Batches: []v1beta1.ReleaseBatch{
					{
						CanaryReplicas: intstr.FromString("10%"),
					},
					{
						CanaryReplicas: intstr.FromString("50%"),
					},
					{
						CanaryReplicas: intstr.FromString("100%"),
					},
				},
			},
			WorkloadRef: v1beta1.ObjectRef{
				APIVersion: daemonDemo.APIVersion,
				Kind:       daemonDemo.Kind,
				Name:       daemonDemo.Name,
			},
		},
		Status: v1beta1.BatchReleaseStatus{
			CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
				CurrentBatch: 0,
			},
		},
	}
)

func init() {
	apps.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	rolloutapi.AddToScheme(scheme)
	kruiseappsv1alpha1.AddToScheme(scheme)
}

func TestCalculateBatchContext(t *testing.T) {
	RegisterFailHandler(Fail)

	percent := intstr.FromString("20%")
	cases := map[string]struct {
		workload func() *kruiseappsv1alpha1.DaemonSet
		release  func() *v1beta1.BatchRelease
		result   *batchcontext.BatchContext
		pods     func() []*corev1.Pod
	}{
		"without NoNeedUpdate": {
			workload: func() *kruiseappsv1alpha1.DaemonSet {
				return &kruiseappsv1alpha1.DaemonSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ds",
						Namespace: "test",
						UID:       "test",
					},
					Spec: kruiseappsv1alpha1.DaemonSetSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}},
						UpdateStrategy: kruiseappsv1alpha1.DaemonSetUpdateStrategy{
							RollingUpdate: &kruiseappsv1alpha1.RollingUpdateDaemonSet{
								Partition: pointer.Int32(10),
							},
						},
					},
					Status: kruiseappsv1alpha1.DaemonSetStatus{
						CurrentNumberScheduled: 10,
						NumberMisscheduled:     0,
						DesiredNumberScheduled: 10,
						NumberReady:            10,
						ObservedGeneration:     1,
						UpdatedNumberScheduled: 5,
						NumberAvailable:        10,
						CollisionCount:         pointer.Int32(1),
						DaemonSetHash:          "update-version",
					},
				}
			},
			pods: func() []*corev1.Pod {
				stablePods := generatePods(5, "stable-version", "True")
				updatedReadyPods := generatePods(5, "update-version", "True")
				return append(stablePods, updatedReadyPods...)
			},
			release: func() *v1beta1.BatchRelease {
				r := &v1beta1.BatchRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-br",
						Namespace: "test",
					},
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							FailureThreshold: &percent,
							Batches: []v1beta1.ReleaseBatch{
								{
									CanaryReplicas: percent,
								},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch: 0,
						},
						UpdateRevision: "update-version",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				FailureThreshold:       &percent,
				CurrentBatch:           0,
				Replicas:               10,
				UpdatedReplicas:        5,
				UpdatedReadyReplicas:   5,
				PlannedUpdatedReplicas: 2,
				DesiredUpdatedReplicas: 2,
				UpdateRevision:         "update-version",
				CurrentPartition:       intstr.FromInt(10),
				DesiredPartition:       intstr.FromInt(8),
				Pods:                   generatePods(10, "", ""),
			},
		},
		"with NoNeedUpdate": {
			workload: func() *kruiseappsv1alpha1.DaemonSet {
				return &kruiseappsv1alpha1.DaemonSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-ds",
						Namespace: "test",
						UID:       "test",
					},
					Spec: kruiseappsv1alpha1.DaemonSetSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "foo"}},
						UpdateStrategy: kruiseappsv1alpha1.DaemonSetUpdateStrategy{
							RollingUpdate: &kruiseappsv1alpha1.RollingUpdateDaemonSet{
								Partition: pointer.Int32(10),
							},
						},
					},
					Status: kruiseappsv1alpha1.DaemonSetStatus{
						CurrentNumberScheduled: 10,
						NumberMisscheduled:     0,
						DesiredNumberScheduled: 10,
						NumberReady:            10,
						ObservedGeneration:     1,
						UpdatedNumberScheduled: 5,
						NumberAvailable:        10,
						CollisionCount:         pointer.Int32(1),
						DaemonSetHash:          "update-version",
					},
				}
			},
			pods: func() []*corev1.Pod {
				stablePods := generatePods(5, "stable-version", "True")
				updatedReadyPods := generatePods(5, "update-version", "True")
				return append(stablePods, updatedReadyPods...)
			},
			release: func() *v1beta1.BatchRelease {

				r := &v1beta1.BatchRelease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-br",
						Namespace: "test",
					},
					Spec: v1beta1.BatchReleaseSpec{
						ReleasePlan: v1beta1.ReleasePlan{
							FailureThreshold: &percent,
							Batches: []v1beta1.ReleaseBatch{
								{
									CanaryReplicas: percent,
								},
							},
						},
					},
					Status: v1beta1.BatchReleaseStatus{
						CanaryStatus: v1beta1.BatchReleaseCanaryStatus{
							CurrentBatch:         0,
							NoNeedUpdateReplicas: pointer.Int32(5),
						},
						UpdateRevision: "update-version",
					},
				}
				return r
			},
			result: &batchcontext.BatchContext{
				CurrentBatch:           0,
				UpdateRevision:         "update-version",
				Replicas:               10,
				UpdatedReplicas:        5,
				UpdatedReadyReplicas:   5,
				NoNeedUpdatedReplicas:  pointer.Int32(5),
				PlannedUpdatedReplicas: 2,
				DesiredUpdatedReplicas: 6,
				CurrentPartition:       intstr.FromInt(10),
				DesiredPartition:       intstr.FromInt(4),
				FailureThreshold:       &percent,
				FilterFunc:             labelpatch.FilterPodsForUnorderedUpdate,
				Pods:                   generatePods(10, "", ""),
			},
		},
	}

	for name, cs := range cases {
		t.Run(name, func(t *testing.T) {
			pods := func() []client.Object {
				var objects []client.Object
				pods := cs.pods()
				for _, pod := range pods {
					objects = append(objects, pod)
				}
				return objects
			}()
			br := cs.release()
			daemon := cs.workload()
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(br, daemon).WithObjects(pods...).Build()
			control := realController{
				client: cli,
				gvk:    daemon.GroupVersionKind(),
				key:    types.NamespacedName{Namespace: "test", Name: "test-ds"},
			}
			controller, err := control.BuildController()
			fmt.Println(err)
			Expect(err).NotTo(HaveOccurred())
			got, err := controller.CalculateBatchContext(cs.release())
			fmt.Println(got.Log())
			fmt.Println(cs.result.Log())
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Log()).Should(Equal(cs.result.Log()))
		})
	}
}

func TestRealController(t *testing.T) {
	RegisterFailHandler(Fail)

	release := releaseDemo.DeepCopy()
	daemon := daemonDemo.DeepCopy()
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(release, daemon.DeepCopy()).Build()
	c := NewController(cli, daemonKey, daemon.GroupVersionKind()).(*realController)
	controller, err := c.BuildController()
	Expect(err).NotTo(HaveOccurred())

	err = controller.Initialize(release)
	Expect(err).NotTo(HaveOccurred())

	fetch := &kruiseappsv1alpha1.DaemonSet{}
	Expect(cli.Get(context.TODO(), daemonKey, fetch)).NotTo(HaveOccurred())

	Expect(*fetch.Spec.UpdateStrategy.RollingUpdate.Paused).Should(BeFalse())

	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(getControlInfo(release)))

	c.object = fetch // mock

	for {
		batchContext, err := controller.CalculateBatchContext(release)
		Expect(err).NotTo(HaveOccurred())
		err = controller.UpgradeBatch(batchContext)
		fetch = &kruiseappsv1alpha1.DaemonSet{}
		// mock
		Expect(cli.Get(context.TODO(), daemonKey, fetch)).NotTo(HaveOccurred())
		c.object = fetch
		if err == nil {
			break
		}
	}

	fetch = &kruiseappsv1alpha1.DaemonSet{}
	Expect(cli.Get(context.TODO(), daemonKey, fetch)).NotTo(HaveOccurred())
	fmt.Println(*fetch.Spec.UpdateStrategy.RollingUpdate.Partition)
	Expect(*fetch.Spec.UpdateStrategy.RollingUpdate.Partition).Should(Equal(int32(9)))

	err = controller.Finalize(release)
	Expect(err).NotTo(HaveOccurred())
	fetch = &kruiseappsv1alpha1.DaemonSet{}
	Expect(cli.Get(context.TODO(), daemonKey, fetch)).NotTo(HaveOccurred())

	Expect(fetch.Annotations[util.BatchReleaseControlAnnotation]).Should(Equal(""))

	stableInfo := controller.GetWorkloadInfo()
	Expect(stableInfo).ShouldNot(BeNil())
	checkWorkloadInfo(stableInfo, daemon)
}

func checkWorkloadInfo(stableInfo *util.WorkloadInfo, daemon *kruiseappsv1alpha1.DaemonSet) {
	Expect(stableInfo.Replicas).Should(Equal(daemon.Status.DesiredNumberScheduled))
	Expect(stableInfo.Status.Replicas).Should(Equal(daemon.Status.DesiredNumberScheduled))
	Expect(stableInfo.Status.ReadyReplicas).Should(Equal(daemon.Status.NumberReady))
	Expect(stableInfo.Status.UpdatedReplicas).Should(Equal(daemon.Status.UpdatedNumberScheduled))
	// how to compare here
	//Expect(stableInfo.Status.UpdatedReadyReplicas).Should(Equal(daemon.Status.UpdatedReadyReplicas))
	Expect(stableInfo.Status.UpdateRevision).Should(Equal(daemon.Status.DaemonSetHash))
	//Expect(stableInfo.Status.StableRevision).Should(Equal(daemon.Status.CurrentRevision))
	Expect(stableInfo.Status.AvailableReplicas).Should(Equal(daemon.Status.NumberAvailable))
	Expect(stableInfo.Status.ObservedGeneration).Should(Equal(daemon.Status.ObservedGeneration))
}

func getControlInfo(release *v1beta1.BatchRelease) string {
	owner, _ := json.Marshal(metav1.NewControllerRef(release, release.GetObjectKind().GroupVersionKind()))
	return string(owner)
}

func generatePods(replicas int, version, readyStatus string) []*corev1.Pod {
	var pods []*corev1.Pod
	for replicas > 0 {
		pods = append(pods, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test",
				Name:      fmt.Sprintf("pod-%s", rand.String(10)),
				Labels: map[string]string{
					"app":                               "foo",
					apps.ControllerRevisionHashLabelKey: version,
				},
				OwnerReferences: []metav1.OwnerReference{{
					UID:        "test",
					Controller: pointer.Bool(true),
				}},
			},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{
					Type:   corev1.PodReady,
					Status: corev1.ConditionStatus(readyStatus),
				}},
			},
		})
		replicas--
	}
	return pods
}
