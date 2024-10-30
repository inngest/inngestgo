package inngestgo

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/execution/state"
	"github.com/inngest/inngest/pkg/publicerr"
	sdkerrors "github.com/inngest/inngestgo/errors"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/oklog/ulid/v2"
	"net/http"
	"os"
	"strings"
	"time"
)

const GatewaySubProtocol = "v0.connect.inngest.com"

type GatewayMessageType string

const GatewayMessageTypeHello GatewayMessageType = "gateway-hello"

const GatewayMessageTypeSDKConnect GatewayMessageType = "sdk-connect"

type AuthData struct {
	Challenge []byte `json:"challenge"`
	Signature []byte `json:"signature"`
}

type SessionDetails struct {
	// InstanceId represents the persistent identifier for this connection.
	// This must not change across the lifetime of the connection, including reconnects.
	InstanceId string `json:"instance_id"`

	// ConnectionId is the transient identifier for a concrete connection. This is different
	// from InstanceId as it is generated for each connection.
	// This is mainly used for debugging purposes.
	ConnectionId string `json:"connectionId"`
}

type GatewayMessageTypeSDKConnectData struct {
	Session SessionDetails `json:"session"`

	Authz AuthData `json:"authz"`

	AppName     string  `json:"app_name"`
	Env         *string `json:"env"`
	Framework   *string `json:"framework"`
	Platform    *string `json:"platform"`
	SDKAuthor   string  `json:"sdk_author"`
	SDKLanguage string  `json:"sdk_language"`
	SDKVersion  string  `json:"sdk_version"`
}

const GatewayMessageTypeExecutorRequest GatewayMessageType = "executor-request"

type GatewayMessageTypeExecutorRequestData struct {
	FunctionSlug string             `json:"fn_slug"`
	StepId       *string            `json:"step_id"`
	Request      sdkrequest.Request `json:"req"`
}

const GatewayMessageTypeSDKReply GatewayMessageType = "sdk-reply"

type SdkResponseStatus int

const (
	SdkResponseStatusNotCompleted SdkResponseStatus = http.StatusPartialContent
	SdkResponseStatusDone         SdkResponseStatus = http.StatusOK
	SdkResponseStatusError        SdkResponseStatus = http.StatusInternalServerError
)

type SdkResponse struct {
	Status SdkResponseStatus       `json:"status"`
	Ops    []state.GeneratorOpcode `json:"ops"`
	Resp   any                     `json:"resp"`
	Err    *string                 `json:"err"`
}

type GatewayMessage struct {
	Kind GatewayMessageType `json:"kind"`
	Data json.RawMessage    `json:"data"`
}

type connectHandler struct {
	h *handler

	connectionId ulid.ULID
}

func (h *connectHandler) connectURLs() []string {
	connectURLs := strings.Split(defaultConnectOrigins, ",")
	if h.h.isDev() {
		connectURLs = []string{fmt.Sprintf("%s/connect", strings.Replace(DevServerURL(), "http", "ws", 1))}
	}

	if len(h.h.ConnectURLs) > 0 {
		connectURLs = h.h.ConnectURLs
	}

	return connectURLs
}

func (h *connectHandler) connectToGateway(ctx context.Context) (*websocket.Conn, error) {
	hosts := h.connectURLs()

	for _, gatewayHost := range hosts {
		// Establish WebSocket connection to one of the gateways
		ws, _, err := websocket.Dial(ctx, gatewayHost, &websocket.DialOptions{
			Subprotocols: []string{
				GatewaySubProtocol,
			},
		})
		if err != nil {
			// try next connection
			continue
		}

		return ws, nil
	}

	return nil, fmt.Errorf("could not establish outbound connection: no available gateway host")
}

