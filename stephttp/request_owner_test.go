package stephttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/step"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"
)

// blockingAPI holds every CheckpointNewRun call until release is closed.
type blockingAPI struct {
	called  chan []sdkrequest.GeneratorOpcode
	release chan struct{}
	// inputs receives the run data of each call when it is not nil.
	inputs chan NewAPIRunData
	// getStepsErr is the error that GetSteps returns.
	getStepsErr error
	// newRunErr is the error that CheckpointNewRun returns.
	newRunErr error
}

func (b *blockingAPI) CheckpointNewRun(ctx context.Context, runID ulid.ULID, input NewAPIRunData, steps ...sdkrequest.GeneratorOpcode) (*CheckpointRun, error) {
	if b.inputs != nil {
		b.inputs <- input
	}
	b.called <- steps
	<-b.release
	if b.newRunErr != nil {
		return nil, b.newRunErr
	}
	return &CheckpointRun{RunID: runID}, nil
}

func (b *blockingAPI) CheckpointSteps(ctx context.Context, run CheckpointRun, steps []sdkrequest.GeneratorOpcode) error {
	return nil
}

func (b *blockingAPI) CheckpointResponse(ctx context.Context, run CheckpointRun, result APIResult) error {
	return nil
}

func (b *blockingAPI) GetSteps(ctx context.Context, runID ulid.ULID) (map[string]json.RawMessage, error) {
	if b.getStepsErr != nil {
		return nil, b.getStepsErr
	}
	return map[string]json.RawMessage{}, nil
}

func TestFinishedRunCheckpointsInBackground(t *testing.T) {
	api := &blockingAPI{
		called:  make(chan []sdkrequest.GeneratorOpcode, 1),
		release: make(chan struct{}),
	}

	p := Setup(SetupOpts{
		Domain: "test.example.com",
	})
	p.api = api

	handler := p.HandleFunc(FnOpts{TrackAllRequests: true}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	rec := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		handler(rec, httptest.NewRequest(http.MethodGet, "/test", nil))
		close(served)
	}()

	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("handler waited for the checkpoint to finish")
	}
	require.Equal(t, "ok", rec.Body.String())

	var steps []sdkrequest.GeneratorOpcode
	select {
	case steps = <-api.called:
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint was not sent")
	}
	require.Len(t, steps, 1)
	require.Equal(t, enums.OpcodeRunComplete, steps[0].Op)

	done := p.Wait(t.Context())

	// Wait polls once a second, so 1.5 seconds covers at least one check.
	select {
	case <-done:
		t.Fatal("Wait returned while a checkpoint was in flight")
	case <-time.After(1500 * time.Millisecond):
	}

	close(api.release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the checkpoint finished")
	}
}

func TestHandledStepErrorFinishesRun(t *testing.T) {
	tests := []struct {
		name          string
		asyncResponse AsyncResponse
	}{
		{name: "token response", asyncResponse: AsyncResponseToken{}},
		{name: "redirect response", asyncResponse: AsyncResponseRedirect{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
			}
			close(api.release)

			p := Setup(SetupOpts{
				Optional: OptionalSetupOpts{DefaultAsyncResponse: tt.asyncResponse},
			})
			p.api = api

			handler := p.HandleFunc(FnOpts{}, func(w http.ResponseWriter, r *http.Request) {
				_, err := step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
					return 0, fmt.Errorf("boom")
				})
				if err != nil {
					http.Error(w, "handled", http.StatusUnauthorized)
				}
			})

			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.Equal(t, "handled\n", rec.Body.String())
			require.Empty(t, rec.Header().Get("Location"))

			var steps []sdkrequest.GeneratorOpcode
			select {
			case steps = <-api.called:
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
			require.Len(t, steps, 2)
			require.Equal(t, enums.OpcodeStepFailed, steps[0].Op)
			require.Equal(t, enums.OpcodeRunComplete, steps[1].Op)
		})
	}
}

