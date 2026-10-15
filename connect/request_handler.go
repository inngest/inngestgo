package connect

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	sdkerrors "github.com/inngest/inngestgo/errors"
	"github.com/inngest/inngestgo/internal/sdkrequest"
)

// RequestHandler serves the executor requests that arrive over the gateway
// connection.  set it with Opts.Handler to take over from the FunctionInvoker
// map passed to Connect.
type RequestHandler interface {
	// Admit decides if this worker takes the request.  the ACK tells the
	// gateway that this worker owns the request and must reply to it, so the
	// decision has to happen before the ACK and not inside Serve.  an error
	// drops the request with no ACK and no call to Serve or Done, which leaves
	// it free to run on another worker.  Admit runs on the connection's read
	// loop and must not block, because the same loop reads gateway heartbeats.
	Admit(req *connectproto.GatewayExecutorRequestData) error

	// Serve runs on a worker pool goroutine after the ACK.  the lease is
	// extended until Serve returns.  an error sends no reply, and the executor
	// retries the request after the lease expires.
	Serve(ctx context.Context, req *connectproto.GatewayExecutorRequestData) (Response, error)

	// Done runs once for each admitted request, after Connect writes or buffers the
	// reply, or after the request is dropped before the ACK and never reaches
	// Serve.
	//
	// this allows the handler to manage internal accounting for each ongoing request.
	//
	// note:  Done does not write or change the reply.  it runs on the worker pool
	// goroutine before that goroutine takes the next request, so it must not block.
	Done(req *connectproto.GatewayExecutorRequestData)
}

// Response is the reply to one executor request.  Connect copies the request
// IDs and trace contexts from the request into the SDKResponse.
type Response struct {
	Status connectproto.SDKResponseStatus
	Body   []byte

	// NoRetry and RetryAfter carry the X-Inngest-No-Retry and Retry-After
	// values that an HTTP serve endpoint sends as headers.
	NoRetry    bool
	RetryAfter *string

	// SDKVersion and RequestVersion carry the X-Inngest-SDK and
	// x-inngest-req-version values.  the executor uses them to pick retry
	// behavior and the execution protocol version.
	SDKVersion     string
	RequestVersion uint32
}

func newSDKResponse(req *connectproto.GatewayExecutorRequestData, resp Response) *connectproto.SDKResponse {
	return &connectproto.SDKResponse{
		RequestId:      req.RequestId,
		AccountId:      req.AccountId,
		EnvId:          req.EnvId,
		AppId:          req.AppId,
		Status:         resp.Status,
		Body:           resp.Body,
		NoRetry:        resp.NoRetry,
		RetryAfter:     resp.RetryAfter,
		SdkVersion:     resp.SDKVersion,
		RequestVersion: resp.RequestVersion,
		SystemTraceCtx: req.SystemTraceCtx,
		UserTraceCtx:   req.UserTraceCtx,
		RunId:          req.RunId,
	}
}

// requestHandler returns Opts.Handler.  when Opts.Handler is nil it returns
// a handler over the FunctionInvoker map passed to Connect.
func (h *connectHandler) requestHandler() RequestHandler {
	if h.opts.Handler != nil {
		return h.opts.Handler
	}
	return invokerHandler{h: h}
}

// invokerHandler serves each request with the FunctionInvoker registered for
// the request's app.
//
// this is used during regular Connect() execution and calls functions directly
// from inbound requests.
type invokerHandler struct {
	h *connectHandler
}

// Admin does some basic checks to ensure the functtion exists for the given request
func (i invokerHandler) Admit(req *connectproto.GatewayExecutorRequestData) error {
	if req.AppName == "" {
		return fmt.Errorf("missing app name in executor request")
	}
	if _, ok := i.h.invokers[req.AppName]; !ok {
		return fmt.Errorf("no invoker for app name %q", req.AppName)
	}
	return nil
}

// Done is a noop with regular connect
func (i invokerHandler) Done(*connectproto.GatewayExecutorRequestData) {}

func (i invokerHandler) Serve(_ context.Context, req *connectproto.GatewayExecutorRequestData) (Response, error) {
	h := i.h
	l := h.logger.With(
		"request_id", req.RequestId,
		"run_id", req.RunId,
		"app_id", req.AppId,
		"function_slug", req.FunctionSlug,
	)
	if req.JobId != "" {
		l = l.With("job_id", req.JobId)
	}

	invoker, ok := h.invokers[req.AppName]
	if !ok {
		return Response{}, fmt.Errorf("no invoker for app name %q", req.AppName)
	}

	// Note: This still uses JSON
	// TODO Replace with Protobuf
	var request sdkrequest.Request
	err := json.Unmarshal(req.RequestPayload, &request)
	if err != nil {
		l.Error("error decoding sdk request", "error", err)
		err = fmt.Errorf("invalid SDK request payload: %w", err)
	}
	request.CallCtx.RequestID = req.RequestId
	request.CallCtx.JobID = req.JobId

	var stepId *string
	if req.StepId != nil && *req.StepId != "step" {
		stepId = req.StepId
	}

	// Invoke function, always complete regardless of connection cancellation once
	// the request has been acknowledged.
	var (
		resp any
		ops  []sdkrequest.GeneratorOpcode
	)
	if err == nil && request.UsesAPI() {
		loadOpts := sdkrequest.LoadFromAPIOpts{
			APIBaseURL:        h.opts.APIBaseURL,
			AuthToken:         string(h.opts.HashedSigningKey),
			AuthTokenFallback: string(h.opts.HashedSigningKeyFallback),
		}
		if h.apiClient != nil {
			loadOpts.HTTPClient = &h.apiClient.client
		}
		err = sdkrequest.LoadFromAPI(context.Background(), &request, loadOpts)
		if err != nil {
			err = fmt.Errorf("error loading function state from API: %w", err)
		}
	}
	if err == nil {
		resp, ops, err = invoker.InvokeFunction(context.Background(), req.FunctionSlug, stepId, request)
	}

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
		err = fmt.Errorf("unhandled step error: %s", err)
		noRetry = true
	}

	// These may be added even for 2xx codes with step errors.
	var retryAfterVal *string
	if retryAt != nil {
		formatted := retryAt.Format(time.RFC3339)
		retryAfterVal = &formatted
	}

	if err != nil {
		l.Error("error calling function", "error", err)
		return Response{
			Status:         connectproto.SDKResponseStatus_ERROR,
			Body:           []byte(fmt.Sprintf("error calling function: %s", err.Error())),
			NoRetry:        noRetry,
			RetryAfter:     retryAfterVal,
			SDKVersion:     fmt.Sprintf("%s:v%s", h.opts.SDKLanguage, h.opts.SDKVersion),
			RequestVersion: 0, // Go SDK currently only supports v0
		}, nil
	}

	if len(ops) > 0 {
		// Note: This still uses JSON
		// TODO Replace with Protobuf
		serializedOps, err := json.Marshal(ops)
		if err != nil {
			return Response{}, fmt.Errorf("could not serialize ops: %w", err)
		}

		// Return the function opcode returned here so that we can re-invoke this
		// function and manage state appropriately.  Any opcode here takes precedence
		// over function return values as the function has not yet finished.
		return Response{
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
		return Response{}, fmt.Errorf("could not serialize resp: %w", err)
	}

	// Return the function response.
	return Response{
		Status:     connectproto.SDKResponseStatus_DONE,
		Body:       serializedResp,
		NoRetry:    noRetry,
		RetryAfter: retryAfterVal,
	}, nil
}
