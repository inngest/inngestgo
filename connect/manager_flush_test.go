package connect

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestManagerFlushNotificationFlushesWithCurrentAuth(t *testing.T) {
	r := require.New(t)

	flushSeen := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.Equal("/v0/connect/flush", req.URL.Path)
		r.Equal("Bearer signing-key", req.Header.Get("Authorization"))

		body, err := io.ReadAll(req.Body)
		r.NoError(err)

		var resp connectproto.SDKResponse
		r.NoError(proto.Unmarshal(body, &resp))
		r.Equal("request-id", resp.RequestId)

		payload, err := proto.Marshal(&connectproto.FlushResponse{})
		r.NoError(err)
		w.Header().Set("Content-Type", "application/protobuf")
		_, _ = w.Write(payload)
		close(flushSeen)
	}))
	defer server.Close()

	logger := slog.New(slog.DiscardHandler)
	apiClient := newWorkerApiClient(server.URL, nil)
	h := &connectHandler{
		opts:                   Opts{IsDev: true, HashedSigningKey: []byte("signing-key")},
		logger:                 logger,
		notifyConnectDoneChan:  make(chan connectReport),
		notifyConnectedChan:    make(chan struct{}),
		initiateConnectionChan: make(chan struct{}, 1),
		notifyFlushChan:        make(chan struct{}, 1),
		apiClient:              apiClient,
		messageBuffer:          newMessageBuffer(apiClient, logger),
		state:                  ConnectionStateConnecting,
	}
	h.startConnection = func(context.Context, connectionEstablishData, ...connectOpt) {
		h.notifyConnectedChan <- struct{}{}
	}
	h.workerCtx, h.cancelWorkerCtx = context.WithCancel(context.Background())
	defer h.cancelWorkerCtx()

	connectCtx, cancelConnect := context.WithCancel(context.Background())
	defer cancelConnect()

	_, err := h.Connect(connectCtx)
	r.NoError(err)

	h.messageBuffer.append(&connectproto.SDKResponse{
		RequestId: "request-id",
	})
	// The lifecycle only notifies; the manager owns the API flush and applies
	// the current auth context.
	h.notifyFlushChan <- struct{}{}

	select {
	case <-flushSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle flush")
	}

	r.False(h.messageBuffer.hasMessages())
	cancelConnect()
	r.NoError(h.Close())
}

func TestManagerFlushNotificationsAreSerialized(t *testing.T) {
	r := require.New(t)

	firstFlushStarted := make(chan struct{})
	releaseFlush := make(chan struct{})
	var activeFlushes atomic.Int32
	var maxActiveFlushes atomic.Int32
	var flushCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// The manager run loop should serialize flushes, so this counter must
		// never observe two active /flush requests at once.
		current := activeFlushes.Add(1)
		defer activeFlushes.Add(-1)

		for {
			max := maxActiveFlushes.Load()
			if current <= max || maxActiveFlushes.CompareAndSwap(max, current) {
				break
			}
		}

		if flushCount.Add(1) == 1 {
			close(firstFlushStarted)
			<-releaseFlush
		}

		payload, err := proto.Marshal(&connectproto.FlushResponse{})
		r.NoError(err)
		w.Header().Set("Content-Type", "application/protobuf")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	logger := slog.New(slog.DiscardHandler)
	apiClient := newWorkerApiClient(server.URL, nil)
	h := &connectHandler{
		opts:                   Opts{IsDev: true, HashedSigningKey: []byte("signing-key")},
		logger:                 logger,
		notifyConnectDoneChan:  make(chan connectReport),
		notifyConnectedChan:    make(chan struct{}),
		initiateConnectionChan: make(chan struct{}, 1),
		notifyFlushChan:        make(chan struct{}, 1),
		apiClient:              apiClient,
		messageBuffer:          newMessageBuffer(apiClient, logger),
		state:                  ConnectionStateConnecting,
	}
	h.startConnection = func(context.Context, connectionEstablishData, ...connectOpt) {
		h.notifyConnectedChan <- struct{}{}
	}
	h.workerCtx, h.cancelWorkerCtx = context.WithCancel(context.Background())
	defer h.cancelWorkerCtx()

	connectCtx, cancelConnect := context.WithCancel(context.Background())
	defer cancelConnect()

	_, err := h.Connect(connectCtx)
	r.NoError(err)

	h.messageBuffer.append(&connectproto.SDKResponse{RequestId: "request-1"})
	h.messageBuffer.append(&connectproto.SDKResponse{RequestId: "request-2"})

	// Send one notification while the first flush is blocked, then attempt a
	// second notification. The channel may coalesce it, but it must not start a
	// concurrent flush.
	h.notifyFlushChan <- struct{}{}
	select {
	case <-firstFlushStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first lifecycle flush")
	}

	select {
	case h.notifyFlushChan <- struct{}{}:
	default:
	}
	close(releaseFlush)

	r.Eventually(func() bool {
		return !h.messageBuffer.hasMessages()
	}, time.Second, time.Millisecond)

	r.Equal(int32(1), maxActiveFlushes.Load())
	cancelConnect()
	r.NoError(h.Close())
}

