package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublishWithURL_EnvironmentHeader(t *testing.T) {
	var receivedEnvHeader string
	var receivedSDKHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEnvHeader = r.Header.Get("X-Inngest-Env")
		receivedSDKHeader = r.Header.Get("X-Inngest-SDK")
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
	assert.Equal(t, "go:v"+version.SDKVersion, receivedSDKHeader)
}

func TestPublishWithURL_HonorsContext(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	mgr := sdkrequest.NewManager(sdkrequest.Opts{
		SigningKey: "test-key",
		Request:    &sdkrequest.Request{},
	})
	defer mgr.CloseCheckpointer()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ctx = sdkrequest.SetManager(ctx, mgr)

	done := make(chan error, 1)
	go func() {
		done <- PublishWithURL(ctx, server.URL, "channel", "topic", []byte(`{"ok":true}`))
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("PublishWithURL did not return after the context deadline")
	}
}
