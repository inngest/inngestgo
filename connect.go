package inngestgo

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inngest/inngest/pkg/connect/types"
	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/publicerr"
	"github.com/inngest/inngest/pkg/sdk"
	sdkerrors "github.com/inngest/inngestgo/errors"

	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/oklog/ulid/v2"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type connectHandler struct {
	h *handler

	connectionId ulid.ULID
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

func (h *connectHandler) connectToGateway(ctx context.Context) (*websocket.Conn, error) {
	hosts := h.connectURLs()
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no connect URLs provided")
	}

	for _, gatewayHost := range hosts {
		h.h.Logger.Debug("attempting to connect", "host", gatewayHost)

		// Establish WebSocket connection to one of the gateways
		ws, _, err := websocket.Dial(ctx, gatewayHost, &websocket.DialOptions{
			Subprotocols: []string{
				types.GatewaySubProtocol,
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
	h.useConnect = true
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	signingKey := h.h.GetSigningKey()
	if signingKey == "" {
		return fmt.Errorf("must provide signing key")
	}

	var functionHash []byte
	{
		fns, err := createFunctionConfigs(h.h.appName, h.h.funcs, url.URL{}, true)
		if err != nil {
			return fmt.Errorf("error creating function configs: %w", err)
		}

		serialized, err := json.Marshal(fns)
		if err != nil {
			return fmt.Errorf("failed to serialize functions: %w", err)
		}

		res := sha256.Sum256(serialized)
		functionHash = res[:]
	}

	connectTimeout, cancelConnectTimeout := context.WithTimeout(ctx, 10*time.Second)
	defer cancelConnectTimeout()

	ws, err := h.connectToGateway(connectTimeout)
	if err != nil {
		return fmt.Errorf("could not connect: %w", err)
	}
	defer func() {
		_ = ws.CloseNow()
	}()

	h.connectionId = ulid.MustNew(ulid.Now(), rand.Reader)

	h.h.Logger.Debug("connection established")

	// Wait for gateway hello message
	{
		initialMessageTimeout, cancelInitialTimeout := context.WithTimeout(ctx, 5*time.Second)
		defer cancelInitialTimeout()
		var helloMessage types.GatewayMessage
		err = wsjson.Read(initialMessageTimeout, ws, &helloMessage)
		if err != nil {
			return fmt.Errorf("did not receive gateway hello message: %w", err)
		}

		if helloMessage.Kind != types.GatewayMessageTypeHello {
			return fmt.Errorf("expected gateway hello message, got %s", helloMessage.Kind)
		}

		h.h.Logger.Debug("received gateway hello message")
	}

	// Send connect message
	{
		hashedKey, err := hashedSigningKey([]byte(signingKey))
		if err != nil {
			return fmt.Errorf("could not hash signing key: %w", err)
		}

		data, err := json.Marshal(types.GatewayMessageTypeSDKConnectData{
			Authz: types.AuthData{
				HashedSigningKey: hashedKey,
			},
			AppName: h.h.appName,
			Env:     h.h.Env,
			Session: types.SessionDetails{
				FunctionHash: functionHash,
				BuildID:      h.h.BuildId,
				InstanceId:   h.instanceId(),
				ConnectionId: h.connectionId.String(),
			},
			SDKAuthor:   SDKAuthor,
			SDKLanguage: SDKLanguage,
			SDKVersion:  SDKVersion,
		})
		if err != nil {
			return fmt.Errorf("could not serialize sdk connect message: %w", err)
		}

		err = wsjson.Write(ctx, ws, types.GatewayMessage{
			Kind: types.GatewayMessageTypeSDKConnect,
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

		var msg types.GatewayMessage
		err = wsjson.Read(ctx, ws, &msg)
		if err != nil {
			// TODO Handle issues reading message: Should we re-establish the connection?
			return err
		}

		h.h.Logger.Debug("received gateway request", "msg", msg)

		switch msg.Kind {
		case types.GatewayMessageTypeSync:
			var data types.GatewayMessageTypeSyncData
			if err := json.Unmarshal(msg.Data, &data); err != nil {
				h.h.Logger.Error("error decoding sync message", "error", err)
				return err
			}

			err := h.h.connectSync(data.DeployId)
			if err != nil {
				h.h.Logger.Error("error syncing", "error", err)
				// TODO Should we drop the connection? Continue receiving messages?
				return err
			}
		case types.GatewayMessageTypeExecutorRequest:
			resp, err := h.connectInvoke(ctx, msg)
			if err != nil {
				h.h.Logger.Error("failed to handle sdk request", "err", err)
				// TODO Should we drop the connection? Continue receiving messages?
				return err
			}

			data, err := json.Marshal(resp)
			if err != nil {
				h.h.Logger.Error("failed to serialize sdk response", "err", err)
				// TODO This should never happen; Signal that we should retry
				continue
			}

			err = wsjson.Write(ctx, ws, types.GatewayMessage{
				Kind: types.GatewayMessageTypeSDKReply,
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

	// TODO Perform graceful shutdown routine
	_ = ws.Close(websocket.StatusNormalClosure, "")

	return nil
}

// connectInvoke is the counterpart to invoke for connect
func (h *connectHandler) connectInvoke(ctx context.Context, msg types.GatewayMessage) (*types.SdkResponse, error) {
	body := &types.GatewayMessageTypeExecutorRequestData{}
	if err := json.Unmarshal(msg.Data, body); err != nil {
		h.h.Logger.Error("error decoding gateway request data", "error", err)
		return nil, fmt.Errorf("invalid gateway message data: %w", err)
	}

	var request sdkrequest.Request
	if err := json.Unmarshal(body.RequestBytes, &request); err != nil {
		h.h.Logger.Error("error decoding sdk request", "error", err)
		return nil, publicerr.Error{
			Message: "malformed input",
			Status:  400,
		}
	}

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

	resp, ops, err := invoke(ctx, fn, &request, stepId)

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
	noRetryVal := ""
	if noRetry {
		noRetryVal = "true"
	}
	retryAfterVal := ""
	if retryAt != nil {
		retryAfterVal = retryAt.Format(time.RFC3339)
	}

	if err != nil {
		h.h.Logger.Error("error calling function", "error", err)
		return &types.SdkResponse{
			RequestId:  body.RequestId,
			Status:     types.SdkResponseStatusError,
			Body:       []byte(fmt.Sprintf("error calling function: %s", err.Error())),
			NoRetry:    noRetryVal,
			RetryAfter: retryAfterVal,
		}, nil
	}

	if len(ops) > 0 {
		serializedOps, err := json.Marshal(ops)
		if err != nil {
			return nil, fmt.Errorf("could not serialize ops: %w", err)
		}

		// Return the function opcode returned here so that we can re-invoke this
		// function and manage state appropriately.  Any opcode here takes precedence
		// over function return values as the function has not yet finished.
		return &types.SdkResponse{
			RequestId:  body.RequestId,
			Status:     types.SdkResponseStatusNotCompleted,
			Body:       serializedOps,
			NoRetry:    noRetryVal,
			RetryAfter: retryAfterVal,
		}, nil
	}

	serializedResp, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("could not serialize resp: %w", err)
	}

	// Return the function response.
	return &types.SdkResponse{
		RequestId:  body.RequestId,
		Status:     types.SdkResponseStatusDone,
		Body:       serializedResp,
		NoRetry:    noRetryVal,
		RetryAfter: retryAfterVal,
	}, nil
}

func (h *handler) connectSync(deployId *string) error {
	connectPlaceholder := url.URL{
		Scheme: "ws",
		Host:   "connect",
	}

	config := sdk.RegisterRequest{
		V:          "1",
		URL:        connectPlaceholder.String(),
		DeployType: "ping",
		SDK:        HeaderValueSDK,
		AppName:    h.appName,
		Headers: sdk.Headers{
			Env:      h.GetEnv(),
			Platform: platform(),
		},
		Capabilities: capabilities,
		UseConnect:   h.useConnect,
	}

	fns, err := createFunctionConfigs(h.appName, h.funcs, connectPlaceholder, true)
	if err != nil {
		return fmt.Errorf("error creating function configs: %w", err)
	}
	config.Functions = fns

	registerURL := fmt.Sprintf("%s/fn/register", defaultAPIOrigin)
	if h.isDev() {
		// TODO: Check if dev server is up.  If not, error.  We can't deploy to production.
		registerURL = fmt.Sprintf("%s/fn/register", DevServerURL())
	}
	if h.RegisterURL != nil {
		registerURL = *h.RegisterURL
	}

	createRequest := func() (*http.Request, error) {
		byt, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("error marshalling function config: %w", err)
		}

		req, err := http.NewRequest(http.MethodPost, registerURL, bytes.NewReader(byt))
		if err != nil {
			return nil, fmt.Errorf("error creating new request: %w", err)
		}
		if deployId != nil {
			qp := req.URL.Query()
			qp.Set("deployId", *deployId)
			req.URL.RawQuery = qp.Encode()
		}

		if h.GetEnv() != "" {
			req.Header.Add(HeaderKeyEnv, h.GetEnv())
		}

		SetBasicRequestHeaders(req)

		return req, nil
	}

	resp, err := fetchWithAuthFallback(
		createRequest,
		h.GetSigningKey(),
		h.GetSigningKeyFallback(),
	)
	if err != nil {
		fmt.Printf("error performing connect registration request: %s\n", err.Error())
		return fmt.Errorf("error performing connect registration request: %w", err)
	}
	if resp.StatusCode > 299 {
		body := map[string]any{}
		byt, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(byt, &body); err != nil {
			return fmt.Errorf("error reading register response: %w\n\n%s", err, byt)
		}
		fmt.Printf("error registering fns: %s\n", body["error"])
		return fmt.Errorf("Error registering functions: %s", body["error"])
	}

	return nil
}
