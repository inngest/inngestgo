package inngestgo

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/inngest/inngest/pkg/connect/types"
	"github.com/inngest/inngest/pkg/connect/wsproto"
	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/publicerr"
	"github.com/inngest/inngest/pkg/syscode"
	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	sdkerrors "github.com/inngest/inngestgo/errors"
	"github.com/pbnjay/memory"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"net/url"
	"runtime"
	"sync"

	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/oklog/ulid/v2"
	"os"
	"strings"
	"time"
)

type workerPoolMsg struct {
	msg *connectproto.ConnectMessage
	ws  *websocket.Conn
}

type connectHandler struct {
	h *handler

	connectionId ulid.ULID

	messageBuffer     []*connectproto.ConnectMessage
	messageBufferLock sync.Mutex

	inProgress sync.WaitGroup

	workerPoolMsgs chan workerPoolMsg

	hostsManager *hostsManager
}

type hostsManager struct {
	gatewayHosts            []string
	availableGatewayHosts   map[string]struct{}
	drainingGatewayHosts    map[string]struct{}
	unreachableGatewayHosts map[string]struct{}
	hostsLock               sync.RWMutex
}

func newHostsManager(gatewayHosts []string) *hostsManager {
	hm := &hostsManager{
		gatewayHosts:            gatewayHosts,
		availableGatewayHosts:   make(map[string]struct{}),
		drainingGatewayHosts:    make(map[string]struct{}),
		unreachableGatewayHosts: make(map[string]struct{}),
	}

	hm.resetGateways()

	return hm
}

func (h *hostsManager) pickAvailableGateway() string {
	h.hostsLock.RLock()
	defer h.hostsLock.RUnlock()

	for host := range h.availableGatewayHosts {
		return host
	}
	return ""
}

func (h *hostsManager) markDrainingGateway(host string) {
	h.hostsLock.Lock()
	defer h.hostsLock.Unlock()
	delete(h.availableGatewayHosts, host)
	h.drainingGatewayHosts[host] = struct{}{}
}

func (h *hostsManager) markUnreachableGateway(host string) {
	h.hostsLock.Lock()
	defer h.hostsLock.Unlock()
	delete(h.availableGatewayHosts, host)
	h.unreachableGatewayHosts[host] = struct{}{}
}

func (h *hostsManager) resetGateways() {
	h.hostsLock.Lock()
	defer h.hostsLock.Unlock()
	h.availableGatewayHosts = make(map[string]struct{})
	h.drainingGatewayHosts = make(map[string]struct{})
	h.unreachableGatewayHosts = make(map[string]struct{})
	for _, host := range h.gatewayHosts {
		h.availableGatewayHosts[host] = struct{}{}
	}
}

// authContext is wrapper for information related to authentication
type authContext struct {
	signingKey string
	fallback   bool
}

func (h *connectHandler) connectURLs() []string {
	if h.h.isDev() {
		return []string{fmt.Sprintf("%s/connect", strings.Replace(DevServerURL(), "http", "ws", 1))}
	}

	if len(h.h.ConnectURLs) > 0 {
		return h.h.ConnectURLs
	}

	return nil
}

func (h *handler) Connect(ctx context.Context) error {
	h.useConnect = true
	concurrency := h.HandlerOpts.GetWorkerConcurrency()

	// This determines how many messages can be processed by each worker at once.
	ch := connectHandler{
		h: h,

		// Should this use the same buffer size as the worker pool?
		workerPoolMsgs: make(chan workerPoolMsg, concurrency),
	}

	for i := 0; i < concurrency; i++ {
		go ch.workerPool(ctx)
	}

	defer func() {
		// TODO Push remaining messages to another destination for processing?
	}()

	err := ch.Connect(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}

		return fmt.Errorf("could not establish connection: %w", err)
	}

	return nil
}

func (h *connectHandler) instanceId() string {
	if h.h.InstanceId != nil {
		return *h.h.InstanceId
	}

	hostname, _ := os.Hostname()
	if hostname != "" {
		return hostname
	}

	// TODO Is there any stable identifier that can be used as a fallback?
	return "<missing-instance-id>"
}

