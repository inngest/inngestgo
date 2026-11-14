package inngestgo

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/inngest/inngestgo/pkg/checkpoint"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/require"
)

// TestServeBufferedStepFailedIsNotRetried covers a permanent step failure
// that is reported alongside a buffered successful step.
//
// A checkpointed function keeps successful steps buffered until the checkpoint
// is flushed, so the op list can contain an OpcodeStepRun followed by an
// OpcodeStepFailed. The handler only recognised the failure when it was the
// only opcode, so in this case the error was reported as a generic failure
// and the run was retried instead of being recorded as failed.
//
// This is the same gap that was fixed for the connect transport in
// https://github.com/inngest/inngestgo/pull/278.
func TestServeBufferedStepFailedIsNotRetried(t *testing.T) {
	setEnvVars(t)

	c, err := NewClient(ClientOpts{AppID: "buffered-step-failed"})
	require.NoError(t, err)

	f, err := CreateFunction(
		c,
		FunctionOpts{
			ID:         "buffered-step-failed",
			Checkpoint: &checkpoint.Config{BatchSteps: 100},
		},
		EventTrigger("test/buffered.step.failed", nil),
		func(ctx context.Context, input Input[map[string]any]) (any, error) {
			// This step succeeds and stays buffered.
			_, err := step.Run(ctx, "reserve", func(context.Context) (string, error) {
				return "reserved", nil
			})
			if err != nil {
				return nil, err
			}
			// This step fails permanently.
			_, err = step.Run(ctx, "charge", func(context.Context) (bool, error) {
				return false, errors.New("charge declined")
			})
			return nil, err
		},
	)
	require.NoError(t, err)

	server := httptest.NewServer(c.Serve())
	defer server.Close()

	queryParams := url.Values{}
	queryParams.Add("fnId", f.FullyQualifiedID())

	// The step must have exhausted its retries for the SDK to report a
	// permanent failure rather than a retryable one.
	maxAttempts := 1
	req := createRequest(t, map[string]any{
		"name": "test/buffered.step.failed",
		"data": map[string]any{},
	})
	req.CallCtx.Attempt = 0
	req.CallCtx.MaxAttempts = &maxAttempts

	resp := handlerPost(t, server.URL+"?"+queryParams.Encode(), req)
	defer func() {
		_ = resp.Body.Close()
	}()

	// A permanent step failure is not retryable, so the handler must not
	// ask the server to retry it.
	require.Equal(t, "true", resp.Header.Get(HeaderKeyNoRetry))
}
