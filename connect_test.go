package inngestgo

import (
	"context"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestConnectEstablish(t *testing.T) {
	os.Setenv("INNGEST_DEV", "1")

	ctx := context.Background()
	err := Connect(ctx)
	require.NoError(t, err)
}
