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
	"google.golang.org/protobuf/proto"
)

func TestConnectionWritePolicyByPhase(t *testing.T) {
	tests := []struct {
		name        string
		phase       connPhase
		heartbeat   bool
		requestAck  bool
		reply       bool
		extendLease bool
		pause       bool
	}{
		{name: "new", phase: connPhaseNew},
		{name: "handshaking", phase: connPhaseHandshaking},
		{name: "active", phase: connPhaseActive, heartbeat: true, requestAck: true, reply: true, extendLease: true, pause: true},
		{name: "draining", phase: connPhaseDraining, reply: true, extendLease: true},
		{name: "closing", phase: connPhaseClosing, reply: true, extendLease: true, pause: true},
		{name: "retired", phase: connPhaseRetired},
		{name: "closed", phase: connPhaseClosed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := newLifecycleTestConnection(nil)
			for _, phase := range pathToPhase(tt.phase) {
				require.NoError(t, conn.transition(phase, "test"))
			}

			require.Equal(t, tt.heartbeat, conn.canWriteHeartbeat())
			require.Equal(t, tt.requestAck, conn.canWriteRequestAck())
			require.Equal(t, tt.reply, conn.canWriteReply())
			require.Equal(t, tt.extendLease, conn.canWriteExtendLease())
			require.Equal(t, tt.pause, conn.canWritePause())
		})
	}
}