func (h *connectHandler) workerPool(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-h.workerPoolMsgs:
			h.processExecutorRequest(msg.ws, msg.msg)
		}
	}
}

func (h *connectHandler) Connect(ctx context.Context) error {
	signingKey := h.h.GetSigningKey()
	if signingKey == "" {
		return fmt.Errorf("must provide signing key")
	}
	auth := authContext{signingKey: signingKey}

	numCpuCores := runtime.NumCPU()
	totalMem := memory.TotalMemory()

	connectPlaceholder := url.URL{
		Scheme: "ws",
		Host:   "connect",
	}

	fns, err := createFunctionConfigs(h.h.appName, h.h.funcs, connectPlaceholder, true)
	if err != nil {
		return fmt.Errorf("error creating function configs: %w", err)
	}

	marshaledFns, err := json.Marshal(fns)
	if err != nil {
		return fmt.Errorf("failed to serialize connect config: %w", err)
	}

	marshaledCapabilities, err := json.Marshal(capabilities)
	if err != nil {
		return fmt.Errorf("failed to serialize connect config: %w", err)
	}

	hosts := h.connectURLs()
	if len(hosts) == 0 {
		return fmt.Errorf("no connect URLs provided")
	}

	h.hostsManager = newHostsManager(hosts)

	eg := errgroup.Group{}

	notifyConnectDoneChan := make(chan connectReport)
	notifyConnectedChan := make(chan struct{})
	initiateConnectionChan := make(chan struct{})

	var attempts int
	eg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-notifyConnectedChan:
				attempts = 0
				continue
			case msg := <-notifyConnectDoneChan:
				h.h.Logger.Error("connect failed", "err", err, "reconnect", msg.reconnect)

				if !msg.reconnect {
					return err
				}

				if msg.err != nil {
					closeErr := websocket.CloseError{}
					if errors.As(err, &closeErr) {
						switch closeErr.Reason {
						// If auth failed, retry with fallback key
						case syscode.CodeConnectAuthFailed:
							if auth.fallback {
								return fmt.Errorf("failed to authenticate with fallback key, exiting")
							}

							signingKeyFallback := h.h.GetSigningKeyFallback()
							if signingKeyFallback != "" {
								auth = authContext{signingKey: signingKeyFallback, fallback: true}
							}

							initiateConnectionChan <- struct{}{}

							continue

						// Retry on the following error codes
						case syscode.CodeConnectGatewayClosing, syscode.CodeConnectInternal, syscode.CodeConnectWorkerHelloTimeout:
							initiateConnectionChan <- struct{}{}

							continue

						default:
							// If we received a reason  that's non-retriable, stop here.
							return fmt.Errorf("connect failed with error code %q", closeErr.Reason)
						}
					}
				}

				initiateConnectionChan <- struct{}{}
			case <-initiateConnectionChan:
			}

			if attempts == 5 {
				return fmt.Errorf("could not establish connection after 5 attempts")
			}

			attempts++

			h.connect(ctx, false, connectionEstablishData{
				signingKey:            auth.signingKey,
				numCpuCores:           int32(numCpuCores),
				totalMem:              int64(totalMem),
				marshaledFns:          marshaledFns,
				marshaledCapabilities: marshaledCapabilities,
			}, notifyConnectedChan, notifyConnectDoneChan)
		}
	})

	initiateConnectionChan <- struct{}{}

	if err := eg.Wait(); err != nil {
		return fmt.Errorf("could not establish connection: %w", err)
	}

	// Send out buffered messages, using new connection if necessary!
	h.messageBufferLock.Lock()
	defer h.messageBufferLock.Unlock()
	if len(h.messageBuffer) > 0 {
		//  Send buffered messages via a working connection
		err = h.withTemporaryConnection(connectionEstablishData{
			signingKey:            auth.signingKey,
			numCpuCores:           int32(numCpuCores),
			totalMem:              int64(totalMem),
			marshaledFns:          marshaledFns,
			marshaledCapabilities: marshaledCapabilities,
		}, func(ws *websocket.Conn) error {
			// Send buffered messages
			err := h.sendBufferedMessages(ws)
			if err != nil {
				return fmt.Errorf("could not send buffered messages: %w", err)
			}

			return nil
		})
		if err != nil {
			h.h.Logger.Error("could not establish connection for sending buffered messages", "err", err)
		}

		// TODO Push remaining messages to another destination for processing?
	}

	return nil
}

