package v1alpha1

import (
	apps "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// AdvancedDeploymentStrategyAnnotation is annotation for deployment,
	// which contains AdvancedDeploymentStrategy fields.
	AdvancedDeploymentStrategyAnnotation = "rollouts.kruise.io/advanced-deployment-strategy"
	// AdvancedDeploymentStatusAnnotation is annotation for deployment,
	// which contains AdvancedDeploymentStatus fields.
	AdvancedDeploymentStatusAnnotation = "rollouts.kruise.io/advanced-deployment-status"
	// DeploymentRolloutPolicyAnnotation must be "batch" or "canary":
	// * "batch" means rolling in batches just like CloneSet, and will not create any extra Deployment;
	// * "canary" means rolling in canary way, and will create a canary Deployment.
	// Defaults to canary
	DeploymentRolloutPolicyAnnotation = "rollouts.kruise.io/rolling-policy"
)

type AdvancedDeploymentStrategy struct {
	// original deployment strategy fields
	apps.DeploymentStrategy
	// Paused = true will block the upgrade of Pods
	Paused bool `json:"paused,omitempty"`
	// Partition describe how many Pods should be upgraded during rollout.
	Partition intstr.IntOrString `json:"partition,omitempty"`
	// StableRevision record the user-specified stable revision, and we
	// will scale down stable revision replicaset in the last.
	StableRevision string `json:"stableRevision,omitempty"`
}

type AdvancedDeploymentStatus struct {
	// ObservedGeneration record the generation of deployment this status observed.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// UpdatedReadyReplicas the number of pods that has been upgraded and ready.
	UpdatedReadyReplicas int32 `json:"updatedReadyReplicas,omitempty"`
	// DesiredUpdatedReplicas is an absolute number calculated based on Partition.
	// This field is designed to avoid users to fall into the details of algorithm
	// for Partition calculation.
	DesiredUpdatedReplicas int32 `json:"desiredUpdatedReplicas,omitempty"`
}

func SetDefaultAdvancedDeploymentStrategy(strategy *AdvancedDeploymentStrategy) {
	if strategy.Type == "" {
		strategy.Type = apps.RollingUpdateDeploymentStrategyType
	}
	if strategy.RollingUpdate == nil {
		strategy.RollingUpdate = &apps.RollingUpdateDeployment{
			MaxSurge:       &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
			MaxUnavailable: &intstr.IntOrString{Type: intstr.String, StrVal: "25%"},
		}
	} else {
		maxSurg, _ := intstr.GetScaledValueFromIntOrPercent(strategy.RollingUpdate.MaxSurge, 100, true)
		maxUnva, _ := intstr.GetScaledValueFromIntOrPercent(strategy.RollingUpdate.MaxUnavailable, 100, true)
		// we cannot allow maxSurg==0&&MaxUnvg==0, otherwise, no pod can be upgrade when rolling update.
		if maxSurg+maxUnva <= 0 {
			strategy.RollingUpdate = &apps.RollingUpdateDeployment{
				MaxSurge:       &intstr.IntOrString{Type: intstr.Int, IntVal: 0},
				MaxUnavailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
			}
		}
	}
}
