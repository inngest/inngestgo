package connect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/inngest/inngest/pkg/connect/wsproto"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
)

func TestMaxWorkerConcurrency(t *testing.T) {
	t.Run("returns user provided value", func(t *testing.T) {
		r := require.New(t)

		maxConcurrency := int64(100)
		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: &maxConcurrency,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		r.Equal(int64(100), *result)
	})

	t.Run("returns environment variable value when user value not provided", func(t *testing.T) {
		r := require.New(t)

		// Set environment variable
		t.Setenv(maxWorkerConcurrencyEnvKey, "50")

		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: nil,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		r.Equal(int64(50), *result)
	})

	t.Run("returns default value when neither user value nor env var provided", func(t *testing.T) {
		r := require.New(t)

		// Ensure environment variable is not set
		_ = os.Unsetenv(maxWorkerConcurrencyEnvKey)

		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: nil,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		r.Equal(defaultMaxWorkerConcurrency, *result)
	})

	t.Run("user provided value takes precedence over environment variable", func(t *testing.T) {
		r := require.New(t)

		// Set environment variable
		t.Setenv(maxWorkerConcurrencyEnvKey, "50")

		maxConcurrency := int64(200)
		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: &maxConcurrency,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		r.Equal(int64(200), *result)
	})

	t.Run("handles invalid environment variable gracefully", func(t *testing.T) {
		r := require.New(t)

		// Set invalid environment variable
		t.Setenv(maxWorkerConcurrencyEnvKey, "invalid")

		h := &connectHandler{
			opts: Opts{
				MaxWorkerConcurrency: nil,
			},
			logger: slog.Default(),
		}

		result := h.maxWorkerConcurrency()
		r.NotNil(result)
		// Should return default value when env var is invalid
		r.Equal(defaultMaxWorkerConcurrency, *result)
	})
}

func TestConnectReturnsNonReconnectError(t *testing.T) {
	r := require.New(t)
	expected := errors.New("connection failed")
	apiClient := newWorkerApiClient("", nil)
	h := &connectHandler{
		opts:                   Opts{IsDev: true},
		logger:                 slog.Default(),
		notifyConnectDoneChan:  make(chan connectReport),
		notifyConnectedChan:    make(chan struct{}),
		initiateConnectionChan: make(chan struct{}, 1),
		apiClient:              apiClient,
		messageBuffer:          newMessageBuffer(apiClient, slog.Default()),
		state:                  ConnectionStateConnecting,
	}
	h.workerCtx, h.cancelWorkerCtx = context.WithCancel(context.Background())
	defer h.cancelWorkerCtx()

	done := make(chan error, 1)
	go func() {
		_, err := h.Connect(context.Background())
		done <- err
	}()

	h.notifyConnectDoneChan <- connectReport{
		err:       expected,
		reconnect: false,
	}

	select {
	case err := <-done:
		r.Error(err)
		r.ErrorIs(err, expected)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Connect to return")
	}
}

func TestConnectReturnsTooManyConnectionsError(t *testing.T) {
	r := require.New(t)
	apiClient := newWorkerApiClient("", nil)
	h := &connectHandler{
		opts:                   Opts{IsDev: true},
		logger:                 slog.Default(),
		notifyConnectDoneChan:  make(chan connectReport),
		notifyConnectedChan:    make(chan struct{}),
		initiateConnectionChan: make(chan struct{}, 1),
		apiClient:              apiClient,
		messageBuffer:          newMessageBuffer(apiClient, slog.Default()),
		state:                  ConnectionStateConnecting,
	}
	h.workerCtx, h.cancelWorkerCtx = context.WithCancel(context.Background())
	defer h.cancelWorkerCtx()

	done := make(chan error, 1)
	go func() {
		_, err := h.Connect(context.Background())
		done <- err
	}()

	h.notifyConnectDoneChan <- connectReport{
		err:       ErrTooManyConnections,
		reconnect: false,
	}

	select {
	case err := <-done:
		r.Error(err)
		r.Contains(err.Error(), "too many connections")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Connect to return")
	}
}