type connectionEstablishData struct {
	signingKey            string
	numCpuCores           int32
	totalMem              int64
	marshaledFns          []byte
	marshaledCapabilities []byte
	manualReadinessAck    bool
}

type preparedConnection struct {
	ws          *websocket.Conn
	gatewayHost string
}

func (h *connectHandler) prepareConnection(ctx context.Context, allowResettingGateways bool, data connectionEstablishData) (preparedConnection, bool, error) {
	connectTimeout, cancelConnectTimeout := context.WithTimeout(ctx, 10*time.Second)
	defer cancelConnectTimeout()

	gatewayHost := h.hostsManager.pickAvailableGateway()
	if gatewayHost == "" {
		h.hostsManager.resetGateways()

		return preparedConnection{}, allowResettingGateways, fmt.Errorf("no available gateway hosts")
	}

	// Establish WebSocket connection to one of the gateways
	ws, _, err := websocket.Dial(connectTimeout, gatewayHost, &websocket.DialOptions{
		Subprotocols: []string{
			types.GatewaySubProtocol,
		},
	})
	if err != nil {
		h.hostsManager.markUnreachableGateway(gatewayHost)
		return preparedConnection{}, false, fmt.Errorf("could not connect to gateway: %w", err)
	}

	// Connection ID is unique per connection, reconnections should get a new ID
	h.connectionId = ulid.MustNew(ulid.Now(), rand.Reader)

	h.h.Logger.Debug("websocket connection established")

	// Wait for gateway hello message
	{
		initialMessageTimeout, cancelInitialTimeout := context.WithTimeout(ctx, 5*time.Second)
		defer cancelInitialTimeout()
		var helloMessage connectproto.ConnectMessage
		err = wsproto.Read(initialMessageTimeout, ws, &helloMessage)
		if err != nil {
			h.hostsManager.markUnreachableGateway(gatewayHost)
			return preparedConnection{}, true, fmt.Errorf("did not receive gateway hello message: %w", err)
		}

		if helloMessage.Kind != connectproto.GatewayMessageType_GATEWAY_HELLO {
			h.hostsManager.markUnreachableGateway(gatewayHost)
			return preparedConnection{}, true, fmt.Errorf("expected gateway hello message, got %s", helloMessage.Kind)
		}

		h.h.Logger.Debug("received gateway hello message")
	}

	// Send connect message
	{
		hashedKey, err := hashedSigningKey([]byte(data.signingKey))
		if err != nil {
			return preparedConnection{}, false, fmt.Errorf("could not hash signing key: %w", err)
		}

		apiOrigin := h.h.GetAPIBaseURL()
		if h.h.isDev() {
			apiOrigin = DevServerURL()
		}

		data, err := proto.Marshal(&connectproto.WorkerConnectRequestData{
			SessionId: &connectproto.SessionIdentifier{
				BuildId:      h.h.BuildId,
				InstanceId:   h.instanceId(),
				ConnectionId: h.connectionId.String(),
			},
			AuthData: &connectproto.AuthData{
				HashedSigningKey: hashedKey,
			},
			AppName: h.h.appName,
			Config: &connectproto.ConfigDetails{
				Capabilities: data.marshaledCapabilities,
				Functions:    data.marshaledFns,
				ApiOrigin:    apiOrigin,
			},
			SystemAttributes: &connectproto.SystemAttributes{
				CpuCores: data.numCpuCores,
				MemBytes: data.totalMem,
				Os:       runtime.GOOS,
			},
			Environment:              h.h.Env,
			Platform:                 Ptr(platform()),
			SdkVersion:               SDKVersion,
			SdkLanguage:              SDKLanguage,
			WorkerManualReadinessAck: data.manualReadinessAck,
		})
		if err != nil {
			return preparedConnection{}, false, fmt.Errorf("could not serialize sdk connect message: %w", err)
		}

		err = wsproto.Write(ctx, ws, &connectproto.ConnectMessage{
			Kind:    connectproto.GatewayMessageType_WORKER_CONNECT,
			Payload: data,
		})
		if err != nil {
			return preparedConnection{}, true, fmt.Errorf("could not send initial message")
		}
	}

	// Wait for gateway ready message
	{
		connectionReadyTimeout, cancelConnectionReadyTimeout := context.WithTimeout(ctx, 20*time.Second)
		defer cancelConnectionReadyTimeout()
		var connectionReadyMsg connectproto.ConnectMessage
		err = wsproto.Read(connectionReadyTimeout, ws, &connectionReadyMsg)
		if err != nil {
			return preparedConnection{}, true, fmt.Errorf("did not receive gateway connection ready message: %w", err)
		}

		if connectionReadyMsg.Kind != connectproto.GatewayMessageType_GATEWAY_CONNECTION_READY {
			return preparedConnection{}, true, fmt.Errorf("expected gateway connection ready message, got %s", connectionReadyMsg.Kind)
		}

		h.h.Logger.Debug("received gateway connection ready message")
	}

	return preparedConnection{ws, gatewayHost}, false, nil
}

