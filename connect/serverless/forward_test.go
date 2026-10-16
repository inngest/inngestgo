package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/internal/connectworker"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapResponse(t *testing.T) {
	retryAfter := "2026-10-16T00:00:00Z"
	tests := []struct {
		name   string
		status int
		header http.Header
		body   string
		want   connectworker.Response
	}{
		{
			name:   "200 is done",
			status: 200,
			header: newHeader(headerSDK, "js:v3.40.0", headerReqVersion, "2"),
			body:   `{"ok":true}`,
			want: connectworker.Response{
				Status:         connectproto.SDKResponseStatus_DONE,
				Body:           []byte(`{"ok":true}`),
				SDKVersion:     "js:v3.40.0",
				RequestVersion: 2,
			},
		},
		{
			name:   "206 is not completed",
			status: 206,
			body:   `[{"op":"Step"}]`,
			want: connectworker.Response{
				Status: connectproto.SDKResponseStatus_NOT_COMPLETED,
				Body:   []byte(`[{"op":"Step"}]`),
			},
		},
		{
			name:   "500 is an error with retry headers",
			status: 500,
			header: newHeader(headerNoRetry, "true", headerRetryAfter, retryAfter),
			body:   `boom`,
			want: connectworker.Response{
				Status:     connectproto.SDKResponseStatus_ERROR,
				Body:       []byte(`boom`),
				NoRetry:    true,
				RetryAfter: &retryAfter,
			},
		},
		{
			name:   "201 stream uses the inner status, body, and headers",
			status: 201,
			header: newHeader(headerSDK, "js:v3.40.0"),
			body:   `{"status":206,"body":"[{\"op\":\"Step\"}]","headers":{"x-inngest-req-version":"1"}}`,
			want: connectworker.Response{
				Status:         connectproto.SDKResponseStatus_NOT_COMPLETED,
				Body:           []byte(`[{"op":"Step"}]`),
				SDKVersion:     "js:v3.40.0",
				RequestVersion: 1,
			},
		},
		{
			name:   "201 Go stream keeps a string result",
			status: 201,
			header: newHeader(headerSDK, "go:v0.20.0"),
			body:   `{"status":200,"body":"true","headers":null,"noRetry":false,"retryAt":null}`,
			want: connectworker.Response{
				Status:     connectproto.SDKResponseStatus_DONE,
				Body:       []byte(`"true"`),
				SDKVersion: "go:v0.20.0",
			},
		},
		{
			name:   "201 Go stream carries retry settings",
			status: 201,
			header: newHeader(headerSDK, "go:v0.20.0"),
			body:   `{"status":500,"body":"boom","headers":null,"noRetry":true,"retryAt":"2026-10-16T00:00:00Z"}`,
			want: connectworker.Response{
				Status:     connectproto.SDKResponseStatus_ERROR,
				Body:       []byte(`"boom"`),
				SDKVersion: "go:v0.20.0",
				NoRetry:    true,
				RetryAfter: &retryAfter,
			},
		},
		{
			name:   "201 stream error",
			status: 201,
			body:   `{"error":"stream broke"}`,
			want: connectworker.Response{
				Status: connectproto.SDKResponseStatus_ERROR,
				Body:   []byte(`app stream response error: stream broke`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			header := tt.header
			if header == nil {
				header = http.Header{}
			}

			got := mapResponse(tt.status, header, []byte(tt.body))
			r.Equal(tt.want, got)
		})
	}
}

