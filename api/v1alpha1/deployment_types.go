package v1alpha1

import (
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// DeploymentStrategyAnnotation is annotation for deployment,
	// which is strategy fields of Advanced Deployment.
	DeploymentStrategyAnnotation = "rollouts.kruise.io/deployment-strategy"

	// DeploymentExtraStatusAnnotation is annotation for deployment,
	// which is extra status field of Advanced Deployment.
	DeploymentExtraStatusAnnotation = "rollouts.kruise.io/deployment-extra-status"
)

// DeploymentStrategy is strategy field for Advanced Deployment
type DeploymentStrategy struct {
	// RollingStyle define the behavior of rolling for deployment.
	RollingStyle RollingStyleType `json:"rollingStyle,omitempty"`
	// original deployment strategy rolling update fields
	RollingUpdate *apps.RollingUpdateDeployment `json:"rollingUpdate,omitempty"`
	// Paused = true will block the upgrade of Pods
	Paused bool `json:"paused,omitempty"`
	// Partition describe how many Pods should be updated during rollout.
	// We use this field to implement partition-style rolling update.
	Partition intstr.IntOrString `json:"partition,omitempty"`
}

type RollingStyleType string

const (
	// PartitionRollingStyleType means rolling in batches just like CloneSet, and will NOT create any extra Deployment;
	PartitionRollingStyleType RollingStyleType = "Partition"
	// CanaryRollingStyleType means rolling in canary way, and will create a canary Deployment.
	CanaryRollingStyleType RollingStyleType = "Canary"
)

// DeploymentExtraStatus is extra status field for Advanced Deployment
type DeploymentExtraStatus struct {
	// ObservedGeneration record the generation of deployment this status observed.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// UpdatedReadyReplicas the number of pods that has been updated and ready.
	UpdatedReadyReplicas int32 `json:"updatedReadyReplicas,omitempty"`
	// ExpectedUpdatedReplicas is an absolute number calculated based on Partition
	// and Deployment.Spec.Replicas, means how many pods are expected be updated under
	// current strategy.
	// This field is designed to avoid users to fall into the details of algorithm
	// for Partition calculation.
	ExpectedUpdatedReplicas int32 `json:"expectedUpdatedReplicas,omitempty"`
}

func SetDefaultDeploymentStrategy(strategy *DeploymentStrategy) {
	if strategy.RollingStyle == CanaryRollingStyleType {
		return
	}
	if strategy.RollingUpdate == nil {
		strategy.RollingUpdate = &apps.RollingUpdateDeployment{}
	}
	if strategy.RollingUpdate.MaxUnavailable == nil {
		// Set MaxUnavailable as 25% by default
		maxUnavailable := intstr.FromString("25%")
		strategy.RollingUpdate.MaxUnavailable = &maxUnavailable
	}
	if strategy.RollingUpdate.MaxSurge == nil {
		// Set MaxSurge as 25% by default
		maxSurge := intstr.FromString("25%")
		strategy.RollingUpdate.MaxUnavailable = &maxSurge
	}

	// Cannot allow maxSurge==0 && MaxUnavailable==0, otherwise, no pod can be updated when rolling update.
	maxSurge, _ := intstr.GetScaledValueFromIntOrPercent(strategy.RollingUpdate.MaxSurge, 100, true)
	maxUnavailable, _ := intstr.GetScaledValueFromIntOrPercent(strategy.RollingUpdate.MaxUnavailable, 100, true)
	if maxSurge == 0 && maxUnavailable == 0 {
		strategy.RollingUpdate = &apps.RollingUpdateDeployment{
			MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: 0},
			MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
		}
	}
}