func (h *connectHandler) sendBufferedMessages(ws *websocket.Conn) error {
	processed := 0
	for _, msg := range h.messageBuffer {
		// always send the message, even if the context is cancelled
		err := wsproto.Write(context.Background(), ws, msg)
		if err != nil {
			// Only send buffered messages once
			h.messageBuffer = h.messageBuffer[processed:]

			h.h.Logger.Error("failed to send buffered message", "err", err)
			return fmt.Errorf("could not send buffered message: %w", err)
		}

		h.h.Logger.Debug("sent buffered message", "msg", msg)
		processed++
	}
	h.messageBuffer = nil
	return nil
}

var errGatewayDraining = errors.New("gateway is draining")

func (h *connectHandler) handleConnection(ctx context.Context, data connectionEstablishData, ws *websocket.Conn, gatewayHost string, notifyConnectedChan chan struct{}, notifyConnectDoneChan chan connectReport) (reconnect bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer func() {
		// TODO Do we need to include a reason here? If we only use this for unexpected disconnects, probably not
		_ = ws.CloseNow()
	}()

	go func() {
		<-ctx.Done()
		_ = ws.Close(websocket.StatusNormalClosure, connectproto.WorkerDisconnectReason_WORKER_SHUTDOWN.String())
	}()

	// Send buffered but unsent messages if connection was re-established
	if len(h.messageBuffer) > 0 {
		h.h.Logger.Debug("sending buffered messages", "count", len(h.messageBuffer))
		err = h.sendBufferedMessages(ws)
		if err != nil {
			return true, fmt.Errorf("could not send buffered messages: %w", err)
		}
	}

	eg := errgroup.Group{}
	eg.Go(func() error {
		for {
			var msg connectproto.ConnectMessage
			err = wsproto.Read(context.Background(), ws, &msg)
			if err != nil {
				h.h.Logger.Error("failed to read message", "err", err)

				// The connection may still be active, but for some reason we couldn't read the message
				return err
			}

			h.h.Logger.Debug("received gateway request", "msg", &msg)

			switch msg.Kind {
			case connectproto.GatewayMessageType_GATEWAY_CLOSING:
				// Stop the read loop: We will not receive any further messages and should establish a new connection
				// We can still use the old connection to send replies to the gateway
				return errGatewayDraining
			case connectproto.GatewayMessageType_GATEWAY_EXECUTOR_REQUEST:
				// Handle invoke in a non-blocking way to allow for other messages to be processed
				h.inProgress.Add(1)
				h.workerPoolMsgs <- workerPoolMsg{
					msg: &msg,
					ws:  ws,
				}
			default:
				h.h.Logger.Error("got unknown gateway request", "err", err)
				continue
			}
		}
	})

	h.h.Logger.Debug("waiting for read loop to end")

	// If read loop ends, this can be for two reasons
	// - Connection loss (io.EOF), read loop terminated intentionally (CloseError), other error (unexpected)
	// - Worker shutdown, parent context got cancelled
	if err := eg.Wait(); err != nil && ctx.Err() == nil {
		if errors.Is(err, errGatewayDraining) {
			h.hostsManager.markDrainingGateway(gatewayHost)

			// Gateway is draining and will not accept new connections.
			// We must reconnect to a different gateway, only then can we close the old connection.
			waitUntilConnected, doneWaiting := context.WithCancel(context.Background())
			defer doneWaiting()

			// Intercept connected signal and pass it to the main goroutine
			notifyConnectedInterceptChan := make(chan struct{})
			go func() {
				<-notifyConnectedChan
				notifyConnectedInterceptChan <- struct{}{}
				doneWaiting()
			}()

			// Establish new connection and pass close reports back to the main goroutine
			h.connect(ctx, true, data, notifyConnectedInterceptChan, notifyConnectDoneChan)

			// Wait until the new connection is established before closing the old one
			<-waitUntilConnected.Done()

			// By returning, we will close the old connection
			return false, errGatewayDraining
		}

		h.h.Logger.Debug("read loop ended with error", "err", err)

		// In case the gateway intentionally closed the connection, we'll receive a close error
		cerr := websocket.CloseError{}
		if errors.As(err, &cerr) {
			h.h.Logger.Error("connection closed with reason", "reason", cerr.Reason)

			// Reconnect!
			return true, fmt.Errorf("connection closed with reason %q: %w", cerr.Reason, cerr)
		}

		// connection closed without reason
		if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
			h.h.Logger.Error("failed to read message from gateway, lost connection unexpectedly", "err", err)
			return true, fmt.Errorf("connection closed unexpectedly: %w", cerr)
		}

		// If this is not a worker shutdown, we should reconnect
		return true, fmt.Errorf("connection closed unexpectedly: %w", ctx.Err())
	}

	// Perform graceful shutdown routine (context was cancelled)

	// Signal gateway that we won't process additional messages!
	{
		h.h.Logger.Debug("sending worker pause message")
		err := wsproto.Write(context.Background(), ws, &connectproto.ConnectMessage{
			Kind: connectproto.GatewayMessageType_WORKER_PAUSE,
		})
		if err != nil {
			// We should not exit here, as we're already in the shutdown routine
			h.h.Logger.Error("failed to serialize worker pause msg", "err", err)
		}
	}

	h.h.Logger.Debug("waiting for in-progress requests to finish")

	// Wait until all in-progress requests are completed
	h.inProgress.Wait()

	// Attempt to shut down connection if not already done
	_ = ws.Close(websocket.StatusNormalClosure, connectproto.WorkerDisconnectReason_WORKER_SHUTDOWN.String())

	return false, nil
}