func TestHandleInvokeMessageSkipsAckWhenPhaseDisallowsAck(t *testing.T) {
	r := require.New(t)
	var logOutput bytes.Buffer

	invoker := &blockingTestInvoker{
		release: make(chan struct{}),
	}
	logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	apiClient := newWorkerApiClient("", nil)
	h := &connectHandler{
		opts: Opts{
			SDKLanguage: "go",
			SDKVersion:  "test",
		},
		invokers: map[string]FunctionInvoker{
			"test-app": invoker,
		},
		logger:        logger,
		messageBuffer: newMessageBuffer(apiClient, logger),
	}
	preparedConn := newLifecycleTestConnection(nil)
	r.NoError(preparedConn.transition(connPhaseHandshaking, "test"))
	r.NoError(preparedConn.markActive("test"))
	r.NoError(preparedConn.beginDrain("test"))

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

func TestHandleInvokeMessageBuffersReplyWhenPhaseDisallowsReply(t *testing.T) {
	r := require.New(t)

	serverSawAck := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		defer func() { _ = conn.CloseNow() }()

		var msg connectproto.ConnectMessage
		r.NoError(wsReadProto(context.Background(), conn, &msg))
		r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, msg.Kind)
		close(serverSawAck)

		<-req.Context().Done()
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
	apiClient := newWorkerApiClient("", nil)
	h := &connectHandler{
		invokers: map[string]FunctionInvoker{
			"test-app": invoker,
		},
		logger:        logger,
		messageBuffer: newMessageBuffer(apiClient, logger),
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
	r.True(preparedConn.canWriteRequestAck())

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

	preparedConn.retire("test")
	close(release)

	select {
	case err := <-done:
		r.NoError(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invoke")
	}

	h.messageBuffer.lock.Lock()
	defer h.messageBuffer.lock.Unlock()
	r.Contains(h.messageBuffer.buffered, "request-id")
}

func TestHandleInvokeMessageBuffersReplyWriteFailureWithoutRetiringConnection(t *testing.T) {
	r := require.New(t)

	serverSawAck := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		defer func() { _ = conn.CloseNow() }()

		var msg connectproto.ConnectMessage
		r.NoError(wsReadProto(context.Background(), conn, &msg))
		r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, msg.Kind)
		close(serverSawAck)

		<-req.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
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
	apiClient := newWorkerApiClient("", nil)
	h := &connectHandler{
		invokers: map[string]FunctionInvoker{
			"test-app": invoker,
		},
		logger:        logger,
		messageBuffer: newMessageBuffer(apiClient, logger),
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}

	preparedConn := newLifecycleTestConnection(nil)
	preparedConn.ws = clientConn
	preparedConn.extendLeaseInterval = time.Hour
	r.NoError(preparedConn.transition(connPhaseHandshaking, "test"))
	r.NoError(preparedConn.markActive("test"))

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

	cancel()
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

func TestHeartbeatWriteIsSkippedWhenPhaseDisallowsHeartbeat(t *testing.T) {
	r := require.New(t)

	received := make(chan connectproto.GatewayMessageType, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		defer func() { _ = conn.CloseNow() }()

		for {
			var msg connectproto.ConnectMessage
			if err := wsReadProto(req.Context(), conn, &msg); err != nil {
				return
			}
			received <- msg.Kind
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(ctx, wsURL, nil)
	r.NoError(err)
	defer func() { _ = clientConn.CloseNow() }()

	logger := slog.New(slog.DiscardHandler)
	h := &connectHandler{
		logger: logger,
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
	}
	preparedConn := newLifecycleTestConnection(nil)
	preparedConn.ws = clientConn
	preparedConn.heartbeatInterval = 10 * time.Millisecond
	r.NoError(preparedConn.transition(connPhaseHandshaking, "test"))
	r.NoError(preparedConn.markActive("test"))
	preparedConn.retire("test")

	done := make(chan error, 1)
	go func() {
		done <- h.handleConnection(ctx, connectionEstablishData{}, preparedConn)
	}()

	select {
	case kind := <-received:
		t.Fatalf("unexpected websocket write: %s", kind)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handleConnection")
	}
}

func TestLeaseExtensionWriteIsSkippedWhenPhaseDisallowsExtendLease(t *testing.T) {
	r := require.New(t)

	received := make(chan connectproto.GatewayMessageType, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		defer func() { _ = conn.CloseNow() }()

		for {
			var msg connectproto.ConnectMessage
			if err := wsReadProto(req.Context(), conn, &msg); err != nil {
				return
			}
			received <- msg.Kind
		}
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
		logger: logger,
		invokers: map[string]FunctionInvoker{
			"test-app": invoker,
		},
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}
	preparedConn := newLifecycleTestConnection(nil)
	preparedConn.ws = clientConn
	preparedConn.extendLeaseInterval = 10 * time.Millisecond
	r.NoError(preparedConn.transition(connPhaseHandshaking, "test"))
	r.NoError(preparedConn.markActive("test"))

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
		_, err := h.connectInvoke(ctx, preparedConn, msg)
		done <- err
	}()

	select {
	case kind := <-received:
		r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, kind)
	case <-time.After(time.Second):
		t.Fatal("server did not receive worker request ack")
	}

	preparedConn.retire("test")

	select {
	case kind := <-received:
		t.Fatalf("unexpected websocket write: %s", kind)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-done:
		r.NoError(err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for invoke")
	}
}

func TestLeaseExtensionContinuesForOwnedWorkDuringDrainOrClose(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*connection) error
	}{
		{
			name: "draining",
			transition: func(conn *connection) error {
				return conn.beginDrain("test")
			},
		},
		{
			name: "closing",
			transition: func(conn *connection) error {
				return conn.beginClose("test")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)

			received := make(chan connectproto.ConnectMessage, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
					InsecureSkipVerify: true,
				})
				r.NoError(err)
				defer func() { _ = conn.CloseNow() }()

				for {
					var msg connectproto.ConnectMessage
					if err := wsReadProto(req.Context(), conn, &msg); err != nil {
						return
					}
					received <- msg
				}
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
				logger: logger,
				invokers: map[string]FunctionInvoker{
					"test-app": invoker,
				},
				workerPool: &workerPool{
					inProgressLeases:     map[string]string{},
					inProgressLeasesLock: sync.Mutex{},
				},
			}
			preparedConn := newLifecycleTestConnection(nil)
			preparedConn.ws = clientConn
			preparedConn.extendLeaseInterval = 10 * time.Millisecond
			r.NoError(preparedConn.transition(connPhaseHandshaking, "test"))
			r.NoError(preparedConn.markActive("test"))

			msg := mustExecutorRequestMessage(t, &connectproto.GatewayExecutorRequestData{
				RequestId:      "request-id",
				AccountId:      "account-id",
				EnvId:          "env-id",
				AppId:          "app-id",
				AppName:        "test-app",
				FunctionSlug:   "test-fn",
				RequestPayload: mustJSON(t, sdkrequest.Request{}),
				LeaseId:        "lease-id",
			})

			done := make(chan error, 1)
			go func() {
				_, err := h.connectInvoke(ctx, preparedConn, msg)
				done <- err
			}()

			select {
			case msg := <-received:
				r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, msg.Kind)
			case <-time.After(time.Second):
				t.Fatal("server did not receive worker request ack")
			}

			r.NoError(tt.transition(preparedConn))

			select {
			case msg := <-received:
				r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_EXTEND_LEASE, msg.Kind)

				var payload connectproto.WorkerRequestExtendLeaseData
				r.NoError(proto.Unmarshal(msg.Payload, &payload))
				r.Equal("request-id", payload.RequestId)
				r.Equal("lease-id", payload.LeaseId)
			case <-time.After(time.Second):
				t.Fatal("server did not receive worker request lease extension")
			}

			close(release)
			select {
			case err := <-done:
				r.NoError(err)
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for invoke")
			}
		})
	}
}

func pathToPhase(phase connPhase) []connPhase {
	switch phase {
	case connPhaseNew:
		return nil
	case connPhaseHandshaking:
		return []connPhase{connPhaseHandshaking}
	case connPhaseActive:
		return []connPhase{connPhaseHandshaking, connPhaseActive}
	case connPhaseDraining:
		return []connPhase{connPhaseHandshaking, connPhaseActive, connPhaseDraining}
	case connPhaseClosing:
		return []connPhase{connPhaseHandshaking, connPhaseActive, connPhaseClosing}
	case connPhaseRetired:
		return []connPhase{connPhaseRetired}
	case connPhaseClosed:
		return []connPhase{connPhaseClosed}
	default:
		return nil
	}
}
