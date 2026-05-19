package connect

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/require"
)

func TestHandleConnectionGracefulShutdownEntersClosingBeforeRetireAndClose(t *testing.T) {
	r := require.New(t)

	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		defer close(serverDone)
		defer func() { _ = conn.CloseNow() }()

		for {
			var msg connectproto.ConnectMessage
			if err := wsReadProto(context.Background(), conn, &msg); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	handleCtx, cancelHandle := context.WithCancel(context.Background())
	defer cancelHandle()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	r.NoError(err)
	defer func() { _ = clientConn.CloseNow() }()

	pauseLogSeen := make(chan struct{})
	logger := slog.New(newLogPatternHandler("sending worker pause message", pauseLogSeen))
	h := &connectHandler{
		logger:        logger,
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}
	h.workerPool.inProgress.Add(1)

	preparedConn := &connection{
		ws:                  clientConn,
		connectionId:        "old-connection",
		heartbeatInterval:   time.Hour,
		extendLeaseInterval: time.Hour,
	}
	activateTestConnection(t, preparedConn)

	done := make(chan error, 1)
	go func() {
		done <- h.handleConnection(handleCtx, connectionEstablishData{}, preparedConn)
	}()

	cancelHandle()

	waitForLogSignal(t, pauseLogSeen, "sending worker pause message")

	r.Equal(connPhaseClosing, preparedConn.phase())

	select {
	case err := <-done:
		t.Fatalf("handleConnection returned before worker pool drained: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	h.workerPool.Done()

	select {
	case err := <-done:
		r.NoError(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handleConnection")
	}

	r.Equal(connPhaseClosed, preparedConn.phase())
	r.True(preparedConn.isRetired())

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket close")
	}
}

func TestHandleInvokeMessageSkipsAckDuringClosing(t *testing.T) {
	r := require.New(t)
	var logOutput bytes.Buffer

	invoker := &blockingTestInvoker{
		release: make(chan struct{}),
	}
	logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	h := &connectHandler{
		opts: Opts{
			SDKLanguage: "go",
			SDKVersion:  "test",
		},
		invokers: map[string]FunctionInvoker{
			"test-app": invoker,
		},
		logger:        logger,
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
	}

	preparedConn := newLifecycleTestConnection(nil)
	r.NoError(preparedConn.transition(connPhaseHandshaking, "test"))
	r.NoError(preparedConn.markActive("test"))
	r.NoError(preparedConn.beginClose("test"))

	msg := mustExecutorRequestMessage(t, &connectproto.GatewayExecutorRequestData{
		RequestId:      "request-id",
		EnvId:          "env-id",
		AppId:          "app-id",
		AppName:        "test-app",
		FunctionSlug:   "test-fn",
		RequestPayload: mustJSON(t, sdkrequest.Request{}),
		LeaseId:        "lease-id",
	})

	err := h.handleInvokeMessage(context.Background(), preparedConn, msg)
	r.NoError(err)
	r.False(invoker.called.Load())
	r.Contains(logOutput.String(), "phase does not allow request ack")
	r.NotContains(logOutput.String(), "error sending request ack")
}

func TestHandleInvokeMessageAttemptsReplyDuringClosingForAckedWork(t *testing.T) {
	r := require.New(t)

	serverSawAck := make(chan struct{})
	serverSawReply := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		defer func() { _ = conn.CloseNow() }()

		var ack connectproto.ConnectMessage
		r.NoError(wsReadProto(req.Context(), conn, &ack))
		r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, ack.Kind)
		close(serverSawAck)

		var reply connectproto.ConnectMessage
		r.NoError(wsReadProto(req.Context(), conn, &reply))
		r.Equal(connectproto.GatewayMessageType_WORKER_REPLY, reply.Kind)
		close(serverSawReply)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(ctx, wsURL, nil)
	r.NoError(err)
	defer func() { _ = clientConn.CloseNow() }()

	release := make(chan struct{})
	invoker := &blockingTestInvoker{
		release: release,
	}
	logger := slog.New(slog.DiscardHandler)
	h := &connectHandler{
		opts: Opts{
			SDKLanguage: "go",
			SDKVersion:  "test",
		},
		invokers: map[string]FunctionInvoker{
			"test-app": invoker,
		},
		logger:        logger,
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}

	preparedConn := &connection{
		ws:                  clientConn,
		connectionId:        "old-connection",
		extendLeaseInterval: time.Hour,
	}
	activateTestConnection(t, preparedConn)

	msg := mustExecutorRequestMessage(t, &connectproto.GatewayExecutorRequestData{
		RequestId:      "request-id",
		EnvId:          "env-id",
		AppId:          "app-id",
		AppName:        "test-app",
		FunctionSlug:   "test-fn",
		RequestPayload: mustJSON(t, sdkrequest.Request{}),
		LeaseId:        "lease-id",
	})

	done := make(chan error, 1)
	go func() {
		done <- h.handleInvokeMessage(ctx, preparedConn, msg)
	}()

	select {
	case <-serverSawAck:
	case <-time.After(time.Second):
		t.Fatal("server did not receive worker request ack")
	}

	r.NoError(preparedConn.beginClose("test"))
	close(release)

	select {
	case err := <-done:
		r.NoError(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invoke")
	}

	select {
	case <-serverSawReply:
	case <-time.After(time.Second):
		t.Fatal("server did not receive worker reply during close")
	}

	h.messageBuffer.lock.Lock()
	defer h.messageBuffer.lock.Unlock()
	r.NotContains(h.messageBuffer.buffered, "request-id")
}

func TestHandleInvokeMessageBuffersClosingReplyWriteFailure(t *testing.T) {
	r := require.New(t)

	serverSawAck := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)

		var ack connectproto.ConnectMessage
		r.NoError(wsReadProto(req.Context(), conn, &ack))
		r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, ack.Kind)
		close(serverSawAck)

		_ = conn.Close(websocket.StatusInternalError, "connect_internal_error")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(ctx, wsURL, nil)
	r.NoError(err)
	defer func() { _ = clientConn.CloseNow() }()

	release := make(chan struct{})
	invoker := &blockingTestInvoker{
		release: release,
	}
	logger := slog.New(slog.DiscardHandler)
	h := &connectHandler{
		opts: Opts{
			SDKLanguage: "go",
			SDKVersion:  "test",
		},
		invokers: map[string]FunctionInvoker{
			"test-app": invoker,
		},
		logger:        logger,
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}

	preparedConn := &connection{
		ws:                  clientConn,
		connectionId:        "old-connection",
		extendLeaseInterval: time.Hour,
	}
	activateTestConnection(t, preparedConn)

	msg := mustExecutorRequestMessage(t, &connectproto.GatewayExecutorRequestData{
		RequestId:      "request-id",
		EnvId:          "env-id",
		AppId:          "app-id",
		AppName:        "test-app",
		FunctionSlug:   "test-fn",
		RequestPayload: mustJSON(t, sdkrequest.Request{}),
		LeaseId:        "lease-id",
	})

	done := make(chan error, 1)
	go func() {
		done <- h.handleInvokeMessage(ctx, preparedConn, msg)
	}()

	select {
	case <-serverSawAck:
	case <-time.After(time.Second):
		t.Fatal("server did not receive worker request ack")
	}

	r.NoError(preparedConn.beginClose("test"))
	_ = clientConn.CloseNow()
	close(release)

	select {
	case err := <-done:
		r.NoError(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invoke")
	}

	r.False(preparedConn.isRetired())

	h.messageBuffer.lock.Lock()
	defer h.messageBuffer.lock.Unlock()
	r.Contains(h.messageBuffer.buffered, "request-id")
}