type connectReport struct {
	reconnect bool
	err       error
}

func (h *connectHandler) connect(ctx context.Context, allowResettingGateways bool, data connectionEstablishData, notifyConnectedChan chan struct{}, notifyConnectDoneChan chan connectReport) {
	preparedConn, reconnect, err := h.prepareConnection(ctx, allowResettingGateways, data)
	if err != nil {
		notifyConnectDoneChan <- connectReport{
			reconnect: reconnect,
			err:       fmt.Errorf("could not establish connection: %w", err),
		}
		return
	}

	notifyConnectedChan <- struct{}{}

	reconnect, err = h.handleConnection(ctx, data, preparedConn.ws, preparedConn.gatewayHost, notifyConnectedChan, notifyConnectDoneChan)
	if err != nil {
		if errors.Is(err, errGatewayDraining) {
			// if the gateway is draining, the original connection was closed, and we already reconnected inside handleConnection
			return
		}

		notifyConnectDoneChan <- connectReport{
			reconnect: reconnect,
			err:       fmt.Errorf("could not handle connection: %w", err),
		}
		return
	}

	notifyConnectDoneChan <- connectReport{
		reconnect: reconnect,
		err:       nil,
	}
}

func (h *connectHandler) withTemporaryConnection(data connectionEstablishData, handler func(ws *websocket.Conn) error) error {
	// Prevent this connection from receiving work
	data.manualReadinessAck = true

	maxAttempts := 4

	var conn *websocket.Conn
	var attempts int
	for {
		if attempts == maxAttempts {
			return fmt.Errorf("could not establish connection after %d attempts", maxAttempts)
		}

		ws, _, err := h.prepareConnection(context.Background(), true, data)
		if err != nil {
			attempts++
			continue
		}

		conn = ws.ws
		break
	}

	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, connectproto.WorkerDisconnectReason_WORKER_SHUTDOWN.String())
	}()

	err := handler(conn)
	if err != nil {
		return err
	}

	return nil
}

