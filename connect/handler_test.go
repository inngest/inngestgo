package connect

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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
	h := &connectHandler{
		opts:                   Opts{IsDev: true},
		logger:                 slog.Default(),
		notifyConnectDoneChan:  make(chan connectReport),
		notifyConnectedChan:    make(chan struct{}),
		initiateConnectionChan: make(chan struct{}, 1),
		messageBuffer:          &messageBuffer{},
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
		reconnect: true,
	}

	select {
	case err := <-done:
		r.Error(err)
		r.Contains(err.Error(), "too many connections")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Connect to return")
	}
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
