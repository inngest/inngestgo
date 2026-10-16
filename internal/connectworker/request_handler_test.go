package connectworker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/inngest/inngest/pkg/connect/wsproto"
	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// fakeRequestHandler records each call and returns the configured results.
type fakeRequestHandler struct {
	admitErr error
	resp     Response
	serveErr error

	mu       sync.Mutex
	admitted []string
	served   []string
	done     []string
}

func (f *fakeRequestHandler) Admit(req *connectproto.GatewayExecutorRequestData) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admitted = append(f.admitted, req.RequestId)
	return f.admitErr
}

func (f *fakeRequestHandler) Serve(_ context.Context, req *connectproto.GatewayExecutorRequestData) (Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.served = append(f.served, req.RequestId)
	return f.resp, f.serveErr
}

func (f *fakeRequestHandler) Done(req *connectproto.GatewayExecutorRequestData) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = append(f.done, req.RequestId)
}

func (f *fakeRequestHandler) calls() (admitted, served, done []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.admitted, f.served, f.done
}

// newTestWebsocketPair returns the client and server ends of one websocket.
func newTestWebsocketPair(t *testing.T) (client, server *websocket.Conn) {
	t.Helper()

	accepted := make(chan *websocket.Conn, 1)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		accepted <- conn
		<-done
		_ = conn.CloseNow()
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) })

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.Dial(context.Background(), wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.CloseNow() })

	return client, <-accepted
}

func TestReceiveExecutorRequest(t *testing.T) {
	req := &connectproto.GatewayExecutorRequestData{
		RequestId:    "request-id",
		AppName:      "app",
		FunctionSlug: "fn",
	}
	validPayload, err := proto.Marshal(req)
	require.NoError(t, err)

	tests := []struct {
		name         string
		payload      []byte
		admitErr     error
		wantAdmitted []string
		wantQueued   bool
	}{
		{
			name:         "admitted request goes to the worker pool",
			payload:      validPayload,
			wantAdmitted: []string{"request-id"},
			wantQueued:   true,
		},
		{
			name:         "rejected request is dropped",
			payload:      validPayload,
			admitErr:     errors.New("draining"),
			wantAdmitted: []string{"request-id"},
		},
		{
			name:    "malformed payload is dropped before admit",
			payload: []byte{0xff, 0xff, 0xff},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)

			handler := &fakeRequestHandler{admitErr: tt.admitErr}
			queued := make(chan workerPoolMsg, 1)
			h := &connectHandler{
				handler: handler,
				logger:  slog.New(slog.DiscardHandler),
			}
			h.workerPool = NewWorkerPool(t.Context(), 1, func(msg workerPoolMsg) {
				queued <- msg
			})

			preparedConn := newLifecycleTestConnection(nil)
			h.receiveExecutorRequest(h.logger, preparedConn, &connectproto.ConnectMessage{
				Kind:    connectproto.GatewayMessageType_GATEWAY_EXECUTOR_REQUEST,
				Payload: tt.payload,
			})

			admitted, served, done := handler.calls()
			r.Equal(tt.wantAdmitted, admitted)
			r.Empty(served)
			r.Empty(done)

			if !tt.wantQueued {
				select {
				case msg := <-queued:
					t.Fatalf("unexpected queued request %q", msg.req.RequestId)
				case <-time.After(50 * time.Millisecond):
				}
				return
			}

			select {
			case msg := <-queued:
				r.True(proto.Equal(req, msg.req))
				r.Same(preparedConn, msg.preparedConn)
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for queued request")
			}
		})
	}
}

