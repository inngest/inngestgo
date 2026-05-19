package connect

import (
	"context"
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
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/require"
)

func TestHandleConnectionGatewayClosingDrainsUntilReplacementConnected(t *testing.T) {
	r := require.New(t)

	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		defer close(serverDone)
		defer func() { _ = conn.CloseNow() }()

		// Simulate the gateway asking this generation to drain while keeping
		// the old transport open for allowed in-flight replies.
		r.NoError(wsproto.Write(context.Background(), conn, &connectproto.ConnectMessage{
			Kind: connectproto.GatewayMessageType_GATEWAY_CLOSING,
		}))

		for {
			var msg connectproto.ConnectMessage
			if err := wsReadProto(req.Context(), conn, &msg); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(ctx, wsURL, nil)
	r.NoError(err)
	defer func() { _ = clientConn.CloseNow() }()

	logger := slog.New(slog.DiscardHandler)
	replacementStarted := make(chan []string, 1)
	releaseReplacement := make(chan struct{})
	h := &connectHandler{
		logger:        logger,
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}
	h.startConnection = func(_ context.Context, _ connectionEstablishData, opts ...connectOpt) {
		// Hold replacement completion so the test can observe the old
		// generation in Draining before it is retired.
		o := connectOpts{}
		for _, opt := range opts {
			opt(&o)
		}

		replacementStarted <- o.excludeGateways
		<-releaseReplacement

		if o.notifyConnectedChan != nil {
			o.notifyConnectedChan <- struct{}{}
			close(o.notifyConnectedChan)
		}
	}

	preparedConn := &connection{
		ws:                clientConn,
		gatewayGroupName:  "old-gateway",
		connectionId:      "old-connection",
		heartbeatInterval: time.Hour,
	}
	activateTestConnection(t, preparedConn)

	done := make(chan error, 1)
	go func() {
		done <- h.handleConnection(ctx, connectionEstablishData{}, preparedConn)
	}()

	select {
	case excluded := <-replacementStarted:
		r.Equal([]string{"old-gateway"}, excluded)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement connection attempt")
	}

	r.Equal(connPhaseDraining, preparedConn.phase())
	r.False(preparedConn.canWriteHeartbeat())
	r.False(preparedConn.canWriteRequestAck())
	r.True(preparedConn.canWriteReply())

	// Once the replacement reports connected, the old generation should retire
	// and close through lifecycle helpers.
	close(releaseReplacement)

	select {
	case err := <-done:
		r.ErrorIs(err, errGatewayDraining)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handleConnection")
	}

	r.Equal(connPhaseClosed, preparedConn.phase())
	r.True(preparedConn.isRetired())

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for old websocket close")
	}
}

func TestHandleConnectionGatewayClosingRetiresAndClosesAfterReplacementTimeout(t *testing.T) {
	r := require.New(t)

	originalTimeout := gatewayDrainReplacementTimeout
	gatewayDrainReplacementTimeout = 25 * time.Millisecond
	t.Cleanup(func() {
		gatewayDrainReplacementTimeout = originalTimeout
	})

	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		defer close(serverDone)
		defer func() { _ = conn.CloseNow() }()

		// The server keeps the socket readable so close completion is driven by
		// the SDK replacement timeout, not by a peer-side close race.
		r.NoError(wsproto.Write(context.Background(), conn, &connectproto.ConnectMessage{
			Kind: connectproto.GatewayMessageType_GATEWAY_CLOSING,
		}))

		for {
			var msg connectproto.ConnectMessage
			if err := wsReadProto(req.Context(), conn, &msg); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(ctx, wsURL, nil)
	r.NoError(err)
	defer func() { _ = clientConn.CloseNow() }()

	logger := slog.New(slog.DiscardHandler)
	replacementStarted := make(chan struct{})
	h := &connectHandler{
		logger:        logger,
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}
	h.startConnection = func(context.Context, connectionEstablishData, ...connectOpt) {
		// Start but never notify connection success; handleConnection should
		// bound the drain with gatewayDrainReplacementTimeout.
		close(replacementStarted)
	}

	preparedConn := &connection{
		ws:                clientConn,
		gatewayGroupName:  "old-gateway",
		connectionId:      "old-connection",
		heartbeatInterval: time.Hour,
	}
	activateTestConnection(t, preparedConn)

	done := make(chan error, 1)
	go func() {
		done <- h.handleConnection(ctx, connectionEstablishData{}, preparedConn)
	}()

	select {
	case <-replacementStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement connection attempt")
	}

	select {
	case err := <-done:
		r.ErrorIs(err, errGatewayDraining)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement timeout")
	}

	r.Equal(connPhaseClosed, preparedConn.phase())
	r.True(preparedConn.isRetired())

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for old websocket close")
	}
}