func TestForwarderAdmit(t *testing.T) {
	tests := []struct {
		name    string
		slug    string
		prepare func(adm *admission)
		wantErr error
		wantMsg string
	}{
		{name: "known function", slug: testFnSlug},
		{name: "unknown function", slug: "other", wantMsg: `app has no function "other"`},
		{name: "draining", slug: testFnSlug, prepare: func(adm *admission) { adm.drain() }, wantErr: errDraining},
		{name: "at capacity", slug: testFnSlug, prepare: func(adm *admission) { adm.admit() }, wantErr: errAtCapacity},
		{name: "after max requests", slug: testFnSlug, prepare: func(adm *admission) { adm.max = 1; adm.ctr.Store(1) }, wantErr: errDraining},
		{name: "after max lifetime", slug: testFnSlug, prepare: func(adm *admission) { adm.deadline = time.Now().Add(-time.Second).UnixNano() }, wantErr: errDraining},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			adm := newAdmission(1, 0, time.Time{})
			if tt.prepare != nil {
				tt.prepare(adm)
			}
			f := &forwarder{stepURLs: map[string]string{testFnSlug: "http://127.0.0.1/api/inngest"}, admission: adm}

			err := f.Admit(&connectproto.GatewayExecutorRequestData{FunctionSlug: tt.slug})
			switch {
			case tt.wantErr != nil:
				r.ErrorIs(err, tt.wantErr)
			case tt.wantMsg != "":
				r.EqualError(err, tt.wantMsg)
			default:
				r.NoError(err)
				r.Equal(1, adm.inFlight())
				f.Done(nil)
				r.Equal(0, adm.inFlight())
			}
		})
	}
}

func TestForwarderServeSendsExecutorRequest(t *testing.T) {
	r := require.New(t)

	type seen struct {
		query  map[string]string
		header http.Header
		valid  bool
	}
	seenCh := make(chan seen, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		valid, _, _ := inngestgo.ValidateRequestSignature(req.Context(), req.Header.Get(headerSignature), testSigningKey, "", body, false)
		seenCh <- seen{
			query:  map[string]string{"fnId": req.URL.Query().Get("fnId"), "step": req.URL.Query().Get("step"), "stepId": req.URL.Query().Get("stepId")},
			header: req.Header.Clone(),
			valid:  valid,
		}
		w.Header().Set(headerSDK, "js:v3.40.0")
		_, _ = w.Write([]byte(`"ok"`))
	}))
	defer server.Close()

	f := &forwarder{
		client:    server.Client(),
		keys:      signingKeys{primary: testSigningKey},
		stepURLs:  map[string]string{testFnSlug: server.URL + "/api/inngest?fnId=" + testFnSlug + "&step=step"},
		admission: newAdmission(1, 0, time.Time{}),
		logger:    discardLogger(),
	}
	stepID := "step"
	resp, err := f.Serve(context.Background(), &connectproto.GatewayExecutorRequestData{
		RequestId:      "request-id",
		AccountId:      "account-id",
		FunctionSlug:   testFnSlug,
		StepId:         &stepID,
		RequestPayload: []byte(`{"event":{"name":"test/echo"},"ctx":{"run_id":"run-id"}}`),
		RunId:          "run-id",
		JobId:          "job-id",
	})
	r.NoError(err)
	r.Equal(connectproto.SDKResponseStatus_DONE, resp.Status)
	r.Equal("js:v3.40.0", resp.SDKVersion)

	got := <-seenCh
	r.True(got.valid, "signature did not validate")
	r.Equal(map[string]string{"fnId": testFnSlug, "step": "step", "stepId": "step"}, got.query)
	r.Equal("run-id", got.header.Get(headerRunID))
	r.Equal("request-id", got.header.Get(headerRequestID))
	r.Equal("job-id", got.header.Get(headerJobID))
	r.Equal("step", got.header.Get(headerStepID))
	r.Equal("account-id", got.header.Get(headerAccountID))
}

func TestForwarderServeAppUnreachable(t *testing.T) {
	r := require.New(t)

	f := &forwarder{
		client:    &http.Client{},
		stepURLs:  map[string]string{testFnSlug: "http://127.0.0.1:1/api/inngest"},
		admission: newAdmission(1, 0, time.Time{}),
		logger:    discardLogger(),
	}
	resp, err := f.Serve(context.Background(), &connectproto.GatewayExecutorRequestData{FunctionSlug: testFnSlug})
	r.NoError(err)
	r.Equal(connectproto.SDKResponseStatus_ERROR, resp.Status)
	r.Contains(string(resp.Body), "could not call app")
	r.False(resp.NoRetry)
}

