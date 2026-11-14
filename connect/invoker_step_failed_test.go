package connect

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/inngest/inngest/pkg/enums"
	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/require"
)

// stepFailedInvoker returns a single OpcodeStepFailed opcode alongside an
// error, which is what step.Run produces once a step has exhausted its
// retries.
type stepFailedInvoker struct {
	called bool
}

func (s *stepFailedInvoker) InvokeFunction(
	_ context.Context,
	_ string,
	_ *string,
	_ sdkrequest.Request,
) (any, []sdkrequest.GeneratorOpcode, error) {
	s.called = true
	return nil, []sdkrequest.GeneratorOpcode{
		{
			ID:   "step-hash",
			Op:   enums.OpcodeStepFailed,
			Name: "charge",
		},
	}, errors.New("step failed")
}

// bufferedStepFailedInvoker returns a buffered successful step alongside the
// permanent failure. This is what a checkpointed function produces when an
// earlier step succeeded and a later step failed permanently: the successful
// step is still buffered in the op list when the failure is reported.
type bufferedStepFailedInvoker struct {
	called bool
}

func (s *bufferedStepFailedInvoker) InvokeFunction(
	_ context.Context,
	_ string,
	_ *string,
	_ sdkrequest.Request,
) (any, []sdkrequest.GeneratorOpcode, error) {
	s.called = true
	return nil, []sdkrequest.GeneratorOpcode{
		{
			ID:   "reserve-hash",
			Op:   enums.OpcodeStepRun,
			Name: "reserve",
		},
		{
			ID:   "charge-hash",
			Op:   enums.OpcodeStepFailed,
			Name: "charge",
		},
	}, errors.New("step failed")
}

// TestInvokerHandlerStepFailedIsNotRetried covers a step that exhausted its
// retries.
//
// The HTTP Serve transport handles OpcodeStepFailed by clearing the error and
// marking the response as non-retryable, so the server records the failure
// and the handler receives the cached step failure on the next invocation.
//
// The Connect transport only handled OpcodeStepError, which is the opcode
// used for a retryable step failure. An exhausted step was therefore
// reported as a generic error and retried, and the handler never received
// the cached step failure, so a compensation step after the failing step
// never ran.
//
// See https://github.com/inngest/inngestgo/issues/267.
func TestInvokerHandlerStepFailedIsNotRetried(t *testing.T) {
	inv := &stepFailedInvoker{}
	h := newTestInvokerHandler(inv, Opts{})

	payload, err := json.Marshal(sdkrequest.Request{})
	require.NoError(t, err)

	resp, err := h.Serve(context.Background(), &connectproto.GatewayExecutorRequestData{
		AppName:        "app",
		FunctionSlug:   "fn",
		RequestPayload: payload,
	})
	require.NoError(t, err)
	require.True(t, inv.called, "the function should have been invoked")

	// The exhausted step is reported as a completed step failure, not as a
	// generic error that the server would retry.
	require.Equal(t, connectproto.SDKResponseStatus_NOT_COMPLETED, resp.Status)
	require.True(t, resp.NoRetry, "an exhausted step must not be retried")
}

// TestInvokerHandlerBufferedStepFailedIsNotRetried covers the same permanent
// failure when an earlier step is still buffered in the op list.
//
// Checkpointed functions keep successful steps buffered until the checkpoint
// is flushed, so a permanent failure can be reported alongside an
// OpcodeStepRun. The failure must still be recognised when it is not the
// only opcode.
func TestInvokerHandlerBufferedStepFailedIsNotRetried(t *testing.T) {
	inv := &bufferedStepFailedInvoker{}
	h := newTestInvokerHandler(inv, Opts{})

	payload, err := json.Marshal(sdkrequest.Request{})
	require.NoError(t, err)

	resp, err := h.Serve(context.Background(), &connectproto.GatewayExecutorRequestData{
		AppName:        "app",
		FunctionSlug:   "fn",
		RequestPayload: payload,
	})
	require.NoError(t, err)
	require.True(t, inv.called, "the function should have been invoked")

	require.Equal(t, connectproto.SDKResponseStatus_NOT_COMPLETED, resp.Status)
	require.True(t, resp.NoRetry, "an exhausted step must not be retried")
}
