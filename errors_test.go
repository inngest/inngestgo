package inngestgo

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsNoRetryError(t *testing.T) {
	err := fmt.Errorf("error")
	require.False(t, isNoRetryError(err))

	wrapped := NoRetryError(err)
	require.True(t, isNoRetryError(wrapped))

	cause := fmt.Errorf("error: %w", wrapped)
	require.True(t, isNoRetryError(cause))
}

func TestGetRetryAtTime(t *testing.T) {
	expected := time.Now().Add(time.Hour)

	err := fmt.Errorf("some err")
	at := RetryAtError(err, expected)

	t.Run("It returns the time with a RetryAtError", func(t *testing.T) {
		require.NotNil(t, getRetryAtTime(at))
		require.EqualValues(t, expected, *getRetryAtTime(at))
	})

	t.Run("It returns if RetryAtError is wrapped itself", func(t *testing.T) {

		wrapped := fmt.Errorf("wrap: %w", at)

		require.NotNil(t, getRetryAtTime(wrapped))
		require.EqualValues(t, expected, *getRetryAtTime(wrapped))
	})
}