func TestHandleConnectionGatewayClosingLateReplacementNotificationDoesNotBlock(t *testing.T) {
	r := require.New(t)

	originalTimeout := gatewayDrainReplacementTimeout
	gatewayDrainReplacementTimeout = 25 * time.Millisecond
	t.Cleanup(func() {
		gatewayDrainReplacementTimeout = originalTimeout
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		r.NoError(err)
		defer func() { _ = conn.CloseNow() }()

		r.NoError(wsproto.Write(context.Background(), conn, &connectproto.ConnectMessage{
			Kind: connectproto.GatewayMessageType_GATEWAY_CLOSING,
		}))

		for {
			var msg connectproto.ConnectMessage
			if err := wsReadProto(req.Context(), conn, &msg); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(ctx, wsURL, nil)
	r.NoError(err)
	defer func() { _ = clientConn.CloseNow() }()

	logger := slog.New(slog.DiscardHandler)
	replacementStarted := make(chan struct{})
	releaseReplacement := make(chan struct{})
	replacementFinished := make(chan struct{})
	h := &connectHandler{
		logger:        logger,
		messageBuffer: newMessageBuffer(newWorkerApiClient("", nil), logger),
		workerPool: &workerPool{
			inProgressLeases:     map[string]string{},
			inProgressLeasesLock: sync.Mutex{},
		},
	}
	h.startConnection = func(_ context.Context, _ connectionEstablishData, opts ...connectOpt) {
		defer close(replacementFinished)

		// Notify after handleConnection has already timed out. The buffered
		// notification channel must prevent this goroutine from blocking.
		o := connectOpts{}
		for _, opt := range opts {
			opt(&o)
		}

		close(replacementStarted)
		<-releaseReplacement

		if o.notifyConnectedChan != nil {
			o.notifyConnectedChan <- struct{}{}
			close(o.notifyConnectedChan)
		}
	}

	preparedConn := &connection{
		ws:                clientConn,
		gatewayGroupName:  "old-gateway",
		connectionId:      "old-connection",
		heartbeatInterval: time.Hour,
	}
	activateTestConnection(t, preparedConn)

	done := make(chan error, 1)
	go func() {
		done <- h.handleConnection(ctx, connectionEstablishData{}, preparedConn)
	}()

	select {
	case <-replacementStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement connection attempt")
	}

	select {
	case err := <-done:
		r.ErrorIs(err, errGatewayDraining)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replacement timeout")
	}

	// Releasing the delayed replacement after timeout verifies the notification
	// send is still non-blocking.
	close(releaseReplacement)

	select {
	case <-replacementFinished:
	case <-time.After(time.Second):
		t.Fatal("late replacement notification blocked")
	}
}

func TestHandleInvokeMessageAttemptsReplyDuringDrainForAckedWork(t *testing.T) {
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

	// After ACK, the request remains owned by this generation. Draining should
	// still permit the reply write before the generation is retired.
	r.NoError(preparedConn.beginDrain("test"))
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
		t.Fatal("server did not receive worker reply during drain")
	}

	h.messageBuffer.lock.Lock()
	defer h.messageBuffer.lock.Unlock()
	r.NotContains(h.messageBuffer.buffered, "request-id")
}

func TestHandleInvokeMessageBuffersDrainingReplyWriteFailure(t *testing.T) {
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

	// Draining allows the reply attempt; forcing the transport closed proves a
	// failed draining write buffers the response without retiring here.
	r.NoError(preparedConn.beginDrain("test"))
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