func TestNewRunStoresRequestBody(t *testing.T) {
	const body = `{"hello":"world"}`

	tests := []struct {
		name string
		// read is how many bytes the handler reads.  -1 reads the whole body.
		read     int
		omit     bool
		limit    int
		expected string
	}{
		{name: "handler reads the whole body", read: -1, expected: body},
		{name: "handler reads no body", read: 0, expected: body},
		{name: "handler reads part of the body", read: 5, expected: body},
		{name: "config omits the body", read: -1, omit: true, expected: ""},
		{name: "limit with the whole body read", read: -1, limit: 5, expected: body[:5]},
		{name: "limit with no body read", read: 0, limit: 5, expected: body[:5]},
		{name: "limit with less than the limit read", read: 3, limit: 5, expected: body[:5]},
		{name: "limit with more than the limit read", read: 10, limit: 5, expected: body[:5]},
		{name: "limit equal to the body size", read: 0, limit: len(body), expected: body},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
				inputs:  make(chan NewAPIRunData, 1),
			}
			close(api.release)

			p := Setup(SetupOpts{})
			p.api = api

			var handlerRead string
			opts := FnOpts{TrackAllRequests: true, OmitRequestBody: tt.omit, MaxRequestBodySize: tt.limit}
			handler := p.HandleFunc(opts, func(w http.ResponseWriter, r *http.Request) {
				var byt []byte
				switch tt.read {
				case -1:
					byt, _ = io.ReadAll(r.Body)
				case 0:
				default:
					byt = make([]byte, tt.read)
					_, _ = io.ReadFull(r.Body, byt)
				}
				_ = r.Body.Close()
				handlerRead = string(byt)
				_, _ = w.Write([]byte("ok"))
			})

			req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
			handler(httptest.NewRecorder(), req)

			switch tt.read {
			case -1:
				require.Equal(t, body, handlerRead)
			default:
				require.Equal(t, body[:tt.read], handlerRead)
			}

			select {
			case input := <-api.inputs:
				require.Equal(t, tt.expected, string(input.Body))
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
		})
	}
}

func TestHandlerPanicRecordsError(t *testing.T) {
	tests := []struct {
		name string
		// write is the response the handler writes before it panics.  zero writes
		// nothing.
		write          int
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "panic before the response",
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "Internal Server Error\n",
		},
		{
			name:           "panic after the response",
			write:          http.StatusCreated,
			expectedStatus: http.StatusCreated,
			expectedBody:   "created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
			}
			close(api.release)

			p := Setup(SetupOpts{})
			p.api = api

			handler := p.HandleFunc(FnOpts{TrackAllRequests: true}, func(w http.ResponseWriter, r *http.Request) {
				if tt.write != 0 {
					w.WriteHeader(tt.write)
					_, _ = w.Write([]byte("created"))
				}
				panic("kaboom")
			})

			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

			require.Equal(t, tt.expectedStatus, rec.Code)
			require.Equal(t, tt.expectedBody, rec.Body.String())

			var steps []sdkrequest.GeneratorOpcode
			select {
			case steps = <-api.called:
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
			require.Len(t, steps, 1)
			require.Equal(t, enums.OpcodeRunComplete, steps[0].Op)

			var result APIResult
			require.NoError(t, json.Unmarshal(steps[0].Data, &result))
			require.Equal(t, tt.expectedStatus, result.Status)
			require.Equal(t, tt.expectedBody, result.Body)
			require.Contains(t, result.Error, "function panicked: kaboom")
		})
	}
}

func TestResumeRequiresSavedSteps(t *testing.T) {
	const signingKey = "signkey-test-12345678"

	tests := []struct {
		name           string
		getStepsErr    error
		expectedStatus int
		expectHandler  bool
	}{
		{
			name:           "steps load",
			expectedStatus: http.StatusPartialContent,
			expectHandler:  true,
		},
		{
			name:           "steps fail to load",
			getStepsErr:    fmt.Errorf("api unavailable"),
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("INNGEST_DEV", "")

			api := &blockingAPI{
				called:      make(chan []sdkrequest.GeneratorOpcode, 1),
				release:     make(chan struct{}),
				getStepsErr: tt.getStepsErr,
			}
			t.Cleanup(func() { close(api.release) })

			p := Setup(SetupOpts{
				Optional: OptionalSetupOpts{SigningKey: signingKey},
			})
			p.api = api

			handlerCalled := false
			handler := p.HandleFunc(FnOpts{}, func(w http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				_, _ = step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
					return 1, nil
				})
				_, _ = w.Write([]byte("ok"))
			})

			runID := ulid.Make().String()
			sig, err := inngestgo.Sign(context.Background(), time.Now(), []byte(signingKey), []byte(runID))
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			req.Header.Set(headerRunID, runID)
			req.Header.Set(headerSignature, sig)

			rec := httptest.NewRecorder()
			handler(rec, req)

			require.Equal(t, tt.expectedStatus, rec.Code)
			require.Equal(t, tt.expectHandler, handlerCalled)
			if tt.expectHandler {
				require.Equal(t, runID, rec.Header().Get(headerRunID))
			}

			// a resume never creates a new run.  a new run checkpoints in a tracked
			// goroutine that blocks on release, so it shows up in both checks.
			require.Zero(t, p.inflight.Load())
			require.Empty(t, api.called)
		})
	}
}

