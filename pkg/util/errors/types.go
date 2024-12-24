package errors

import (
	"errors"
	"fmt"
)

// RetryError represents a benign error that can be handled or ignored by the caller.
// It encapsulates information that is non-critical and does not require immediate attention.
type RetryError struct {
	Err error
}

// Error implements the error interface for RetryError.
// It returns the error message of the encapsulated error or a default message.
func (e *RetryError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[retry]: %s", e.Err.Error())
	}
	return "retry error"
}

// NewRetryError creates a new instance of RetryError.
func NewRetryError(err error) *RetryError {
	return &RetryError{Err: err}
}

func IsRetryError(err error) bool {
	var re *RetryError
	return errors.As(err, &re)
}

func AsRetryError(err error, target **RetryError) bool {
	return errors.As(err, target)
}

// BadRequestError represents a fatal error that requires special handling.
// Such errors are critical and may necessitate logging, alerts, or even program termination.
type BadRequestError struct {
	Err error
}

// Error implements the error interface for BadRequestError.
// It returns the error message of the encapsulated error or a default message.
func (e *BadRequestError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "fatal error"
}

// NewBadRequestError creates a new instance of BadRequestError.
// It encapsulates the provided error, marking it as critical.
func NewBadRequestError(err error) *BadRequestError {
	return &BadRequestError{Err: err}
}

// IsBadRequest checks whether the provided error is of type BadRequestError.
// It returns true if the error is a BadRequestError or wraps a BadRequestError, false otherwise.
func IsBadRequest(err error) bool {
	var brErr *BadRequestError
	return AsBadRequest(err, &brErr)
}

// AsBadRequest attempts to cast the provided error to a BadRequestError.
// It returns true if the casting is successful, allowing the caller to handle it accordingly.
func AsBadRequest(err error, target **BadRequestError) bool {
	return errors.As(err, target)
}