func (h *handler) Connect(ctx context.Context) error {
	ch := connectHandler{h: h}
	return ch.Connect(ctx)
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

func (h *connectHandler) Connect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	ws, err := h.connectToGateway(ctx)
	defer ws.CloseNow()

	h.connectionId = ulid.MustNew(ulid.Now(), rand.Reader)

	// Wait for gateway hello message
	{
		initialMessageTimeout, cancelInitialTimeout := context.WithTimeout(ctx, 5*time.Second)
		defer cancelInitialTimeout()
		var helloMessage GatewayMessage
		err = wsjson.Read(initialMessageTimeout, ws, &helloMessage)
		if err != nil {
			return fmt.Errorf("did not receive gateway hello message: %w", err)
		}

		if helloMessage.Kind != GatewayMessageTypeHello {
			return fmt.Errorf("expected gateway hello message, got %s", helloMessage.Kind)
		}
	}

	// Send connect message
	{
		data, err := json.Marshal(GatewayMessageTypeSDKConnectData{
			AppName: h.h.appName,
			Session: SessionDetails{
				InstanceId:   h.instanceId(),
				ConnectionId: h.connectionId.String(),
			},
			Authz:       AuthData{},
			Env:         nil,
			Framework:   nil,
			Platform:    nil,
			SDKAuthor:   "",
			SDKLanguage: "",
			SDKVersion:  "",
		})
		if err != nil {
			return fmt.Errorf("could not serialize sdk connect message: %w", err)
		}

		// TODO Include authz data, version data (SDK version, code build tag), instance identifier
		err = wsjson.Write(ctx, ws, GatewayMessage{
			Kind: GatewayMessageTypeSDKConnect,
			Data: data,
		})
		if err != nil {
			return fmt.Errorf("could not send initial message")
		}
	}

	for {
		if ctx.Err() != nil {
			break
		}

		var msg GatewayMessage
		err = wsjson.Read(ctx, ws, &msg)
		if err != nil {
			// TODO Handle issues reading message: Should we re-establish the connection?
			continue
		}

		h.h.Logger.Debug("received gateway request", "msg", msg)

		switch msg.Kind {
		case GatewayMessageTypeExecutorRequest:
			resp, err := h.connectInvoke(ctx, msg)
			if err != nil {
				h.h.Logger.Error("failed to handle sdk request", "err", err)
				// TODO Should we drop the connection? Continue receiving messages?
				continue
			}

			data, err := json.Marshal(resp)
			if err != nil {
				h.h.Logger.Error("failed to serialize sdk response", "err", err)
				// TODO This should never happen; Signal that we should retry
				continue
			}

			err = wsjson.Write(ctx, ws, GatewayMessage{
				Kind: GatewayMessageTypeSDKReply,
				Data: data,
			})
			if err != nil {
				h.h.Logger.Error("failed to send sdk response", "err", err)
				// TODO This should never happen; Signal that we should retry
				continue
			}
		default:
			h.h.Logger.Error("got unknown gateway request", "err", err)
			continue
		}
	}

	ws.Close(websocket.StatusNormalClosure, "")

	return nil
}

// connectInvoke is the counterpart to invoke for connect
func (h *connectHandler) connectInvoke(ctx context.Context, msg GatewayMessage) (*SdkResponse, error) {
	body := &GatewayMessageTypeExecutorRequestData{}
	if err := json.Unmarshal(msg.Data, body); err != nil {
		h.h.Logger.Error("error decoding sdk request", "error", err)
		return nil, publicerr.Error{
			Message: "malformed input",
			Status:  400,
		}
	}

	if body.Request.UseAPI {
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

	resp, ops, err := invoke(ctx, fn, &body.Request, stepId)

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
	if noRetry {
		// TODO Do we need to supply this?
		//w.Header().Add(HeaderKeyNoRetry, "true")
	}
	if retryAt != nil {
		// TODO Do we need to supply this?
		//w.Header().Add(HeaderKeyRetryAfter, retryAt.Format(time.RFC3339))
	}

	if err != nil {
		h.h.Logger.Error("error calling function", "error", err)
		// TODO Make sure this is properly surfaced in the executor!
		return &SdkResponse{
			Status: SdkResponseStatusError,
			Err:    StrPtr(fmt.Sprintf("error calling function: %s", err.Error())),
		}, nil
	}

	if len(ops) > 0 {
		// Return the function opcode returned here so that we can re-invoke this
		// function and manage state appropriately.  Any opcode here takes precedence
		// over function return values as the function has not yet finished.
		return &SdkResponse{
			Status: SdkResponseStatusNotCompleted,
			Ops:    ops,
		}, nil
	}

	// Return the function response.
	return &SdkResponse{
		Status: SdkResponseStatusDone,
		Resp:   resp,
	}, nil
}
