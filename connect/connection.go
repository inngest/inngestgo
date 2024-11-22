package connect

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/inngest/inngest/pkg/connect/types"
	"github.com/inngest/inngest/pkg/connect/wsproto"
	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/oklog/ulid/v2"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"runtime"
	"time"
)

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

type connectionEstablishData struct {
	hashedSigningKey      []byte
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

	h.logger.Debug("websocket connection established")

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

		h.logger.Debug("received gateway hello message")
	}

	// Send connect message
	{

		apiOrigin := h.opts.APIBaseUrl
		if h.opts.IsDev {
			apiOrigin = h.opts.DevServerUrl
		}

		data, err := proto.Marshal(&connectproto.WorkerConnectRequestData{
			SessionId: &connectproto.SessionIdentifier{
				BuildId:      h.opts.BuildId,
				InstanceId:   h.instanceId(),
				ConnectionId: h.connectionId.String(),
			},
			AuthData: &connectproto.AuthData{
				HashedSigningKey: data.hashedSigningKey,
			},
			AppName: h.opts.AppName,
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
			Environment:              h.opts.Env,
			Platform:                 h.opts.Platform,
			SdkVersion:               h.opts.SDKVersion,
			SdkLanguage:              h.opts.SDKLanguage,
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

		h.logger.Debug("received gateway connection ready message")
	}

	return preparedConnection{ws, gatewayHost}, false, nil
}

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
		h.logger.Debug("sending buffered messages", "count", len(h.messageBuffer))
		err = h.sendBufferedMessages(ws)
		if err != nil {
			return true, fmt.Errorf("could not send buffered messages: %w", err)
		}
	}

	go func() {
		heartbeatTicker := time.NewTicker(WorkerHeartbeatInterval)
		defer heartbeatTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeatTicker.C:
				err := wsproto.Write(ctx, ws, &connectproto.ConnectMessage{
					Kind: connectproto.GatewayMessageType_WORKER_HEARTBEAT,
				})
				if err != nil {
					h.logger.Error("failed to send worker heartbeat", "err", err)
				}
			}

		}
	}()

	eg := errgroup.Group{}
	eg.Go(func() error {
		for {
			var msg connectproto.ConnectMessage
			err = wsproto.Read(context.Background(), ws, &msg)
			if err != nil {
				h.logger.Error("failed to read message", "err", err)

				// The connection may still be active, but for some reason we couldn't read the message
				return err
			}

			h.logger.Debug("received gateway request", "msg", &msg)

			switch msg.Kind {
			case connectproto.GatewayMessageType_GATEWAY_CLOSING:
				// Stop the read loop: We will not receive any further messages and should establish a new connection
				// We can still use the old connection to send replies to the gateway
				return errGatewayDraining
			case connectproto.GatewayMessageType_GATEWAY_EXECUTOR_REQUEST:
				// Handle invoke in a non-blocking way to allow for other messages to be processed
				h.workerPool.Add(workerPoolMsg{
					msg: &msg,
					ws:  ws,
				})
			default:
				h.logger.Error("got unknown gateway request", "err", err)
				continue
			}
		}
	})

	h.logger.Debug("waiting for read loop to end")

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

		h.logger.Debug("read loop ended with error", "err", err)

		// In case the gateway intentionally closed the connection, we'll receive a close error
		cerr := websocket.CloseError{}
		if errors.As(err, &cerr) {
			h.logger.Error("connection closed with reason", "reason", cerr.Reason)

			// Reconnect!
			return true, fmt.Errorf("connection closed with reason %q: %w", cerr.Reason, cerr)
		}

		// connection closed without reason
		if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
			h.logger.Error("failed to read message from gateway, lost connection unexpectedly", "err", err)
			return true, fmt.Errorf("connection closed unexpectedly: %w", cerr)
		}

		// If this is not a worker shutdown, we should reconnect
		return true, fmt.Errorf("connection closed unexpectedly: %w", ctx.Err())
	}

	// Perform graceful shutdown routine (context was cancelled)

	// Signal gateway that we won't process additional messages!
	{
		h.logger.Debug("sending worker pause message")
		err := wsproto.Write(context.Background(), ws, &connectproto.ConnectMessage{
			Kind: connectproto.GatewayMessageType_WORKER_PAUSE,
		})
		if err != nil {
			// We should not exit here, as we're already in the shutdown routine
			h.logger.Error("failed to serialize worker pause msg", "err", err)
		}
	}

	h.logger.Debug("waiting for in-progress requests to finish")

	// Wait until all in-progress requests are completed
	h.workerPool.Wait()

	// Attempt to shut down connection if not already done
	_ = ws.Close(websocket.StatusNormalClosure, connectproto.WorkerDisconnectReason_WORKER_SHUTDOWN.String())

	return false, nil
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