func TestConnectInvokeUsesProtoRequestAndJobIDs(t *testing.T) {
	r := require.New(t)
	requestID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	jobID := "job-123"

	accepted := make(chan *websocket.Conn, 1)
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		accepted <- conn
		<-done
		_ = conn.CloseNow()
	}))
	defer server.Close()
	defer close(done)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	r.NoError(err)
	defer func() { _ = clientConn.CloseNow() }()
	serverConn := <-accepted

	ackCh := make(chan connectproto.ConnectMessage, 1)
	errCh := make(chan error, 1)
	go func() {
		var ack connectproto.ConnectMessage
		errCh <- wsproto.Read(context.Background(), serverConn, &ack)
		ackCh <- ack
	}()

	requestPayload, err := json.Marshal(sdkrequest.Request{
		Event: []byte(`{"name":"test/connect.request.ids","data":{}}`),
		Steps: map[string]json.RawMessage{},
		CallCtx: sdkrequest.CallCtx{
			RequestID: "body-request-id",
			JobID:     "body-job-id",
		},
	})
	r.NoError(err)
	msgPayload, err := proto.Marshal(&connectproto.GatewayExecutorRequestData{
		RequestId:      requestID,
		JobId:          jobID,
		AppName:        "app",
		FunctionSlug:   "fn",
		RequestPayload: requestPayload,
	})
	r.NoError(err)

	invoker := &captureInvoker{}
	h := &connectHandler{
		logger: slog.New(slog.DiscardHandler),
		invokers: map[string]FunctionInvoker{
			"app": invoker,
		},
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}
	resp, err := h.connectInvoke(context.Background(), &connection{
		ws:                  clientConn,
		extendLeaseInterval: time.Hour,
	}, &connectproto.ConnectMessage{
		Payload: msgPayload,
	})
	r.NoError(err)
	r.Equal(requestID, resp.RequestId)
	r.Equal(requestID, invoker.request.CallCtx.RequestID)
	r.Equal(jobID, invoker.request.CallCtx.JobID)

	r.NoError(<-errCh)
	r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, (<-ackCh).Kind)
}

func TestMessageReadLimit(t *testing.T) {
	t.Run("uses default limit when nil", func(t *testing.T) {
		r := require.New(t)

		// Create a test WebSocket server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
				InsecureSkipVerify: true,
			})
			r.NoError(err)
			defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

			// Keep connection open briefly
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Connect without setting limit (should use default 32KB)
		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		r.NoError(err)
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		// The default limit should be 32KB (32768 bytes)
		// We don't call SetReadLimit, so it uses the library default
	})

	t.Run("uses custom limit when set", func(t *testing.T) {
		r := require.New(t)

		customLimit := int64(1024) // 1KB

		// Create a test WebSocket server that sends a large message
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
				InsecureSkipVerify: true,
			})
			r.NoError(err)
			defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

			// Send a 2KB message (larger than our 1KB limit)
			largeMsg := make([]byte, 2048)
			for i := range largeMsg {
				largeMsg[i] = byte('A')
			}

			ctx := context.Background()
			err = conn.Write(ctx, websocket.MessageBinary, largeMsg)
			r.NoError(err)

			// Keep connection open
			time.Sleep(500 * time.Millisecond)
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		r.NoError(err)
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		// Set the custom limit
		conn.SetReadLimit(customLimit)

		// Try to read the large message - should fail
		_, _, err = conn.Read(ctx)
		r.Error(err)

		// Verify the error message indicates the read limit was hit
		r.Contains(err.Error(), "read limited at")
	})

	t.Run("allows unlimited when set to -1", func(t *testing.T) {
		r := require.New(t)

		// Create a test WebSocket server that sends a very large message
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
				InsecureSkipVerify: true,
			})
			r.NoError(err)
			defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

			// Send a 1MB message (much larger than default 32KB)
			largeMsg := make([]byte, 1024*1024)
			for i := range largeMsg {
				largeMsg[i] = byte('B')
			}

			ctx := context.Background()
			err = conn.Write(ctx, websocket.MessageBinary, largeMsg)
			r.NoError(err)

			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		r.NoError(err)
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		// Set unlimited
		conn.SetReadLimit(-1)

		// Should be able to read the large message
		_, data, err := conn.Read(ctx)
		r.NoError(err)
		r.Len(data, 1024*1024)
	})

	t.Run("zero value uses default limit", func(t *testing.T) {
		r := require.New(t)

		zero := int64(0)
		h := &connectHandler{
			opts: Opts{
				MessageReadLimit: &zero,
			},
			logger: slog.Default(),
		}

		r.NotNil(h.opts.MessageReadLimit)
		r.Equal(int64(0), *h.opts.MessageReadLimit)
		// In prepareConnection, we check for nil or 0 and skip SetReadLimit
		// This means the default 32KB limit will be used
	})

	t.Run("respects custom limit in opts", func(t *testing.T) {
		r := require.New(t)

		customLimit := int64(10 * 1024 * 1024) // 10MB
		h := &connectHandler{
			opts: Opts{
				MessageReadLimit: &customLimit,
			},
			logger: slog.Default(),
		}

		r.NotNil(h.opts.MessageReadLimit)
		r.Equal(int64(10*1024*1024), *h.opts.MessageReadLimit)
	})
}

