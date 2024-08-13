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
	"flag"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

type Action string

const (
	// Create action
	Create Action = "create"
	// Delete action
	Delete Action = "delete"
	// action includes: patch service selector
	Update Action = "patch"
	// action includes: remove service selector/restore gateway
	Restore Action = "unpatch"
)

// Define variables for the default expectation timeout and DefaultGraceExpectations instance.
var (
	ExpectationGraceTimeout  time.Duration
	DefaultGraceExpectations = NewGraceExpectations()
)

func init() {
	flag.DurationVar(&ExpectationGraceTimeout, "grace-timeout", time.Minute*5, "The grace expectation timeout. Defaults 5min")
	DefaultGraceExpectations.StartCleaner(ExpectationGraceTimeout)
}

// NewGraceExpectations returns a GraceExpectations.
func NewGraceExpectations() *realGraceExpectations {
	return &realGraceExpectations{
		controllerCache: make(map[string]timeCache),
	}
}

type timeCache map[Action]*time.Time

type realGraceExpectations struct {
	sync.RWMutex
	controllerCache map[string]timeCache // key: parent key, workload namespace/name
}

func (r *realGraceExpectations) GetExpectations(controllerKey string) timeCache {
	r.RLock()
	defer r.RUnlock()

	expectations := r.controllerCache[controllerKey]
	if expectations == nil {
		return nil
	}
	res := make(timeCache, len(expectations))
	for k, v := range expectations {
		res[k] = v
	}
	return res
}

func (r *realGraceExpectations) Expect(controllerKey string, action Action) {
	r.Lock()
	defer r.Unlock()

	expectations := r.controllerCache[controllerKey]
	if expectations == nil {
		expectations = make(timeCache)
		r.controllerCache[controllerKey] = expectations
	}
	recordTime := time.Now()
	expectations[action] = &recordTime
}

func (r *realGraceExpectations) Observe(controllerKey string, action Action) {
	r.Lock()
	defer r.Unlock()

	expectations := r.controllerCache[controllerKey]
	if expectations == nil {
		return
	}
	delete(expectations, action)
	if len(expectations) == 0 {
		delete(r.controllerCache, controllerKey)
	}
}

func (r *realGraceExpectations) SatisfiedExpectations(controllerKey string, action Action, graceSeconds int32) (bool, time.Duration) {
	r.RLock()
	defer r.RUnlock()

	expectations := r.controllerCache[controllerKey]
	if expectations == nil {
		return true, 0
	}
	recordTime, ok := expectations[action]
	if !ok {
		return true, 0
	}
	remaining := time.Duration(graceSeconds)*time.Second - time.Since(*recordTime)
	if remaining <= 0 {
		return true, 0
	}
	return false, remaining
}

func (r *realGraceExpectations) DeleteExpectations(controllerKey string) {
	r.Lock()
	defer r.Unlock()
	delete(r.controllerCache, controllerKey)
}

// cleaning outdated items
func (r *realGraceExpectations) CleanOutdatedItems(interval time.Duration) {
	r.Lock()
	defer r.Unlock()

	for controllerKey, expectations := range r.controllerCache {
		for action, recordTime := range expectations {
			if time.Since(*recordTime) > interval {
				delete(expectations, action)
			}
		}
		if len(expectations) == 0 {
			klog.Infof("clean outdated item: %s", controllerKey)
			delete(r.controllerCache, controllerKey)
		}
	}
}

// Start a goroutine to clean outdated items every 5 minutes
func (r *realGraceExpectations) StartCleaner(interval time.Duration) {
	klog.Infof("start grace expectations cleaner, interval: %v", interval)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			klog.Info("---clean outdated items---")
			r.CleanOutdatedItems(interval)
		}
	}()
}

// warning: only used for test
func (r *realGraceExpectations) resetExpectations() {
	r.Lock()
	defer r.Unlock()

	for controllerKey := range r.controllerCache {
		delete(r.controllerCache, controllerKey)
	}
}