// TestForwarderRunsGoFunction syncs a Go serve() app and runs each function
// to completion through the forwarder.  the test plays the executor.  it
// reads the opcodes from each 206 and sends the step results back in the next
// request, until the function returns 200.
func TestForwarderRunsGoFunction(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%t", streaming), func(t *testing.T) {
			testForwarderRunsGoFunction(t, streaming)
		})
	}
}

func testForwarderRunsGoFunction(t *testing.T, streaming bool) {
	tests := []struct {
		name       string
		slug       string
		event      string
		wantOps    []string
		wantOutput string
	}{
		{
			name:       "single request",
			slug:       testFnSlug,
			event:      `{"name":"test/echo","data":{"hello":"world"}}`,
			wantOutput: `{"hello":"world"}`,
		},
		{
			name:  "steps and a sleep",
			slug:  testStepsSlug,
			event: `{"name":"test/steps","data":{}}`,
			wantOps: []string{
				enums.OpcodeStepRun.String(),
				enums.OpcodeSleep.String(),
				enums.OpcodeStepRun.String(),
			},
			wantOutput: `"ab"`,
		},
	}

	h, err := newGoAppWith(streaming)
	require.NoError(t, err)
	server := httptest.NewServer(h)
	defer server.Close()

	keys := signingKeys{primary: testSigningKey}
	app, err := syncApp(context.Background(), server.Client(), server.URL+"/api/inngest", keys, false)
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			f := &forwarder{
				client:    server.Client(),
				keys:      keys,
				stepURLs:  app.stepURLs,
				admission: newAdmission(1, 0, time.Time{}),
				logger:    discardLogger(),
			}

			steps := map[string]json.RawMessage{}
			var ops []string
			for attempt := 0; ; attempt++ {
				r.Less(attempt, 10, "function did not finish")

				resp := serveOnce(t, f, tt.slug, tt.event, steps)
				r.Contains(resp.SDKVersion, "go:")

				if resp.Status == connectproto.SDKResponseStatus_DONE {
					assert.JSONEq(t, tt.wantOutput, string(resp.Body))
					break
				}
				r.Equal(connectproto.SDKResponseStatus_NOT_COMPLETED, resp.Status, "body: %s", resp.Body)

				var opcodes []struct {
					ID   string          `json:"id"`
					Op   string          `json:"op"`
					Data json.RawMessage `json:"data"`
				}
				r.NoError(json.Unmarshal(resp.Body, &opcodes))
				for _, op := range opcodes {
					ops = append(ops, op.Op)
					switch op.Op {
					case enums.OpcodeStepRun.String():
						steps[op.ID] = json.RawMessage(`{"data":` + string(op.Data) + `}`)
					default:
						steps[op.ID] = json.RawMessage(`null`)
					}
				}
			}
			r.Equal(tt.wantOps, ops)
		})
	}
}

// serveOnce sends one executor request for slug through the forwarder, with
// the step results so far.
func serveOnce(t *testing.T, f *forwarder, slug, event string, steps map[string]json.RawMessage) connectworker.Response {
	t.Helper()
	r := require.New(t)

	payload, err := json.Marshal(sdkrequest.Request{
		Event: []byte(event),
		Steps: steps,
		CallCtx: sdkrequest.CallCtx{
			RunID: "run-id",
		},
	})
	r.NoError(err)

	stepID := "step"
	req := &connectproto.GatewayExecutorRequestData{
		RequestId:      "request-id",
		FunctionSlug:   slug,
		StepId:         &stepID,
		RequestPayload: payload,
		RunId:          "run-id",
	}
	r.NoError(f.Admit(req))
	defer f.Done(req)

	resp, err := f.Serve(context.Background(), req)
	r.NoError(err)
	return resp
}

// newHeader builds a header from key and value pairs, with canonical keys as
// a real HTTP response has.
func newHeader(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}
