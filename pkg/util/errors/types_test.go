package errors

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRetryError(t *testing.T) {
	t.Run("NewRetryError with nil error", func(t *testing.T) {
		retryErr := NewRetryError(nil)
		assert.NotNil(t, retryErr)
		assert.Nil(t, retryErr.Err)
		assert.Equal(t, "retry error", retryErr.Error())
	})

	t.Run("NewRetryError with an underlying error", func(t *testing.T) {
		innerErr := errors.New("something went wrong")
		retryErr := NewRetryError(innerErr)
		assert.NotNil(t, retryErr)
		assert.Equal(t, innerErr, retryErr.Err)
		assert.Equal(t, "[retry]: something went wrong", retryErr.Error())
	})

	t.Run("IsRetryError checks", func(t *testing.T) {
		directRetryErr := NewRetryError(errors.New("direct"))
		wrappedRetryErr := fmt.Errorf("wrapped: %w", directRetryErr)
		otherErr := errors.New("not a retry error")

		assert.True(t, IsRetryError(directRetryErr))
		assert.True(t, IsRetryError(wrappedRetryErr))
		assert.False(t, IsRetryError(otherErr))
	})

	t.Run("AsRetryError checks", func(t *testing.T) {
		directRetryErr := NewRetryError(errors.New("direct"))
		wrappedRetryErr := fmt.Errorf("wrapped: %w", directRetryErr)

		var target *RetryError
		assert.True(t, AsRetryError(wrappedRetryErr, &target))
		assert.Equal(t, directRetryErr, target)
	})
}

func TestBadRequestError(t *testing.T) {
	t.Run("NewBadRequestError with nil error", func(t *testing.T) {
		badReqErr := NewBadRequestError(nil)
		assert.NotNil(t, badReqErr)
		assert.Nil(t, badReqErr.Err)
		assert.Equal(t, "fatal error", badReqErr.Error())
	})

	t.Run("NewBadRequestError with an underlying error", func(t *testing.T) {
		innerErr := errors.New("invalid input")
		badReqErr := NewBadRequestError(innerErr)
		assert.NotNil(t, badReqErr)
		assert.Equal(t, innerErr, badReqErr.Err)
		assert.Equal(t, "invalid input", badReqErr.Error())
	})

	t.Run("IsBadRequest checks", func(t *testing.T) {
		directBadReqErr := NewBadRequestError(errors.New("direct"))
		wrappedBadReqErr := fmt.Errorf("wrapped: %w", directBadReqErr)
		otherErr := errors.New("not a bad request error")

		assert.True(t, IsBadRequest(directBadReqErr))
		assert.True(t, IsBadRequest(wrappedBadReqErr))
		assert.False(t, IsBadRequest(otherErr))
	})

	t.Run("AsBadRequest checks", func(t *testing.T) {
		directBadReqErr := NewBadRequestError(errors.New("direct"))
		wrappedBadReqErr := fmt.Errorf("wrapped: %w", directBadReqErr)

		var target *BadRequestError
		assert.True(t, AsBadRequest(wrappedBadReqErr, &target))
		assert.Equal(t, directBadReqErr, target)
	})
}
