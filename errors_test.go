package inngestgo

import (
	"fmt"
	"testing"

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