func TestFunctionIDDefaultsToRoutePattern(t *testing.T) {
	tests := []struct {
		name string
		id   string
		// pattern is the http.ServeMux pattern.  empty calls the handler without
		// a ServeMux.
		pattern string
		// middleware wraps the ServeMux with Provider.Middleware.  false wraps the
		// route with Provider.HandleFunc.
		middleware bool
		// copyRequest puts middleware that calls r.WithContext between
		// Provider.Middleware and the ServeMux.
		copyRequest bool
		method      string
		path        string
		expected    string
	}{
		{
			name:     "config ID wins",
			id:       "create-user",
			pattern:  "POST /users/{id}",
			method:   http.MethodPost,
			path:     "/users/123",
			expected: "create-user",
		},
		{
			name:     "pattern with a method",
			pattern:  "POST /users/{id}",
			method:   http.MethodPost,
			path:     "/users/123",
			expected: "POST /users/{id}",
		},
		{
			name:     "pattern without a method",
			pattern:  "/users/{id}",
			method:   http.MethodGet,
			path:     "/users/123",
			expected: "GET /users/{id}",
		},
		{
			name:     "no ServeMux",
			method:   http.MethodGet,
			path:     "/users/123",
			expected: "",
		},
		{
			name:       "Middleware in front of a ServeMux",
			pattern:    "POST /users/{id}",
			middleware: true,
			method:     http.MethodPost,
			path:       "/users/123",
			expected:   "POST /users/{id}",
		},
		{
			name:        "Middleware in front of middleware that copies the request",
			pattern:     "POST /users/{id}",
			middleware:  true,
			copyRequest: true,
			method:      http.MethodPost,
			path:        "/users/123",
			expected:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
				inputs:  make(chan NewAPIRunData, 1),
			}
			close(api.release)

			p := Setup(SetupOpts{})
			p.api = api

			opts := FnOpts{ID: tt.id, TrackAllRequests: true}
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("ok"))
			})

			var handler http.Handler = p.HandleFunc(opts, next)
			switch {
			case tt.middleware:
				// behind the middleware, the handler opts in with Configure.
				optedIn := func(w http.ResponseWriter, r *http.Request) {
					Configure(r.Context(), func(o *FnOpts) {
						o.ID = tt.id
						o.TrackAllRequests = true
					})
					next(w, r)
				}
				mux := http.NewServeMux()
				mux.HandleFunc(tt.pattern, optedIn)
				handler = mux
				if tt.copyRequest {
					handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mux.ServeHTTP(w, r.WithContext(r.Context()))
					})
				}
				handler = p.Middleware(MiddlewareOpts{})(handler)
			case tt.pattern != "":
				mux := http.NewServeMux()
				mux.Handle(tt.pattern, handler)
				handler = mux
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			require.Equal(t, http.StatusOK, rec.Code)

			select {
			case input := <-api.inputs:
				require.Equal(t, tt.expected, input.Fn)
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
		})
	}
}

func TestAsyncResponseDefaults(t *testing.T) {
	tests := []struct {
		name            string
		providerDefault AsyncResponse
		route           AsyncResponse
		// omitResponse calls Configure to omit the response before the async step.
		omitResponse   bool
		expectedStatus int
	}{
		{
			name:           "redirect without any config",
			expectedStatus: http.StatusSeeOther,
		},
		{
			name:            "provider default",
			providerDefault: AsyncResponseToken{},
			expectedStatus:  http.StatusOK,
		},
		{
			name:            "provider default after Configure",
			providerDefault: AsyncResponseToken{},
			omitResponse:    true,
			expectedStatus:  http.StatusOK,
		},
		{
			name:            "route config wins",
			providerDefault: AsyncResponseToken{},
			route:           AsyncResponseRedirect{},
			expectedStatus:  http.StatusSeeOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
			}
			close(api.release)

			p := Setup(SetupOpts{
				Optional: OptionalSetupOpts{DefaultAsyncResponse: tt.providerDefault},
			})
			p.api = api

			handler := p.HandleFunc(FnOpts{AsyncResponse: tt.route}, func(w http.ResponseWriter, r *http.Request) {
				if tt.omitResponse {
					Configure(r.Context(), func(o *FnOpts) { o.OmitResponseBody = true })
				}
				step.Sleep(r.Context(), "wait", time.Second)
				_, _ = w.Write([]byte("done"))
			})

			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

			require.Equal(t, tt.expectedStatus, rec.Code)
			if tt.expectedStatus == http.StatusOK {
				require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
				var token asyncResponseToken
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &token))
				require.NotEqual(t, ulid.ULID{}, token.RunID)
			}
		})
	}
}

