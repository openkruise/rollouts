package grace

import (
	"errors"
	"testing"
	"time"
)

func TestRunWithGraceSeconds(t *testing.T) {
	tests := []struct {
		name          string
		graceSeconds  int32
		modified      bool
		err           error
		expectedRetry bool
		expectedErr   error
	}{
		{name: "No modification, no grace period", graceSeconds: 0, modified: false, err: nil, expectedRetry: false, expectedErr: nil},
		{name: "Modification, with grace period", graceSeconds: 10, modified: true, err: nil, expectedRetry: true, expectedErr: nil},
		{name: "No modification, expectation unsatisfied", graceSeconds: 10, modified: false, err: nil, expectedRetry: true, expectedErr: nil},
		{name: "Function returns error", graceSeconds: 10, modified: false, err: errors.New("test error"), expectedRetry: true, expectedErr: errors.New("test error")},
	}

	graceExpectations := NewGraceExpectations()

	for _, cs := range tests {
		t.Run(cs.name, func(t *testing.T) {
			f := func() (bool, error) {
				return cs.modified, cs.err
			}

			retry, _, err := runWithGraceSeconds(graceExpectations, "testKey", "create", cs.graceSeconds, f)
			if retry != cs.expectedRetry {
				t.Errorf("expected retry: %v, got: %v", cs.expectedRetry, retry)
			}

			if !equalErr(err, cs.expectedErr) {
				t.Errorf("expected error: %v, got: %v", cs.expectedErr, err)
			}
		})
	}

	// Additional test to verify the timeout behavior
	t.Run("Satisfaction of expectation over time", func(t *testing.T) {
		graceSeconds := int32(1) // 1 second grace period
		graceExpectations := NewGraceExpectations()

		f := func() (bool, error) {
			return true, nil
		}

		retry, _, err := runWithGraceSeconds(graceExpectations, "testKey2", "delete", graceSeconds, f)
		if retry != true {
			t.Errorf("expected retry: true after modification, got: %v", retry)
		}
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}

		// Wait for the grace period to be satisfied
		time.Sleep(time.Duration(graceSeconds) * time.Second)

		f2 := func() (bool, error) {
			return false, nil
		}
		retry, _, err = runWithGraceSeconds(graceExpectations, "testKey2", "delete", graceSeconds, f2)
		if retry != false {
			t.Errorf("expected retry: false after grace period satisfied, got: %v", retry)
		}
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})
}

// Comprehensive test for RunWithGraceSeconds using testFunction
func TestRunWithGraceSecondsComprehensive(t *testing.T) {
	graceExpectations := NewGraceExpectations()

	tests := []struct {
		name          string
		expectedState int
		graceSeconds  int32
		expectedRetry bool
		expectedErr   error
	}{
		{"No modification needed", 0, 0, false, nil},
		{"Initial modification, create expectation", 1, 10, true, nil},
		{"Function errors", -1, 10, true, errors.New("test error")},
	}

	for _, cs := range tests {
		t.Run(cs.name, func(t *testing.T) {
			f := testFunction(cs.expectedState)

			retry, _, err := runWithGraceSeconds(graceExpectations, cs.name, "create", cs.graceSeconds, f)
			if !equalErr(err, cs.expectedErr) {
				t.Errorf("%s: expected error: %v, but got none", cs.name, cs.expectedErr)
			}

			if retry != cs.expectedRetry {
				t.Errorf("%s: expected retry: %v, got: %v", cs.name, cs.expectedRetry, retry)
			}
		})
	}
}

// Test function generator
func testFunction(expectedState int) func() (bool, error) {
	currentState := 0
	return func() (bool, error) {
		if expectedState < 0 {
			return true, errors.New("test error")
		}
		if currentState == expectedState {
			return false, nil
		} else {
			currentState++
			return true, nil
		}
	}
}

func equalErr(err1, err2 error) bool {
	if err1 == nil && err2 == nil {
		return true
	}
	if err1 != nil && err2 != nil {
		return true
	}
	return false
}
