package connect

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureInvoker records the last request and returns a fixed result.
type captureInvoker struct {
	request sdkrequest.Request
}

func (c *captureInvoker) InvokeFunction(
	ctx context.Context,
	slug string,
	stepId *string,
	request sdkrequest.Request,
) (any, []sdkrequest.GeneratorOpcode, error) {
	c.request = request
	return map[string]bool{"ok": true}, nil, nil
}

func newTestInvokerHandler(invoker FunctionInvoker, opts Opts) invokerHandler {
	return invokerHandler{
		invokers:   map[string]FunctionInvoker{"app": invoker},
		opts:       opts,
		logger:     slog.New(slog.DiscardHandler),
		httpClient: &http.Client{},
	}
}

func TestInvokerHandlerAdmit(t *testing.T) {
	tests := []struct {
		name    string
		appName string
		wantErr string
	}{
		{name: "registered app", appName: "app"},
		{name: "missing app name", wantErr: "missing app name"},
		{name: "unknown app", appName: "other", wantErr: `no invoker for app name "other"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestInvokerHandler(&captureInvoker{}, Opts{})
			err := h.Admit(&connectproto.GatewayExecutorRequestData{AppName: tt.appName})
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestInvokerHandlerUsesProtoRequestAndJobIDs(t *testing.T) {
	r := require.New(t)

	requestPayload, err := json.Marshal(sdkrequest.Request{
		Event: []byte(`{"name":"test/connect.request.ids","data":{}}`),
		Steps: map[string]json.RawMessage{},
		CallCtx: sdkrequest.CallCtx{
			RequestID: "body-request-id",
			JobID:     "body-job-id",
		},
	})
	r.NoError(err)

	invoker := &captureInvoker{}
	h := newTestInvokerHandler(invoker, Opts{})
	resp, err := h.Serve(context.Background(), &connectproto.GatewayExecutorRequestData{
		RequestId:      "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		JobId:          "job-123",
		AppName:        "app",
		FunctionSlug:   "fn",
		RequestPayload: requestPayload,
	})
	r.NoError(err)
	r.Equal(connectproto.SDKResponseStatus_DONE, resp.Status)
	r.Equal("01ARZ3NDEKTSV4RRFFQ69G5FAV", invoker.request.CallCtx.RequestID)
	r.Equal("job-123", invoker.request.CallCtx.JobID)
}

func TestInvokerHandlerHydratesUseAPIRequest(t *testing.T) {
	r := require.New(t)
	var apiCalls atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "Bearer primary", req.Header.Get("Authorization"))
		apiCalls.Add(1)

		switch req.URL.Path {
		case "/v0/runs/run-id/batch":
			_, _ = w.Write([]byte(`[{"name":"one"},{"name":"two"}]`))
		case "/v0/runs/run-id/actions":
			_, _ = w.Write([]byte(`{"step-id":{"value":true}}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer apiServer.Close()

	requestPayload, err := json.Marshal(sdkrequest.Request{
		Event: []byte(`{"name":"one"}`),
		CallCtx: sdkrequest.CallCtx{
			RunID:  "run-id",
			UseAPI: true,
		},
	})
	r.NoError(err)

	invoker := &captureInvoker{}
	h := newTestInvokerHandler(invoker, Opts{
		APIBaseURL:               apiServer.URL,
		HashedSigningKey:         []byte("primary"),
		HashedSigningKeyFallback: []byte("fallback"),
		SDKLanguage:              "go",
		SDKVersion:               "test",
	})
	resp, err := h.Serve(context.Background(), &connectproto.GatewayExecutorRequestData{
		RequestId:      "request-id",
		AppName:        "app",
		FunctionSlug:   "fn",
		RequestPayload: requestPayload,
		LeaseId:        "lease-id",
	})
	r.NoError(err)
	r.Equal(connectproto.SDKResponseStatus_DONE, resp.Status)
	r.EqualValues(2, apiCalls.Load())
	r.Len(invoker.request.Events, 2)
	assert.JSONEq(t, `{"name":"two"}`, string(invoker.request.Events[1]))
	r.Contains(invoker.request.Steps, "step-id")
	assert.JSONEq(t, `{"value":true}`, string(invoker.request.Steps["step-id"]))
}

func TestInvokerHandlerInvalidPayloadReturnsError(t *testing.T) {
	r := require.New(t)

	invoker := &captureInvoker{}
	h := newTestInvokerHandler(invoker, Opts{SDKLanguage: "go", SDKVersion: "test"})
	resp, err := h.Serve(context.Background(), &connectproto.GatewayExecutorRequestData{
		RequestId:      "request-id",
		AppName:        "app",
		FunctionSlug:   "fn",
		RequestPayload: []byte(`{not json`),
	})
	r.NoError(err)
	r.Equal(connectproto.SDKResponseStatus_ERROR, resp.Status)
	r.Contains(string(resp.Body), "invalid SDK request payload")
	r.Equal("go:vtest", resp.SDKVersion)
	r.False(resp.NoRetry)
}