func TestMessageReadLimitWithProtobuf(t *testing.T) {
	t.Run("rejects messages exceeding limit", func(t *testing.T) {
		r := require.New(t)

		smallLimit := int64(1024) // 1KB limit

		// Create a test WebSocket server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
				InsecureSkipVerify: true,
			})
			r.NoError(err)
			defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

			ctx := context.Background()

			// Send a protobuf message with a large payload (exceeds 1KB)
			largePayload := make([]byte, 2048)
			for i := range largePayload {
				largePayload[i] = byte('X')
			}

			msg := &connectproto.ConnectMessage{
				Kind:    connectproto.GatewayMessageType_GATEWAY_HEARTBEAT,
				Payload: largePayload,
			}

			marshaled, err := proto.Marshal(msg)
			r.NoError(err)

			err = conn.Write(ctx, websocket.MessageBinary, marshaled)
			r.NoError(err)

			time.Sleep(500 * time.Millisecond)
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		r.NoError(err)
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		// Set small limit
		conn.SetReadLimit(smallLimit)

		// Try to read the message - should fail due to size
		_, _, err = conn.Read(ctx)
		r.Error(err)

		// Verify the error message indicates the read limit was hit
		r.Contains(err.Error(), "read limited at")
	})

	t.Run("accepts messages within limit", func(t *testing.T) {
		r := require.New(t)

		largeLimit := int64(10 * 1024 * 1024) // 10MB limit

		// Create a test WebSocket server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
				InsecureSkipVerify: true,
			})
			r.NoError(err)
			defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

			ctx := context.Background()

			// Send a protobuf message within the limit
			msg := &connectproto.ConnectMessage{
				Kind:    connectproto.GatewayMessageType_GATEWAY_HEARTBEAT,
				Payload: []byte("small payload"),
			}

			marshaled, err := proto.Marshal(msg)
			r.NoError(err)

			err = conn.Write(ctx, websocket.MessageBinary, marshaled)
			r.NoError(err)

			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, wsURL, nil)
		r.NoError(err)
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		// Set large limit
		conn.SetReadLimit(largeLimit)

		// Should successfully read the message
		_, data, err := conn.Read(ctx)
		r.NoError(err)
		r.NotEmpty(data)

		// Verify we can unmarshal the protobuf
		var msg connectproto.ConnectMessage
		err = proto.Unmarshal(data, &msg)
		r.NoError(err)
		r.Equal(connectproto.GatewayMessageType_GATEWAY_HEARTBEAT, msg.Kind)
	})
}

func TestHandleWorkerRequestExtendLeaseAck(t *testing.T) {
	newLeaseID := "new-lease-id"

	tests := []struct {
		name           string
		initialLeases  map[string]string
		payload        *connectproto.WorkerRequestExtendLeaseAckData
		expectedLeases map[string]string
	}{
		{
			name: "updates existing request lease",
			initialLeases: map[string]string{
				"request-id": "old-lease-id",
			},
			payload: &connectproto.WorkerRequestExtendLeaseAckData{
				RequestId:  "request-id",
				NewLeaseId: &newLeaseID,
			},
			expectedLeases: map[string]string{
				"request-id": "new-lease-id",
			},
		},
		{
			name: "removes request lease when ack has no new lease",
			initialLeases: map[string]string{
				"request-id": "old-lease-id",
			},
			payload: &connectproto.WorkerRequestExtendLeaseAckData{
				RequestId: "request-id",
			},
			expectedLeases: map[string]string{},
		},
		{
			name:          "ignores stale ack for completed request",
			initialLeases: map[string]string{},
			payload: &connectproto.WorkerRequestExtendLeaseAckData{
				RequestId:  "request-id",
				NewLeaseId: &newLeaseID,
			},
			expectedLeases: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)

			payload, err := proto.Marshal(tt.payload)
			r.NoError(err)

			h := &connectHandler{
				workerPool: &workerPool{
					inProgressLeases: tt.initialLeases,
				},
			}

			err = h.handleWorkerRequestExtendLeaseAck(&connectproto.ConnectMessage{
				Kind:    connectproto.GatewayMessageType_WORKER_REQUEST_EXTEND_LEASE_ACK,
				Payload: payload,
			})
			r.NoError(err)

			r.Equal(tt.expectedLeases, h.workerPool.inProgressLeases)
		})
	}
}

func TestHandleMessageReplyAckClearsPendingAck(t *testing.T) {
	r := require.New(t)

	apiClient := newWorkerApiClient("", nil)
	h := &connectHandler{
		messageBuffer: newMessageBuffer(apiClient, slog.Default()),
	}
	h.messageBuffer.pendingAck["request-id"] = &connectproto.SDKResponse{
		RequestId: "request-id",
	}

	payload, err := proto.Marshal(&connectproto.WorkerReplyAckData{
		RequestId: "request-id",
	})
	r.NoError(err)

	err = h.handleMessageReplyAck(&connectproto.ConnectMessage{
		Kind:    connectproto.GatewayMessageType_WORKER_REPLY_ACK,
		Payload: payload,
	})
	r.NoError(err)

	h.messageBuffer.lock.Lock()
	defer h.messageBuffer.lock.Unlock()
	r.NotContains(h.messageBuffer.pendingAck, "request-id")
}