func TestTrackingStartsAtFirstStep(t *testing.T) {
	tests := []struct {
		name string
		opts FnOpts
		// stepBeforeWrite runs a step before the handler writes.  stepAfterWrite
		// runs a step after it.
		stepBeforeWrite bool
		stepAfterWrite  bool
		expectRun       bool
		expectHeader    bool
		// expectedBody is the response body stored with the run.
		expectedBody string
	}{
		{
			name: "no steps",
		},
		{
			name:         "no steps with TrackAllRequests",
			opts:         FnOpts{TrackAllRequests: true},
			expectRun:    true,
			expectHeader: true,
			expectedBody: "ok",
		},
		{
			name:            "step before the response",
			stepBeforeWrite: true,
			expectRun:       true,
			expectHeader:    true,
			expectedBody:    "ok",
		},
		{
			name:           "step after the response",
			stepAfterWrite: true,
			expectRun:      true,
			expectedBody:   "ok",
		},
		{
			name:            "step before the response with OmitResponseBody",
			opts:            FnOpts{OmitResponseBody: true},
			stepBeforeWrite: true,
			expectRun:       true,
			expectHeader:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
			}
			close(api.release)

			p := Setup(SetupOpts{})
			p.api = api

			runStep := func(r *http.Request) {
				_, _ = step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
					return 1, nil
				})
			}
			handler := p.HandleFunc(tt.opts, func(w http.ResponseWriter, r *http.Request) {
				if tt.stepBeforeWrite {
					runStep(r)
				}
				_, _ = w.Write([]byte("ok"))
				if tt.stepAfterWrite {
					runStep(r)
				}
			})

			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, "/test", nil))
			require.Equal(t, "ok", rec.Body.String())

			if tt.expectHeader {
				_, err := ulid.Parse(rec.Header().Get(headerRunID))
				require.NoError(t, err)
				require.NotEmpty(t, rec.Header().Get("X-Inngest-SDK"))
			} else {
				require.Empty(t, rec.Header().Get(headerRunID))
				require.Empty(t, rec.Header().Get("X-Inngest-SDK"))
			}

			if !tt.expectRun {
				// a new run checkpoints in a tracked goroutine that sends to called
				// before it returns, so a run shows up in one of these checks.
				require.Zero(t, p.inflight.Load())
				require.Empty(t, api.called)
				return
			}

			var steps []sdkrequest.GeneratorOpcode
			select {
			case steps = <-api.called:
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
			complete := steps[len(steps)-1]
			require.Equal(t, enums.OpcodeRunComplete, complete.Op)

			if tt.opts.OmitResponseBody {
				require.Empty(t, complete.Data)
				return
			}
			var result APIResult
			require.NoError(t, json.Unmarshal(complete.Data, &result))
			require.Equal(t, tt.expectedBody, result.Body)
		})
	}
}

func TestStoredResponseBodyLimit(t *testing.T) {
	tests := []struct {
		name         string
		limit        int
		writes       []string
		expectedBody string
	}{
		{
			name:         "body under the limit",
			limit:        10,
			writes:       []string{"hello"},
			expectedBody: "hello",
		},
		{
			name:         "one write over the limit",
			limit:        5,
			writes:       []string{"hello world"},
			expectedBody: "hello",
		},
		{
			name:         "writes that cross the limit",
			limit:        7,
			writes:       []string{"hello", " ", "world"},
			expectedBody: "hello w",
		},
		{
			name:         "zero uses the default limit",
			writes:       []string{"hello"},
			expectedBody: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
			}
			close(api.release)

			p := Setup(SetupOpts{})
			p.api = api

			opts := FnOpts{TrackAllRequests: true, MaxResponseBodySize: tt.limit}
			handler := p.HandleFunc(opts, func(w http.ResponseWriter, r *http.Request) {
				for _, write := range tt.writes {
					_, _ = w.Write([]byte(write))
				}
			})

			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

			// the client always gets the full response.
			require.Equal(t, strings.Join(tt.writes, ""), rec.Body.String())

			var steps []sdkrequest.GeneratorOpcode
			select {
			case steps = <-api.called:
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
			var result APIResult
			require.NoError(t, json.Unmarshal(steps[len(steps)-1].Data, &result))
			require.Equal(t, tt.expectedBody, result.Body)
		})
	}
}

