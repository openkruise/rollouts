package feature

import "flag"

var (
	// If workloadTypeFilterSwitch is true, webhook and controllers
	// will filter out unsupported workload type. Currently, rollout
	// support Deployment, CloneSet, StatefulSet and Advanced StatefulSet.
	// Default to true.
	workloadTypeFilterSwitch bool
)

func init() {
	flag.BoolVar(&workloadTypeFilterSwitch, "filter-workload-type", true, "filter known workload gvk for rollout controller")
}

func NeedFilterWorkloadType() bool {
	return workloadTypeFilterSwitch
}
