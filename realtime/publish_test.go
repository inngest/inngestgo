package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishWithURL_EnvironmentHeader(t *testing.T) {
	var receivedEnvHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEnvHeader = r.Header.Get("X-Inngest-Env")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mgr := sdkrequest.NewManager(sdkrequest.Opts{
		SigningKey: "test-key",
		Request: &sdkrequest.Request{
			CallCtx: sdkrequest.CallCtx{Env: "preview"},
		},
	})
	defer mgr.CloseCheckpointer()
	ctx := sdkrequest.SetManager(context.Background(), mgr)

	err := PublishWithURL(ctx, server.URL, "channel", "topic", []byte(`{"ok":true}`))
	require.NoError(t, err)
	assert.Equal(t, "preview", receivedEnvHeader)
}
