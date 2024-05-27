package rollout

import (
	"github.com/openkruise/rollouts/api/v1beta1"
)

type ReleaseManager interface {
	runCanary(c *RolloutContext) error
	doCanaryFinalising(c *RolloutContext) (bool, error)
	fetchBatchRelease(ns, name string) (*v1beta1.BatchRelease, error)
	removeBatchRelease(c *RolloutContext) (bool, error)
}
