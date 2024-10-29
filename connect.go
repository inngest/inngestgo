package inngestgo

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"strings"
	"time"
)

const GatewaySubProtocol = "v0.connect.inngest.com"

type gatewayMessageType string

const gatewayMessageTypeHello gatewayMessageType = "gateway-hello"
const gatewayMessageTypeSDKConnect gatewayMessageType = "sdk-connect"

const gatewayMessageTypeExecutorRequest gatewayMessageType = "executor-request"
const gatewayMessageTypeSDKReply gatewayMessageType = "sdk-reply"

type gatewayMessage struct {
	Kind gatewayMessageType `json:"kind"`
	Data json.RawMessage    `json:"data"`
}

func (h *handler) connectURL() string {
	connectURL := defaultConnectOrigin
	if h.isDev() {
		connectURL = fmt.Sprintf("%s/connect", strings.Replace(DevServerURL(), "http", "ws", 1))
	}
	if h.RegisterURL != nil {
		connectURL = *h.ConnectURL
	}

	return connectURL
}

func (h *handler) Connect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, h.connectURL(), &websocket.DialOptions{
		Subprotocols: []string{
			GatewaySubProtocol,
		},
	})
	if err != nil {
		return fmt.Errorf("could not establish outbound connection: %w", err)
	}
	defer ws.CloseNow()

	{
		initialMessageTimeout, cancelInitialTimeout := context.WithTimeout(ctx, 5*time.Second)
		defer cancelInitialTimeout()
		var helloMessage gatewayMessage
		err = wsjson.Read(initialMessageTimeout, ws, &helloMessage)
		if err != nil {
			return fmt.Errorf("did not receive gateway hello message: %w", err)
		}

		if helloMessage.Kind != gatewayMessageTypeHello {
			return fmt.Errorf("expected gateway hello message, got %s", helloMessage.Kind)
		}
	}

	err = wsjson.Write(ctx, ws, gatewayMessage{
		Kind: gatewayMessageTypeSDKConnect,
		Data: nil,
	})
	if err != nil {
		return fmt.Errorf("could not send initial message")
	}

	<-time.After(time.Second * 10)

	err = wsjson.Write(ctx, ws, gatewayMessage{
		Kind: gatewayMessageTypeSDKReply,
		Data: nil,
	})
	if err != nil {
		return fmt.Errorf("could not send test followup message")
	}

	ws.Close(websocket.StatusNormalClosure, "")

	return nil
}