func TestResolveConfigBodySizes(t *testing.T) {
	p := Setup(SetupOpts{})
	r := httptest.NewRequest(http.MethodGet, "/test", nil)

	require.Equal(t, DefaultMaxRequestBodySize, p.resolveConfig(FnOpts{}, r).MaxRequestBodySize)
	require.Equal(t, 10, p.resolveConfig(FnOpts{MaxRequestBodySize: 10}, r).MaxRequestBodySize)
	require.Equal(t, DefaultMaxResponseBodySize, p.resolveConfig(FnOpts{}, r).MaxResponseBodySize)
	require.Equal(t, DefaultMaxResponseBodySize, p.resolveConfig(FnOpts{MaxResponseBodySize: -1}, r).MaxResponseBodySize)
	require.Equal(t, 10, p.resolveConfig(FnOpts{MaxResponseBodySize: 10}, r).MaxResponseBodySize)
}

func TestAsyncStartFailureFailsRequest(t *testing.T) {
	tests := []struct {
		name          string
		asyncResponse AsyncResponse
	}{
		{name: "redirect response", asyncResponse: AsyncResponseRedirect{}},
		{name: "token response", asyncResponse: AsyncResponseToken{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:    make(chan []sdkrequest.GeneratorOpcode, 1),
				release:   make(chan struct{}),
				newRunErr: fmt.Errorf("api unavailable"),
			}
			close(api.release)

			p := Setup(SetupOpts{})
			p.api = api

			handler := p.HandleFunc(FnOpts{AsyncResponse: tt.asyncResponse}, func(w http.ResponseWriter, r *http.Request) {
				step.Sleep(r.Context(), "wait", time.Second)
				_, _ = w.Write([]byte("done"))
			})

			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodGet, "/test", nil))

			require.Equal(t, http.StatusInternalServerError, rec.Code)
			require.Equal(t, "Internal Server Error\n", rec.Body.String())
			require.Empty(t, rec.Header().Get("Location"))
			require.Len(t, api.called, 1)
		})
	}
}

func TestConfigureDuringRequest(t *testing.T) {
	type operation struct {
		Name   string `json:"operationName"`
		Secret bool   `json:"secret"`
	}

	tests := []struct {
		name string
		// middleware wraps the whole ServeMux with Provider.Middleware.  false
		// wraps the route with Provider.Handle.
		middleware bool
		opts       FnOpts
		body       string
		// configure runs inside the handler after it reads the operation.
		configure            func(op operation, o *FnOpts)
		expectedFn           string
		expectedBody         string
		expectResponseStored bool
	}{
		{
			name:       "GraphQL operation sets the ID",
			middleware: true,
			body:       `{"operationName":"GetUser"}`,
			configure: func(op operation, o *FnOpts) {
				o.ID = "gql/" + op.Name
				o.OmitRequestBody = op.Secret
			},
			expectedFn:           "gql/GetUser",
			expectedBody:         `{"operationName":"GetUser"}`,
			expectResponseStored: true,
		},
		{
			name:       "GraphQL operation omits a secret body",
			middleware: true,
			body:       `{"operationName":"SetSecret","secret":true}`,
			configure: func(op operation, o *FnOpts) {
				o.ID = "gql/" + op.Name
				o.OmitRequestBody = op.Secret
			},
			expectedFn:           "gql/SetSecret",
			expectedBody:         "",
			expectResponseStored: true,
		},
		{
			name: "fields that Configure does not set stay",
			opts: FnOpts{ID: "route"},
			body: `{}`,
			configure: func(op operation, o *FnOpts) {
				o.OmitResponseBody = true
			},
			expectedFn:   "route",
			expectedBody: `{}`,
		},
		{
			name: "an empty ID falls back to the route pattern",
			opts: FnOpts{ID: "route"},
			body: `{}`,
			configure: func(op operation, o *FnOpts) {
				o.ID = ""
			},
			expectedFn:           "POST /gql",
			expectedBody:         `{}`,
			expectResponseStored: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
				inputs:  make(chan NewAPIRunData, 1),
			}
			close(api.release)

			p := Setup(SetupOpts{})
			p.api = api

			gql := func(w http.ResponseWriter, r *http.Request) {
				var op operation
				require.NoError(t, json.NewDecoder(r.Body).Decode(&op))
				Configure(r.Context(), func(o *FnOpts) { tt.configure(op, o) })

				_, _ = step.Run(r.Context(), "resolve", func(ctx context.Context) (int, error) {
					return 1, nil
				})
				_, _ = w.Write([]byte(`{"data":{}}`))
			}

			mux := http.NewServeMux()
			var handler http.Handler = mux
			if tt.middleware {
				// GraphQL reads the body before Configure, so the middleware
				// records every body.
				mux.HandleFunc("POST /gql", gql)
				handler = p.Middleware(MiddlewareOpts{RecordRequestBodies: true})(mux)
			} else {
				mux.Handle("POST /gql", p.HandleFunc(tt.opts, gql))
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/gql", strings.NewReader(tt.body)))
			require.Equal(t, `{"data":{}}`, rec.Body.String())

			select {
			case input := <-api.inputs:
				require.Equal(t, tt.expectedFn, input.Fn)
				require.Equal(t, tt.expectedBody, string(input.Body))
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}

			steps := <-api.called
			complete := steps[len(steps)-1]
			require.Equal(t, enums.OpcodeRunComplete, complete.Op)
			if !tt.expectResponseStored {
				require.Empty(t, complete.Data)
				return
			}
			var result APIResult
			require.NoError(t, json.Unmarshal(complete.Data, &result))
			require.Equal(t, `{"data":{}}`, result.Body)
		})
	}
}

