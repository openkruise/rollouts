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

package grace

import (
	"time"

	"k8s.io/klog/v2"
)

func RunWithGraceSeconds(key, action string, graceSeconds int32, f func() (bool, error)) (bool, time.Duration, error) {
	return runWithGraceSeconds(DefaultGraceExpectations, key, action, graceSeconds, f)
}

// Returns:
// - error: If the passed function itself returns an error, the error is directly returned.
// - bool: Tells the caller whether a retry is needed.
// - time.Duration: The remaining time to wait. (only valid when error is nil)
// The passed function `f` needs to be idempotent.
// - The bool value returned by `f` indicates whether the related resources were updated in this call.
// - If resources were updated, we need to wait for `graceSeconds`.
func runWithGraceSeconds(e *realGraceExpectations, key, action string, graceSeconds int32, f func() (bool, error)) (bool, time.Duration, error) {
	modified, err := f()
	if err != nil {
		return true, 0, err
	}
	// if user specify 0, it means no need to wait
	if graceSeconds == 0 {
		e.Observe(key, Action(action))
		return false, 0, nil
	}
	// if f return true, it means some resources are modified by f in this call
	// we need to wait a grace period
	if modified {
		e.Expect(key, Action(action))
		klog.Infof("function return modified, expectation created, key: %s, action: %s, expect to wait %d seconds", key, action, graceSeconds)
		return true, time.Duration(graceSeconds) * time.Second, nil
	}
	if satisfied, remaining := e.SatisfiedExpectations(key, Action(action), graceSeconds); !satisfied {
		klog.Infof("expectation unsatisfied, key: %s, action: %s, remaining/graceSeconds: %.1f/%d", key, action, remaining.Seconds(), graceSeconds)
		return true, remaining, nil
	}
	e.Observe(key, Action(action))
	klog.Infof("expectation satisfied, key: %s, action: %s", key, action)
	return false, 0, nil
}

// warning: only used for test
func ResetExpectations() {
	DefaultGraceExpectations.resetExpectations()
}
