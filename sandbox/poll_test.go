package sandbox

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPollTimeoutBoundsHTTP(t *testing.T) {
	previous := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = previous })

	tests := []struct {
		name     string
		poll     func(*Client) error
		wantCode ErrorCode
	}{
		{name: "running", wantCode: CodeStartTimedOut, poll: func(c *Client) error {
			_, err := c.waitUntilRunning(context.Background(), SandboxInfo{ID: "sandbox-id", Status: StatusStarting}, 25*time.Millisecond, ActionCreate)
			return err
		}},
		{name: "pause", wantCode: CodeStartTimedOut, poll: func(c *Client) error {
			_, err := c.pollStatus(context.Background(), SandboxInfo{ID: "sandbox-id", Status: StatusPausing}, StatusPaused, ActionPause, 25*time.Millisecond)
			return err
		}},
		{name: "snapshot", wantCode: CodeSnapshotWaitTimedOut, poll: func(c *Client) error {
			_, err := c.waitUntilSnapshotReady(context.Background(), SnapshotInfo{ID: "snapshot-id", Status: SnapshotCreating}, 25*time.Millisecond)
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := &Client{t: &transport{
				baseURL: "http://sandbox.test", dev: true,
				http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					<-req.Context().Done()
					return nil, req.Context().Err()
				})},
			}}
			start := time.Now()
			err := tt.poll(client)
			var sandboxErr *Error
			require.ErrorAs(t, err, &sandboxErr)
			assert.Equal(t, tt.wantCode, sandboxErr.Code)
			assert.Equal(t, 1, calls)
			assert.Less(t, time.Since(start), 250*time.Millisecond)
		})
	}
}

func TestPollPreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &Client{t: &transport{}}
	_, err := client.waitUntilRunning(ctx, SandboxInfo{ID: "sandbox-id", Status: StatusStarting}, time.Second, ActionCreate)
	require.True(t, errors.Is(err, context.Canceled))
}