func TestProcessExecutorRequestUsesRequestHandler(t *testing.T) {
	r := require.New(t)

	retryAfter := "2026-10-14T00:00:00Z"
	handler := &fakeRequestHandler{
		resp: Response{
			Status:         connectproto.SDKResponseStatus_NOT_COMPLETED,
			Body:           []byte(`[{"op":"Step"}]`),
			NoRetry:        true,
			RetryAfter:     &retryAfter,
			SDKVersion:     "js:v3.40.0",
			RequestVersion: 2,
		},
	}
	logger := slog.New(slog.DiscardHandler)
	h := &connectHandler{
		handler:       handler,
		logger:        logger,
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
		workerPool: &workerPool{
			inProgressLeases: map[string]string{},
		},
	}

	clientConn, serverConn := newTestWebsocketPair(t)
	preparedConn := &connection{
		ws:                  clientConn,
		extendLeaseInterval: time.Hour,
	}
	activateTestConnection(t, preparedConn)

	stepID := "step-id"
	req := &connectproto.GatewayExecutorRequestData{
		RequestId:      "request-id",
		AccountId:      "account-id",
		EnvId:          "env-id",
		AppId:          "app-id",
		AppName:        "app",
		FunctionSlug:   "fn",
		StepId:         &stepID,
		RequestPayload: []byte(`{"raw":true}`),
		SystemTraceCtx: []byte("system-trace"),
		UserTraceCtx:   []byte("user-trace"),
		RunId:          "run-id",
		LeaseId:        "lease-id",
	}

	h.workerPool.inProgress.Add(1)
	go h.processExecutorRequest(workerPoolMsg{req: req, preparedConn: preparedConn})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var ack connectproto.ConnectMessage
	r.NoError(wsproto.Read(ctx, serverConn, &ack))
	r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, ack.Kind)

	var reply connectproto.ConnectMessage
	r.NoError(wsproto.Read(ctx, serverConn, &reply))
	r.Equal(connectproto.GatewayMessageType_WORKER_REPLY, reply.Kind)

	var resp connectproto.SDKResponse
	r.NoError(proto.Unmarshal(reply.Payload, &resp))
	r.True(proto.Equal(&connectproto.SDKResponse{
		RequestId:      "request-id",
		AccountId:      "account-id",
		EnvId:          "env-id",
		AppId:          "app-id",
		Status:         connectproto.SDKResponseStatus_NOT_COMPLETED,
		Body:           []byte(`[{"op":"Step"}]`),
		NoRetry:        true,
		RetryAfter:     &retryAfter,
		SdkVersion:     "js:v3.40.0",
		RequestVersion: 2,
		SystemTraceCtx: []byte("system-trace"),
		UserTraceCtx:   []byte("user-trace"),
		RunId:          "run-id",
	}, &resp), "unexpected reply %v", &resp)

	h.workerPool.Wait()
	_, served, done := handler.calls()
	r.Equal([]string{"request-id"}, served)
	r.Equal([]string{"request-id"}, done)
}

func TestProcessExecutorRequestCallsDoneWithoutServe(t *testing.T) {
	tests := []struct {
		name     string
		closing  bool
		serveErr error
		// wantServed is true when the request is ACKed and reaches Serve.
		wantServed bool
	}{
		{
			name:    "connection closing before the ACK",
			closing: true,
		},
		{
			name:       "serve error sends no reply",
			serveErr:   errors.New("app unavailable"),
			wantServed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)

			handler := &fakeRequestHandler{serveErr: tt.serveErr}
			logger := slog.New(slog.DiscardHandler)
			h := &connectHandler{
				handler:       handler,
				logger:        logger,
				messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
				workerPool: &workerPool{
					inProgressLeases: map[string]string{},
				},
			}

			clientConn, serverConn := newTestWebsocketPair(t)
			preparedConn := &connection{
				ws:                  clientConn,
				extendLeaseInterval: time.Hour,
			}
			activateTestConnection(t, preparedConn)
			if tt.closing {
				r.NoError(preparedConn.beginClose("test"))
			}

			received := make(chan connectproto.GatewayMessageType, 4)
			go func() {
				for {
					var msg connectproto.ConnectMessage
					if err := wsproto.Read(context.Background(), serverConn, &msg); err != nil {
						return
					}
					received <- msg.Kind
				}
			}()

			req := &connectproto.GatewayExecutorRequestData{
				RequestId: "request-id",
				AppName:   "app",
				LeaseId:   "lease-id",
			}
			h.workerPool.inProgress.Add(1)
			h.processExecutorRequest(workerPoolMsg{req: req, preparedConn: preparedConn})

			_, served, done := handler.calls()
			r.Equal([]string{"request-id"}, done)
			if tt.wantServed {
				r.Equal([]string{"request-id"}, served)
				r.Equal(connectproto.GatewayMessageType_WORKER_REQUEST_ACK, <-received)
			} else {
				r.Empty(served)
			}

			select {
			case kind := <-received:
				t.Fatalf("unexpected message %s", kind)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}
