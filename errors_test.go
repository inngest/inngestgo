package inngestgo

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsNoRetryError(t *testing.T) {
	err := fmt.Errorf("error")
	require.False(t, IsNoRetryError(err))

	wrapped := NoRetryError(err)
	require.True(t, IsNoRetryError(wrapped))

	cause := fmt.Errorf("error: %w", wrapped)
	require.True(t, IsNoRetryError(cause))
}

func TestGetRetryAtTime(t *testing.T) {
	expected := time.Now().Add(time.Hour)

	err := fmt.Errorf("some err")
	at := RetryAtError(err, expected)

	t.Run("It returns the time with a RetryAtError", func(t *testing.T) {
		require.NotNil(t, GetRetryAtTime(at))
		require.EqualValues(t, expected, *GetRetryAtTime(at))
	})

	t.Run("It returns if RetryAtError is wrapped itself", func(t *testing.T) {

		wrapped := fmt.Errorf("wrap: %w", at)

		require.NotNil(t, GetRetryAtTime(wrapped))
		require.EqualValues(t, expected, *GetRetryAtTime(wrapped))
	})
}
