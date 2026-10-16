package serverless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/inngest/inngestgo/internal/connectworker"
)

var (
	errDraining   = errors.New("daemon is draining")
	errAtCapacity = errors.New("daemon is at capacity")
)

// forwarder serves each executor request by posting it to the app over HTTP,
// as the executor does for an HTTP app.
type forwarder struct {
	client    *http.Client
	keys      signingKeys
	stepURLs  map[string]string
	admission *admission
	logger    *slog.Logger
}

var _ connectworker.RequestHandler = (*forwarder)(nil)

func (f *forwarder) Admit(req *connectproto.GatewayExecutorRequestData) error {
	if _, ok := f.stepURLs[req.FunctionSlug]; !ok {
		return fmt.Errorf("app has no function %q", req.FunctionSlug)
	}
	switch f.admission.admit() {
	case admitted:
		return nil
	case rejectAtCapacity:
		return errAtCapacity
	default:
		// draining, MaxRequests, and MaxLifetime all mean this worker takes no
		// more requests.
		return errDraining
	}
}

func (f *forwarder) Done(*connectproto.GatewayExecutorRequestData) {
	f.admission.release()
}

// Serve never returns an error.  a failed call to the app becomes an ERROR
// reply, which the executor retries, so the request does not wait for its
// lease to expire.
func (f *forwarder) Serve(ctx context.Context, req *connectproto.GatewayExecutorRequestData) (connectworker.Response, error) {
	l := f.logger.With(
		"request_id", req.RequestId,
		"run_id", req.RunId,
		"function_slug", req.FunctionSlug,
	)

	httpReq, err := f.newRequest(ctx, req)
	if err != nil {
		l.Error("could not build app request", "error", err)
		return errorResponse(fmt.Errorf("could not build app request: %w", err)), nil
	}

	resp, err := f.client.Do(httpReq)
	if err != nil {
		l.Error("could not call app", "error", err)
		return errorResponse(fmt.Errorf("could not call app: %w", err)), nil
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		l.Error("could not read app response", "error", err)
		return errorResponse(fmt.Errorf("could not read app response: %w", err)), nil
	}

	return mapResponse(resp.StatusCode, resp.Header, body), nil
}

// newRequest builds the POST that the executor sends to an HTTP app for this
// request.
func (f *forwarder) newRequest(ctx context.Context, req *connectproto.GatewayExecutorRequestData) (*http.Request, error) {
	target, err := url.Parse(f.stepURLs[req.FunctionSlug])
	if err != nil {
		return nil, err
	}
	query := target.Query()
	query.Set("fnId", req.FunctionSlug)
	query.Set("stepId", req.GetStepId())
	target.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(req.RequestPayload))
	if err != nil {
		return nil, err
	}

	sig, err := f.keys.sign(ctx, req.RequestPayload)
	if err != nil {
		return nil, fmt.Errorf("could not sign request: %w", err)
	}

	h := httpReq.Header
	h.Set(headerContentType, "application/json")
	if sig != "" {
		h.Set(headerSignature, sig)
	}
	h.Set(headerRunID, req.RunId)
	h.Set(headerStepID, req.GetStepId())
	if req.AccountId != "" {
		h.Set(headerAccountID, req.AccountId)
	}
	if req.RequestId != "" {
		h.Set(headerRequestID, req.RequestId)
	}
	if req.JobId != "" {
		h.Set(headerJobID, req.JobId)
	}
	return httpReq, nil
}

// streamResponse is the body of a 201 reply.  an app that streams its reply
// sends 201 at once and puts the real status, headers, and body in here.
type streamResponse struct {
	Error      *string           `json:"error"`
	StatusCode int               `json:"status"`
	Body       json.RawMessage   `json:"body"`
	Headers    map[string]string `json:"headers"`
}

// mapResponse turns the app's HTTP reply into the connect reply, as the
// executor does for an HTTP app.
func mapResponse(status int, header http.Header, body []byte) connectworker.Response {
	if status == http.StatusCreated {
		var stream streamResponse
		if err := json.Unmarshal(body, &stream); err != nil {
			return errorResponse(fmt.Errorf("could not decode app stream response: %w", err))
		}
		if stream.Error != nil {
			return errorResponse(fmt.Errorf("app stream response error: %s", *stream.Error))
		}

		status = stream.StatusCode
		body = stream.Body
		// a string body can arrive JSON encoded twice.
		if len(body) > 1 && body[0] == '"' && body[len(body)-1] == '"' {
			var s string
			if err := json.Unmarshal(body, &s); err == nil {
				body = []byte(s)
			}
		}

		header = header.Clone()
		for k, v := range stream.Headers {
			header.Set(k, v)
		}
	}

	resp := connectworker.Response{
		Body:       body,
		NoRetry:    header.Get(headerNoRetry) == "true",
		SDKVersion: header.Get(headerSDK),
	}
	if after := header.Get(headerRetryAfter); after != "" {
		resp.RetryAfter = &after
	}
	if v, err := strconv.ParseUint(header.Get(headerReqVersion), 10, 32); err == nil {
		resp.RequestVersion = uint32(v)
	}

	switch status {
	case http.StatusOK:
		resp.Status = connectproto.SDKResponseStatus_DONE
	case http.StatusPartialContent:
		resp.Status = connectproto.SDKResponseStatus_NOT_COMPLETED
	default:
		resp.Status = connectproto.SDKResponseStatus_ERROR
	}
	return resp
}

func errorResponse(err error) connectworker.Response {
	return connectworker.Response{
		Status: connectproto.SDKResponseStatus_ERROR,
		Body:   []byte(err.Error()),
	}
}
