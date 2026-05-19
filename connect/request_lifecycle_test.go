package connect

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/stretchr/testify/require"
)

func TestWriteRequestAckSkipsWhenPhaseDisallowsAck(t *testing.T) {
	r := require.New(t)

	h := &connectHandler{}
	conn := newLifecycleTestConnection(nil)
	r.NoError(conn.transition(connPhaseHandshaking, "test"))
	r.NoError(conn.markActive("test"))
	r.NoError(conn.beginDrain("replacement connected"))

	err := h.writeRequestAck(context.Background(), conn, []byte("{}"), slog.New(slog.DiscardHandler))

	// Draining generations may finish already-ACKed work, but they must not
	// take ownership of new requests by sending a fresh ACK.
	r.ErrorIs(err, errConnectionRetired)
	r.Equal(connPhaseDraining, conn.phase())
}

func TestWriteRequestAckCanceledContextRetiresBeforeInvoke(t *testing.T) {
	r := require.New(t)
	var logOutput bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	h := &connectHandler{}
	conn := newLifecycleTestConnection(nil)
	r.NoError(conn.transition(connPhaseHandshaking, "test"))
	r.NoError(conn.markActive("test"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := h.writeRequestAck(ctx, conn, []byte("{}"), logger)

	// A hard ACK write failure means the request never became this worker's
	// responsibility. Retiring the generation also suppresses later stale
	// writes from the same websocket.
	r.Error(err)
	r.Contains(err.Error(), "could not write message to websocket")
	r.Equal(connPhaseRetired, conn.phase())
	r.Contains(logOutput.String(), "could not write message to websocket")
}

func TestWriteReplyBuffersWhenPhaseDisallowsReply(t *testing.T) {
	r := require.New(t)

	logger := slog.New(slog.DiscardHandler)
	h := &connectHandler{
		logger:        logger,
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
	}
	conn := newLifecycleTestConnection(nil)
	r.NoError(conn.transition(connPhaseHandshaking, "test"))
	r.NoError(conn.markActive("test"))
	conn.retire("replacement connected")

	resp := &connectproto.SDKResponse{RequestId: "request-id"}
	err := h.writeReply(context.Background(), conn, resp, &connectproto.ConnectMessage{})

	// Replies happen after request ownership was established. If this
	// generation can no longer write, keep the result for API flush instead of
	// dropping completed work.
	r.NoError(err)
	h.messageBuffer.lock.Lock()
	defer h.messageBuffer.lock.Unlock()
	r.Contains(h.messageBuffer.buffered, "request-id")
}

func TestCanExtendRequestLeaseByPhase(t *testing.T) {
	r := require.New(t)

	conn := newLifecycleTestConnection(nil)
	r.NoError(conn.transition(connPhaseHandshaking, "test"))
	r.NoError(conn.markActive("test"))
	r.True(canExtendRequestLease(conn))

	conn.retire("replacement connected")

	// Lease extension is only useful while this generation is still allowed to
	// speak for in-flight work.
	r.False(canExtendRequestLease(conn))
}
