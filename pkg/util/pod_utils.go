package util

import (
	"context"
	"fmt"
	"strings"

	utilclient "github.com/openkruise/rollouts/pkg/util/client"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IsPodReady returns true if a pod is ready; false otherwise.
func IsPodReady(pod *v1.Pod) bool {
	return IsPodReadyConditionTrue(pod.Status)
}

// IsPodReadyConditionTrue returns true if a pod is ready; false otherwise.
func IsPodReadyConditionTrue(status v1.PodStatus) bool {
	condition := GetPodReadyCondition(status)
	return condition != nil && condition.Status == v1.ConditionTrue
}

// GetPodReadyCondition extracts the pod ready condition from the given status and returns that.
// Returns nil if the condition is not present.
func GetPodReadyCondition(status v1.PodStatus) *v1.PodCondition {
	_, condition := GetPodCondition(&status, v1.PodReady)
	return condition
}

// GetPodCondition extracts the provided condition from the given status and returns that.
// Returns nil and -1 if the condition is not present, and the index of the located condition.
func GetPodCondition(status *v1.PodStatus, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if status == nil {
		return -1, nil
	}
	return GetPodConditionFromList(status.Conditions, conditionType)
}

// GetPodConditionFromList extracts the provided condition from the given list of condition and
// returns the index of the condition and the condition. Returns -1 and nil if the condition is not present.
func GetPodConditionFromList(conditions []v1.PodCondition, conditionType v1.PodConditionType) (int, *v1.PodCondition) {
	if conditions == nil {
		return -1, nil
	}
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return i, &conditions[i]
		}
	}
	return -1, nil
}

func IsConsistentWithRevision(pod *v1.Pod, revision string) bool {
	if strings.HasSuffix(pod.Labels[appsv1.DefaultDeploymentUniqueLabelKey], revision) {
		return true
	}
	if strings.HasSuffix(pod.Labels[appsv1.ControllerRevisionHashLabelKey], revision) {
		return true
	}
	return false
}

func FilterActivePods(pods []*v1.Pod) []*v1.Pod {
	var activePods []*v1.Pod
	for _, pod := range pods {
		if pod.DeletionTimestamp.IsZero() {
			activePods = append(activePods, pod)
		}
	}
	return activePods
}

func IsCompletedPod(pod *v1.Pod) bool {
	return pod.Status.Phase == v1.PodFailed || pod.Status.Phase == v1.PodSucceeded
}

func ListOwnedPods(c client.Client, object client.Object) ([]*v1.Pod, error) {
	selector, err := parseSelector(object)
	if err != nil {
		return nil, err
	}

	podLister := &v1.PodList{}
	err = c.List(context.TODO(), podLister, &client.ListOptions{LabelSelector: selector, Namespace: object.GetNamespace()}, utilclient.DisableDeepCopy)
	if err != nil {
		return nil, err
	}
	pods := make([]*v1.Pod, 0, len(podLister.Items))
	for i := range podLister.Items {
		pod := &podLister.Items[i]
		if IsCompletedPod(pod) {
			continue
		}
		// we should find their indirect owner-relationship,
		// such as pod -> replicaset -> deployment
		owned, err := IsOwnedBy(c, pod, object)
		if err != nil {
			return nil, err
		} else if !owned {
			continue
		}
		pods = append(pods, pod)
	}
	return pods, nil
}

func PatchPodBatchLabel(c client.Client, pods []*v1.Pod, rolloutID string, batchID int32, updateRevision string, canaryGoal int32, logKey types.NamespacedName) (bool, error) {
	// the number of active pods that has been patched successfully.
	patchedUpdatedReplicas := int32(0)
	for _, pod := range pods {
		podRolloutID := pod.Labels[RolloutIDLabel]
		if pod.DeletionTimestamp.IsZero() {
			// we don't patch label for the active old revision pod
			if !IsConsistentWithRevision(pod, updateRevision) {
				continue
			}
			// if it has been patched, count and ignore
			if podRolloutID == rolloutID {
				patchedUpdatedReplicas++
				continue
			}
		}

		// for such terminating pod and others, if it has been patched, just ignore
		if podRolloutID == rolloutID {
			continue
		}

		podClone := pod.DeepCopy()
		by := fmt.Sprintf(`{"metadata":{"labels":{"%s":"%s","%s":"%d"}}}`, RolloutIDLabel, rolloutID, RolloutBatchIDLabel, batchID)
		err := c.Patch(context.TODO(), podClone, client.RawPatch(types.StrategicMergePatchType, []byte(by)))
		if err != nil {
			klog.Errorf("Failed to patch Pod(%v) batchID, err: %v", client.ObjectKeyFromObject(podClone), err)
			return false, err
		}

		if pod.DeletionTimestamp.IsZero() && IsConsistentWithRevision(pod, updateRevision) {
			patchedUpdatedReplicas++
		}
	}

	klog.V(3).Infof("Patch %v pods with batchID for batchRelease %v, goal is %d pods", patchedUpdatedReplicas, logKey, canaryGoal)
	return patchedUpdatedReplicas >= canaryGoal, nil
}
