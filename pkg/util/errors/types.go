package errors

import (
	"errors"
	"fmt"
)

// BenignError represents a benign error that can be handled or ignored by the caller.
// It encapsulates information that is non-critical and does not require immediate attention.
type BenignError struct {
	Err error
}

// Error implements the error interface for BenignError.
// It returns the error message of the encapsulated error or a default message.
func (e *BenignError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[benign]: %s", e.Err.Error())
	}
	return "benign error"
}

// NewBenignError creates a new instance of BenignError.
// If the provided err is nil, it signifies a benign condition without a specific error message.
func NewBenignError(err error) *BenignError {
	return &BenignError{Err: err}
}

func IsBenign(err error) bool {
	var benignErr *BenignError
	return errors.As(err, &benignErr)
}

func AsBenign(err error, target **BenignError) bool {
	return errors.As(err, target)
}

// FatalError represents a fatal error that requires special handling.
// Such errors are critical and may necessitate logging, alerts, or even program termination.
type FatalError struct {
	Err error
}

// Error implements the error interface for FatalError.
// It returns the error message of the encapsulated error or a default message.
func (e *FatalError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "fatal error"
}

// NewFatalError creates a new instance of FatalError.
// It encapsulates the provided error, marking it as critical.
func NewFatalError(err error) *FatalError {
	return &FatalError{Err: err}
}

// IsFatal checks whether the provided error is of type FatalError.
// It returns true if the error is a FatalError or wraps a FatalError, false otherwise.
func IsFatal(err error) bool {
	var fatalErr *FatalError
	return AsFatal(err, &fatalErr)
}

// AsFatal attempts to cast the provided error to a FatalError.
// It returns true if the casting is successful, allowing the caller to handle it accordingly.
func AsFatal(err error, target **FatalError) bool {
	return errors.As(err, target)
}