func TestConfigureAfterRunStarts(t *testing.T) {
	tests := []struct {
		name       string
		started    bool
		expectedID string
	}{
		{name: "before the run starts", expectedID: "second"},
		{name: "after the run starts", started: true, expectedID: "first"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Setup(SetupOpts{})
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			cfg := p.resolveConfig(FnOpts{ID: "first"}, req)
			o := &requestOwner{
				r:        req,
				w:        newResponseWriter(httptest.NewRecorder()),
				provider: p,
				mgr:      sdkrequest.NewManager(sdkrequest.Opts{Mode: sdkrequest.StepModeManual}),
				config:   &cfg,
				started:  tt.started,
			}
			ctx := o.withConfigUpdater(context.Background())

			Configure(ctx, func(opts *FnOpts) {
				opts.ID = "second"
				opts.OmitResponseBody = true
			})

			require.Equal(t, tt.expectedID, o.config.ID)
			// other fields change at any time.
			require.True(t, o.config.OmitResponseBody)
		})
	}
}

func TestResponseStoredForKnownFunctions(t *testing.T) {
	tests := []struct {
		name string
		// middleware wraps with Provider.Middleware.  false wraps with
		// Provider.HandleFunc.
		middleware bool
		// configureBefore and configureAfter call Configure before or after the
		// handler writes its response.
		configureBefore bool
		configureAfter  bool
		expectRun       bool
		expectedBody    string
	}{
		{name: "HandleFunc", expectRun: true, expectedBody: "ok"},
		{name: "Middleware without Configure", middleware: true},
		{name: "Middleware and Configure before the response", middleware: true, configureBefore: true, expectRun: true, expectedBody: "ok"},
		{name: "Middleware and Configure after the response", middleware: true, configureAfter: true, expectRun: true, expectedBody: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
			}
			close(api.release)

			p := Setup(SetupOpts{})
			p.api = api

			configure := func(r *http.Request) {
				Configure(r.Context(), func(o *FnOpts) { o.ID = "configured" })
			}
			// the handler responds before its first step.
			next := func(w http.ResponseWriter, r *http.Request) {
				if tt.configureBefore {
					configure(r)
				}
				_, _ = w.Write([]byte("ok"))
				if tt.configureAfter {
					configure(r)
				}
				_, _ = step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
					return 1, nil
				})
			}

			var handler http.Handler = p.HandleFunc(FnOpts{}, next)
			if tt.middleware {
				handler = p.Middleware(MiddlewareOpts{})(http.HandlerFunc(next))
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))
			require.Equal(t, "ok", rec.Body.String())

			if !tt.expectRun {
				// a new run checkpoints in a tracked goroutine that sends to called
				// before it returns, so a run shows up in one of these checks.
				require.Zero(t, p.inflight.Load())
				require.Empty(t, api.called)
				require.Empty(t, rec.Header().Get(headerRunID))
				return
			}

			var steps []sdkrequest.GeneratorOpcode
			select {
			case steps = <-api.called:
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
			var result APIResult
			require.NoError(t, json.Unmarshal(steps[len(steps)-1].Data, &result))
			require.Equal(t, tt.expectedBody, result.Body)
		})
	}
}