func TestLateRetiredReplySchedulesManagerFlush(t *testing.T) {
	r := require.New(t)

	flushSeen := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.Equal("/v0/connect/flush", req.URL.Path)

		body, err := io.ReadAll(req.Body)
		r.NoError(err)

		var resp connectproto.SDKResponse
		r.NoError(proto.Unmarshal(body, &resp))
		r.Equal("late-request-id", resp.RequestId)

		payload, err := proto.Marshal(&connectproto.FlushResponse{})
		r.NoError(err)
		w.Header().Set("Content-Type", "application/protobuf")
		_, _ = w.Write(payload)
		close(flushSeen)
	}))
	defer server.Close()

	logger := slog.New(slog.DiscardHandler)
	apiClient := newWorkerApiClient(server.URL, nil)
	h := &connectHandler{
		opts:                   Opts{IsDev: true, HashedSigningKey: []byte("signing-key")},
		logger:                 logger,
		notifyConnectDoneChan:  make(chan connectReport),
		notifyConnectedChan:    make(chan struct{}),
		initiateConnectionChan: make(chan struct{}, 1),
		notifyFlushChan:        make(chan struct{}, 1),
		apiClient:              apiClient,
		messageBuffer:          newMessageBuffer(apiClient, logger),
		state:                  ConnectionStateConnecting,
	}
	h.startConnection = func(context.Context, connectionEstablishData, ...connectOpt) {
		h.notifyConnectedChan <- struct{}{}
	}
	h.workerCtx, h.cancelWorkerCtx = context.WithCancel(context.Background())
	defer h.cancelWorkerCtx()

	connectCtx, cancelConnect := context.WithCancel(context.Background())
	defer cancelConnect()

	_, err := h.Connect(connectCtx)
	r.NoError(err)

	// The retire notification can be consumed before ACKed work finishes.
	// A later buffered reply must produce its own manager notification.
	h.notifyFlushChan <- struct{}{}
	r.Eventually(func() bool {
		return len(h.notifyFlushChan) == 0
	}, time.Second, time.Millisecond)

	conn := newLifecycleTestConnection(nil)
	r.NoError(conn.transition(connPhaseHandshaking, "test"))
	r.NoError(conn.markActive("test"))
	conn.retire("replacement connected")

	err = h.writeReply(context.Background(), conn, &connectproto.SDKResponse{
		RequestId: "late-request-id",
	}, &connectproto.ConnectMessage{})
	r.NoError(err)

	select {
	case <-flushSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for late buffered response flush")
	}

	r.False(h.messageBuffer.hasMessages())
	cancelConnect()
	r.NoError(h.Close())
}
