package grace

import (
	"testing"
	"time"
)

func TestGetExpectations(t *testing.T) {
	r := NewGraceExpectations()

	now := time.Now()
	r.controllerCache["testKey"] = timeCache{
		Create: &now,
	}

	result := r.GetExpectations("testKey")
	if result == nil || len(result) != 1 || result[Create] == nil {
		t.Errorf("expected timeCache with one Create action, got %v", result)
	}
}

func TestExpect(t *testing.T) {
	r := NewGraceExpectations()
	r.Expect("testKey", Create)

	if _, exists := r.controllerCache["testKey"][Create]; !exists {
		t.Errorf("expected Create action for testKey to be recorded")
	}
}

func TestObserve(t *testing.T) {
	r := NewGraceExpectations()

	r.Expect("testKey", Create)
	r.Observe("testKey", Create)

	if _, exists := r.controllerCache["testKey"]; exists {
		t.Errorf("expected testKey to be removed from cache after Observe")
	}
}

func TestSatisfiedExpectations(t *testing.T) {
	r := NewGraceExpectations()
	r.Expect("testKey", Create)

	// Should be unsatisfied if graceSeconds is 0 (immediate timeout)
	satisfied, _ := r.SatisfiedExpectations("testKey", Create, 0)
	if !satisfied {
		t.Errorf("expected expectations to be satisfied immediately for 0 graceSeconds")
	}

	// Set a new expectation with some future grace period
	r.Expect("testKey", Create)
	satisfied, _ = r.SatisfiedExpectations("testKey", Create, 60)
	if satisfied {
		t.Errorf("expected expectations to be unsatisfied for 60 second grace period")
	}
}

func TestDeleteExpectations(t *testing.T) {
	r := NewGraceExpectations()
	r.Expect("testKey", Create)

	r.DeleteExpectations("testKey")

	if _, exists := r.controllerCache["testKey"]; exists {
		t.Errorf("expected testKey to be deleted from cache after DeleteExpectations")
	}
}

func TestCleanOutdatedItems(t *testing.T) {
	r := NewGraceExpectations()

	// Set an expectation well in the past so it is outdated
	past := time.Now().Add(-time.Hour)
	r.controllerCache["testKey"] = timeCache{
		Create: &past,
	}

	r.CleanOutdatedItems(ExpectationGraceTimeout)

	if _, exists := r.controllerCache["testKey"]; exists {
		t.Errorf("expected testKey to be removed by CleanOutdatedItems")
	}

	// Set a recent expectation to ensure it is not cleaned out
	r.Expect("testKey", Create)
	recent := time.Now()
	r.controllerCache["testKey"][Create] = &recent

	r.CleanOutdatedItems(ExpectationGraceTimeout)

	if _, exists := r.controllerCache["testKey"]; !exists {
		t.Errorf("expected testKey to not be removed by CleanOutdatedItems")
	}
}

func TestResetExpectations(t *testing.T) {
	r := NewGraceExpectations()
	r.Expect("testKey", Create)

	r.resetExpectations()

	if _, exists := r.controllerCache["testKey"]; exists {
		t.Errorf("expected controller cache to be empty after resetExpectations")
	}
}

func TestComprehensive(t *testing.T) {
	// Initialize realGraceExpectations
	r := NewGraceExpectations()

	// Add expectations
	r.Expect("testController1", Create)
	r.Expect("testController1", Delete)
	r.Expect("testController2", Update)

	// Validate that expectations are correctly added
	if expectations := r.GetExpectations("testController1"); len(expectations) != 2 {
		t.Errorf("expected 2 actions for testController1, got %d", len(expectations))
	}
	if expectations := r.GetExpectations("testController2"); len(expectations) != 1 {
		t.Errorf("expected 1 action for testController2, got %d", len(expectations))
	}

	// Observe and remove a specific action
	r.Observe("testController1", Create)
	if expectations := r.GetExpectations("testController1"); len(expectations) != 1 {
		t.Errorf("expected 1 action for testController1 after observation, got %d", len(expectations))
	}

	// Check satisfaction status for an existing expectation
	satisfied, remaining := r.SatisfiedExpectations("testController1", Delete, 0)
	if !satisfied || remaining > 0 {
		t.Errorf("expected unsatisfied expectation for testController1 Delete action")
	}

	// Check satisfaction status for a non-existing action
	satisfied, _ = r.SatisfiedExpectations("testController1", Create, 0)
	if !satisfied {
		t.Errorf("expected satisfied status for non-existing Create action on testController1")
	}

	// Advance the time and check satisfaction after timeout
	r.Expect("testController3", Restore)
	time.Sleep(2 * time.Second)
	satisfied, _ = r.SatisfiedExpectations("testController3", Restore, 1) // 1 second grace period
	if !satisfied {
		t.Errorf("expected satisfied status for Restore action on testController3 after timeout")
	}

	// Delete expectations for a controller key
	r.DeleteExpectations("testController1")
	if expectations := r.GetExpectations("testController1"); len(expectations) != 0 {
		t.Errorf("expected no actions for testController1 after deletion")
	}

	// Clean outdated items
	r.Expect("testController4", Update)
	outdated := time.Now().Add(-10 * time.Minute) // Set a past record time to be outdated
	r.controllerCache["testController4"][Update] = &outdated
	r.CleanOutdatedItems(ExpectationGraceTimeout)
	if expectations := r.GetExpectations("testController4"); len(expectations) != 0 {
		t.Errorf("expected no actions for testController4 after clean")
	}

	// Reset expectations
	r.Expect("testController5", Delete)
	r.resetExpectations()
	if len(r.controllerCache) != 0 {
		t.Errorf("expected empty controller cache after reset")
	}
}

func TestStartCleaner(t *testing.T) {
	// Shorten the interval for faster testing
	testInterval := time.Millisecond * 100

	// Initialize realGraceExpectations
	r := NewGraceExpectations()

	// Add mixed expectations
	past := time.Now().Add(-10 * time.Minute)
	recent := time.Now().Add(testInterval * 3)

	r.controllerCache["testController1"] = timeCache{
		Create: &past,
		Update: &recent,
	}
	r.controllerCache["testController2"] = timeCache{
		Delete:  &past,
		Restore: &recent,
	}

	// Start the cleaner with a short interval
	r.StartCleaner(testInterval)

	// Wait for the cleaner to run
	time.Sleep(testInterval * 3)

	// Verify the outdated items have been cleaned and recent ones remain
	if len(r.controllerCache["testController1"]) != 1 || r.controllerCache["testController1"][Update] != &recent {
		t.Errorf("expected only recent Update action for testController1 to remain, found %d", len(r.controllerCache["testController1"]))
	}

	if len(r.controllerCache["testController2"]) != 1 || r.controllerCache["testController2"][Restore] != &recent {
		t.Errorf("expected only recent Restore action for testController2 to remain, found %d", len(r.controllerCache["testController2"]))
	}
}