func TestEmptyFunctionIDWarnsOncePerWrapper(t *testing.T) {
	tests := []struct {
		name string
		// wrappers is how many HandleFunc calls wrap the handler.  each gets
		// every path.
		wrappers         int
		id               string
		paths            []string
		expectedWarnings int
	}{
		{
			name:             "one wrapper and many URLs",
			wrappers:         1,
			paths:            []string{"/users/1", "/users/2", "/users/3"},
			expectedWarnings: 1,
		},
		{
			name:             "two wrappers",
			wrappers:         2,
			paths:            []string{"/users/1", "/users/2"},
			expectedWarnings: 2,
		},
		{
			name:             "a function ID",
			wrappers:         1,
			id:               "get-user",
			paths:            []string{"/users/1", "/users/2"},
			expectedWarnings: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 10),
				release: make(chan struct{}),
			}
			close(api.release)

			var logs bytes.Buffer
			p := Setup(SetupOpts{})
			p.api = api
			p.logger = slog.New(slog.NewTextHandler(&logs, nil))

			for range tt.wrappers {
				handler := p.HandleFunc(FnOpts{ID: tt.id, TrackAllRequests: true}, func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte("ok"))
				})
				for _, path := range tt.paths {
					handler(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
				}
			}

			require.Equal(t, tt.expectedWarnings, strings.Count(logs.String(), "api function has no ID"))
		})
	}
}

func TestConfigureChangesBodyLimits(t *testing.T) {
	const (
		requestBody  = `{"hello":"world"}`
		responseBody = "hello world"
	)

	tests := []struct {
		name string
		opts FnOpts
		// requestLimit and responseLimit are set by Configure after the handler
		// reads the request and writes the response.
		requestLimit     int
		responseLimit    int
		expectedRequest  string
		expectedResponse string
	}{
		{
			name:             "lower request limit",
			requestLimit:     5,
			expectedRequest:  requestBody[:5],
			expectedResponse: responseBody,
		},
		{
			name:             "lower response limit",
			responseLimit:    4,
			expectedRequest:  requestBody,
			expectedResponse: responseBody[:4],
		},
		{
			name:             "raised limits keep what was cut",
			opts:             FnOpts{MaxRequestBodySize: 5, MaxResponseBodySize: 4},
			requestLimit:     100,
			responseLimit:    100,
			expectedRequest:  requestBody[:5],
			expectedResponse: responseBody[:4],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
				inputs:  make(chan NewAPIRunData, 1),
			}
			close(api.release)

			p := Setup(SetupOpts{})
			p.api = api

			opts := tt.opts
			opts.TrackAllRequests = true
			handler := p.HandleFunc(opts, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				_, _ = w.Write([]byte(responseBody))
				Configure(r.Context(), func(o *FnOpts) {
					if tt.requestLimit > 0 {
						o.MaxRequestBodySize = tt.requestLimit
					}
					if tt.responseLimit > 0 {
						o.MaxResponseBodySize = tt.responseLimit
					}
				})
			})

			rec := httptest.NewRecorder()
			handler(rec, httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(requestBody)))
			require.Equal(t, responseBody, rec.Body.String())

			select {
			case input := <-api.inputs:
				require.Equal(t, tt.expectedRequest, string(input.Body))
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}

			steps := <-api.called
			var result APIResult
			require.NoError(t, json.Unmarshal(steps[len(steps)-1].Data, &result))
			require.Equal(t, tt.expectedResponse, result.Body)
		})
	}
}