func TestHandleInvokeMessageBuffersReplyWhenConnectionClosesAfterAck(t *testing.T) {
	r := require.New(t)

	serverSawAck := make(chan struct{})
	serverClosed := make(chan struct{})
	invokerRelease := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)

		var msg connectproto.ConnectMessage
		err = wsReadProto(req.Context(), conn, &msg)
		r.NoError(err)
		r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, msg.Kind)
		close(serverSawAck)
		close(serverClosed)

		_ = conn.Close(websocket.StatusInternalError, "connect_internal_error")
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(ctx, wsURL, nil)
	r.NoError(err)
	defer func() { _ = clientConn.CloseNow() }()

	apiClient := newWorkerApiClient("", nil)
	invoker := &blockingTestInvoker{
		release: invokerRelease,
	}
	h := &connectHandler{
		opts: Opts{
			SDKLanguage: "go",
			SDKVersion:  "test",
		},
		invokers: map[string]FunctionInvoker{
			"test-app": invoker,
		},
		logger:        slog.Default(),
		messageBuffer: newMessageBuffer(apiClient, slog.Default()),
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}

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

	preparedConn := &connection{
		ws:                  clientConn,
		connectionId:        "old-connection",
		extendLeaseInterval: time.Hour,
	}

	go func() {
		<-serverClosed
		preparedConn.retire()
		close(invokerRelease)
	}()

	err = h.handleInvokeMessage(ctx, preparedConn, msg)
	r.NoError(err)
	r.True(invoker.called.Load())

	h.messageBuffer.lock.Lock()
	defer h.messageBuffer.lock.Unlock()
	r.Contains(h.messageBuffer.buffered, "request-id")
	r.NotContains(h.messageBuffer.pendingAck, "request-id")

	select {
	case <-serverSawAck:
	default:
		t.Fatal("server did not receive worker request ack")
	}
}

func TestHandleInvokeMessageSkipsQueuedRequestForRetiredConnection(t *testing.T) {
	r := require.New(t)

	apiClient := newWorkerApiClient("", nil)
	invoker := &blockingTestInvoker{
		release: make(chan struct{}),
	}
	h := &connectHandler{
		invokers: map[string]FunctionInvoker{
			"test-app": invoker,
		},
		logger:        slog.Default(),
		messageBuffer: newMessageBuffer(apiClient, slog.Default()),
	}

	msg := mustExecutorRequestMessage(t, &connectproto.GatewayExecutorRequestData{
		RequestId:      "request-id",
		EnvId:          "env-id",
		AppId:          "app-id",
		AppName:        "test-app",
		FunctionSlug:   "test-fn",
		RequestPayload: mustJSON(t, sdkrequest.Request{}),
		LeaseId:        "lease-id",
	})

	preparedConn := &connection{
		connectionId:        "old-connection",
		extendLeaseInterval: time.Hour,
	}
	preparedConn.retire()

	err := h.handleInvokeMessage(context.Background(), preparedConn, msg)
	r.NoError(err)
	r.False(invoker.called.Load())

	h.messageBuffer.lock.Lock()
	defer h.messageBuffer.lock.Unlock()
	r.Empty(h.messageBuffer.buffered)
	r.Empty(h.messageBuffer.pendingAck)
}

type blockingTestInvoker struct {
	release <-chan struct{}
	called  atomic.Bool
}

func (b *blockingTestInvoker) InvokeFunction(ctx context.Context, slug string, stepId *string, request sdkrequest.Request) (any, []sdkrequest.GeneratorOpcode, error) {
	b.called.Store(true)
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-b.release:
		return map[string]string{"ok": "true"}, nil, nil
	}
}

func mustExecutorRequestMessage(t *testing.T, data *connectproto.GatewayExecutorRequestData) *connectproto.ConnectMessage {
	t.Helper()

	payload, err := proto.Marshal(data)
	require.NoError(t, err)

	return &connectproto.ConnectMessage{
		Kind:    connectproto.GatewayMessageType_GATEWAY_EXECUTOR_REQUEST,
		Payload: payload,
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()

	payload, err := json.Marshal(v)
	require.NoError(t, err)
	return payload
}

func wsReadProto(ctx context.Context, conn *websocket.Conn, msg proto.Message) error {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	return proto.Unmarshal(data, msg)
}

type captureInvoker struct {
	request sdkrequest.Request
}

func (c *captureInvoker) InvokeFunction(
	ctx context.Context,
	slug string,
	stepId *string,
	request sdkrequest.Request,
) (any, []sdkrequest.GeneratorOpcode, error) {
	c.request = request
	return map[string]bool{"ok": true}, nil, nil
}