func (h *connectHandler) processExecutorRequest(ws *websocket.Conn, msg *connectproto.ConnectMessage) {
	defer h.inProgress.Done()

	// Always make sure the invoke finishes properly
	processCtx := context.Background()

	err := h.handleInvokeMessage(processCtx, ws, msg)

	// When we encounter an error, we cannot retry the connection from inside the goroutine.
	// If we're dealing with connection loss, the next read loop will fail with the same error
	// and handle the reconnection.
	if err != nil {
		cerr := websocket.CloseError{}
		if errors.As(err, &cerr) {
			h.h.Logger.Error("gateway connection closed with reason", "reason", cerr.Reason)
			return
		}

		if errors.Is(err, io.EOF) {
			h.h.Logger.Error("gateway connection closed unexpectedly", "err", err)
			return
		}

		// TODO If error is not connection-related, should we retry? Send the buffered message?
	}
}

func (h *connectHandler) handleInvokeMessage(ctx context.Context, ws *websocket.Conn, msg *connectproto.ConnectMessage) error {
	resp, err := h.connectInvoke(ctx, ws, msg)
	if err != nil {
		h.h.Logger.Error("failed to handle sdk request", "err", err)
		// TODO Should we drop the connection? Continue receiving messages?
		return fmt.Errorf("could not handle sdk request: %w", err)
	}

	data, err := proto.Marshal(resp)
	if err != nil {
		h.h.Logger.Error("failed to serialize sdk response", "err", err)
		// TODO This should never happen; Signal that we should retry
		return fmt.Errorf("could not serialize sdk response: %w", err)
	}

	responseMessage := &connectproto.ConnectMessage{
		Kind:    connectproto.GatewayMessageType_WORKER_REPLY,
		Payload: data,
	}

	err = wsproto.Write(ctx, ws, responseMessage)
	if err != nil {
		h.h.Logger.Error("failed to send sdk response", "err", err)

		// Buffer message to retry
		h.messageBufferLock.Lock()
		h.messageBuffer = append(h.messageBuffer, responseMessage)
		h.messageBufferLock.Unlock()

		return fmt.Errorf("could not send sdk response: %w", err)
	}

	return nil
}