func TestMiddlewareOptIn(t *testing.T) {
	const body = `{"hello":"world"}`

	tests := []struct {
		name string
		opts MiddlewareOpts
		// configure is the handler's Configure call.  nil does not opt in.
		configure func(o *FnOpts)
		// readFirst reads the request body before Configure.
		readFirst bool
		asyncStep bool

		expectedStatus int
		expectedBody   string
		expectRun      bool
		// expectedStored is the request body stored with the run.
		expectedStored string
		expectedLog    string
	}{
		{
			name:           "steps run without Configure",
			expectedStatus: http.StatusOK,
			expectedBody:   "1",
		},
		{
			name:           "Configure before reading the body",
			configure:      func(o *FnOpts) { o.ID = "fn" },
			expectedStatus: http.StatusOK,
			expectedBody:   "1",
			expectRun:      true,
			expectedStored: body,
		},
		{
			name:           "Configure after reading the body",
			configure:      func(o *FnOpts) { o.ID = "fn" },
			readFirst:      true,
			expectedStatus: http.StatusOK,
			expectedBody:   "1",
			expectRun:      true,
			expectedStored: "",
			expectedLog:    "read the request body before stephttp.Configure",
		},
		{
			name:           "RecordRequestBodies and Configure after reading the body",
			opts:           MiddlewareOpts{RecordRequestBodies: true},
			configure:      func(o *FnOpts) { o.ID = "fn" },
			readFirst:      true,
			expectedStatus: http.StatusOK,
			expectedBody:   "1",
			expectRun:      true,
			expectedStored: body,
		},
		{
			name: "RecordRequestBodies and OmitRequestBody",
			opts: MiddlewareOpts{RecordRequestBodies: true},
			configure: func(o *FnOpts) {
				o.ID = "fn"
				o.OmitRequestBody = true
			},
			readFirst:      true,
			expectedStatus: http.StatusOK,
			expectedBody:   "1",
			expectRun:      true,
			expectedStored: "",
		},
		{
			name: "Configure cannot raise the middleware buffer",
			opts: MiddlewareOpts{RecordRequestBodies: true, MaxRequestBodySize: 5},
			configure: func(o *FnOpts) {
				o.ID = "fn"
				o.MaxRequestBodySize = 100
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "1",
			expectRun:      true,
			expectedStored: body[:5],
		},
		{
			name:           "async step without Configure",
			asyncStep:      true,
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "Internal Server Error\n",
			expectedLog:    "ran an async step without stephttp.Configure",
		},
		{
			name:           "async step with Configure",
			configure:      func(o *FnOpts) { o.ID = "fn" },
			asyncStep:      true,
			expectedStatus: http.StatusSeeOther,
			expectRun:      true,
			expectedStored: body,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &blockingAPI{
				called:  make(chan []sdkrequest.GeneratorOpcode, 1),
				release: make(chan struct{}),
				inputs:  make(chan NewAPIRunData, 1),
			}
			close(api.release)

			var logs bytes.Buffer
			p := Setup(SetupOpts{})
			p.api = api
			p.logger = slog.New(slog.NewTextHandler(&logs, nil))

			handler := p.Middleware(tt.opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.configure != nil && !tt.readFirst {
					Configure(r.Context(), tt.configure)
				}
				_, _ = io.ReadAll(r.Body)
				if tt.configure != nil && tt.readFirst {
					Configure(r.Context(), tt.configure)
				}
				if tt.asyncStep {
					step.Sleep(r.Context(), "wait", time.Second)
				}
				n, _ := step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
					return 1, nil
				})
				_, _ = fmt.Fprint(w, n)
			}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body)))

			require.Equal(t, tt.expectedStatus, rec.Code)
			if tt.expectedBody != "" {
				require.Equal(t, tt.expectedBody, rec.Body.String())
			}
			if tt.expectedLog != "" {
				require.Contains(t, logs.String(), tt.expectedLog)
			}

			if !tt.expectRun {
				// a new run checkpoints in a tracked goroutine that sends to called
				// before it returns, so a run shows up in one of these checks.
				require.Zero(t, p.inflight.Load())
				require.Empty(t, api.called)
				require.Empty(t, rec.Header().Get(headerRunID))
				return
			}

			select {
			case input := <-api.inputs:
				require.Equal(t, "fn", input.Fn)
				require.Equal(t, tt.expectedStored, string(input.Body))
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
			_, err := ulid.Parse(rec.Header().Get(headerRunID))
			require.NoError(t, err)
		})
	}
}

func TestMiddlewareResumesWithoutConfigure(t *testing.T) {
	const signingKey = "signkey-test-12345678"
	t.Setenv("INNGEST_DEV", "")

	api := &blockingAPI{
		called:  make(chan []sdkrequest.GeneratorOpcode, 1),
		release: make(chan struct{}),
	}
	t.Cleanup(func() { close(api.release) })

	p := Setup(SetupOpts{Optional: OptionalSetupOpts{SigningKey: signingKey}})
	p.api = api

	// a resumed run opted in on its first request, so the handler does not need
	// Configure to continue it.
	handler := p.Middleware(MiddlewareOpts{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
			return 1, nil
		})
		_, _ = w.Write([]byte("ok"))
	}))

	runID := ulid.Make().String()
	sig, err := inngestgo.Sign(context.Background(), time.Now(), []byte(signingKey), []byte(runID))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.Header.Set(headerRunID, runID)
	req.Header.Set(headerSignature, sig)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusPartialContent, rec.Code)
	require.Equal(t, runID, rec.Header().Get(headerRunID))
	require.Empty(t, api.called)
}