// connectInvoke is the counterpart to invoke for connect
func (h *connectHandler) connectInvoke(ctx context.Context, ws *websocket.Conn, msg *connectproto.ConnectMessage) (*connectproto.SDKResponse, error) {
	body := connectproto.GatewayExecutorRequestData{}
	if err := proto.Unmarshal(msg.Payload, &body); err != nil {
		// TODO Should we send this back to the gateway?
		h.h.Logger.Error("error decoding gateway request data", "error", err)
		return nil, fmt.Errorf("invalid gateway message data: %w", err)
	}

	// Note: This still uses JSON
	// TODO Replace with Protobuf
	var request sdkrequest.Request
	if err := json.Unmarshal(body.RequestPayload, &request); err != nil {
		// TODO Should we send this back to the gateway? Previously this was a status code 400 public error with "malformed input"
		h.h.Logger.Error("error decoding sdk request", "error", err)
		return nil, fmt.Errorf("invalid SDK request payload: %w", err)
	}

	ackPayload, err := proto.Marshal(&connectproto.WorkerRequestAckData{
		RequestId:    body.RequestId,
		AppId:        body.AppId,
		FunctionSlug: body.FunctionSlug,
		StepId:       body.StepId,
	})
	if err != nil {
		h.h.Logger.Error("error marshaling request ack", "error", err)
		return nil, publicerr.Error{
			Message: "malformed input",
			Status:  400,
		}
	}

	// Ack message
	// If we're shutting down (context is canceled) we will not ack, which is desired!
	if err := wsproto.Write(ctx, ws, &connectproto.ConnectMessage{
		Kind:    connectproto.GatewayMessageType_WORKER_REQUEST_ACK,
		Payload: ackPayload,
	}); err != nil {
		h.h.Logger.Error("error sending request ack", "error", err)
		return nil, publicerr.Error{
			Message: "failed to ack worker request",
			Status:  400,
		}
	}

	// TODO Should we wait for a gateway response before starting to process? What if the gateway fails acking and we start too early?
	// This should not happen but could lead to double processing of the same message

	if request.UseAPI {
		// TODO: implement this
		// retrieve data from API
		// request.Steps =
		// request.Events =
		_ = 0 // no-op to avoid linter error
	}

	h.h.l.RLock()
	var fn ServableFunction
	for _, f := range h.h.funcs {
		if f.Slug() == body.FunctionSlug {
			fn = f
			break
		}
	}
	h.h.l.RUnlock()

	if fn == nil {
		// XXX: This is a 500 within the JS SDK.  We should probably change
		// the JS SDK's status code to 410.  404 indicates that the overall
		// API for serving Inngest isn't found.
		return nil, publicerr.Error{
			Message: fmt.Sprintf("function not found: %s", body.FunctionSlug),
			Status:  410,
		}
	}

	var stepId *string
	if body.StepId != nil && *body.StepId != "step" {
		stepId = body.StepId
	}

	// Invoke function, always complete regardless of
	resp, ops, err := invoke(context.Background(), fn, &request, stepId)

	// NOTE: When triggering step errors, we should have an OpcodeStepError
	// within ops alongside an error.  We can safely ignore that error, as it's
	// only used for checking whether the step used a NoRetryError or RetryAtError
	//
	// For that reason, we check those values first.
	noRetry := sdkerrors.IsNoRetryError(err)
	retryAt := sdkerrors.GetRetryAtTime(err)
	if len(ops) == 1 && ops[0].Op == enums.OpcodeStepError {
		// Now we've handled error types we can ignore step
		// errors safely.
		err = nil
	}

	// Now that we've handled the OpcodeStepError, if we *still* ahve
	// a StepError kind returned from a function we must have an unhandled
	// step error.  This is a NonRetryableError, as the most likely code is:
	//
	// 	_, err := step.Run(ctx, func() (any, error) { return fmt.Errorf("") })
	// 	if err != nil {
	// 	     return err
	// 	}
	if sdkerrors.IsStepError(err) {
		err = fmt.Errorf("Unhandled step error: %s", err)
		noRetry = true
	}

	// These may be added even for 2xx codes with step errors.
	var retryAfterVal *string
	if retryAt != nil {
		retryAfterVal = StrPtr(retryAt.Format(time.RFC3339))
	}

	if err != nil {
		h.h.Logger.Error("error calling function", "error", err)
		return &connectproto.SDKResponse{
			RequestId:  body.RequestId,
			Status:     connectproto.SDKResponseStatus_ERROR,
			Body:       []byte(fmt.Sprintf("error calling function: %s", err.Error())),
			NoRetry:    noRetry,
			RetryAfter: retryAfterVal,
		}, nil
	}

	if len(ops) > 0 {
		// Note: This still uses JSON
		// TODO Replace with Protobuf
		serializedOps, err := json.Marshal(ops)
		if err != nil {
			return nil, fmt.Errorf("could not serialize ops: %w", err)
		}

		// Return the function opcode returned here so that we can re-invoke this
		// function and manage state appropriately.  Any opcode here takes precedence
		// over function return values as the function has not yet finished.
		return &connectproto.SDKResponse{
			RequestId:  body.RequestId,
			Status:     connectproto.SDKResponseStatus_NOT_COMPLETED,
			Body:       serializedOps,
			NoRetry:    noRetry,
			RetryAfter: retryAfterVal,
		}, nil
	}

	// Note: This still uses JSON
	// TODO Replace with Protobuf
	serializedResp, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("could not serialize resp: %w", err)
	}

	// Return the function response.
	return &connectproto.SDKResponse{
		RequestId:  body.RequestId,
		Status:     connectproto.SDKResponseStatus_DONE,
		Body:       serializedResp,
		NoRetry:    noRetry,
		RetryAfter: retryAfterVal,
	}, nil
}
