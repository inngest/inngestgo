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

func TestStart(t *testing.T) {
	const (
		signingKey  = "signkey-test-12345678"
		requestBody = `{"hello":"world"}`
	)

	tests := []struct {
		name string
		// resume sends a signed request from Inngest that resumes a run.
		resume      bool
		getStepsErr error
		// handler runs after Start with the w and r that Start returns.
		handler        func(w http.ResponseWriter, r *http.Request)
		expectedStatus int
		expectRun      bool
		// check runs on the response and the ops of the new run.
		check func(t *testing.T, rec *httptest.ResponseRecorder, input NewAPIRunData, steps []sdkrequest.GeneratorOpcode)
	}{
		{
			name: "finished run",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				_, _ = step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
					return 1, nil
				})
				_, _ = w.Write([]byte("ok"))
			},
			expectedStatus: http.StatusOK,
			expectRun:      true,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, input NewAPIRunData, steps []sdkrequest.GeneratorOpcode) {
				require.Equal(t, "ok", rec.Body.String())
				_, err := ulid.Parse(rec.Header().Get(headerRunID))
				require.NoError(t, err)
				require.Equal(t, requestBody, string(input.Body))

				require.Len(t, steps, 2)
				require.Equal(t, enums.OpcodeStepRun, steps[0].Op)
				complete := steps[1]
				require.Equal(t, enums.OpcodeRunComplete, complete.Op)
				var result APIResult
				require.NoError(t, json.Unmarshal(complete.Data, &result))
				require.Equal(t, "ok", result.Body)
			},
		},
		{
			name: "async step",
			handler: func(w http.ResponseWriter, r *http.Request) {
				step.Sleep(r.Context(), "wait", time.Second)
				_, _ = w.Write([]byte("done"))
			},
			expectedStatus: http.StatusSeeOther,
			expectRun:      true,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, input NewAPIRunData, steps []sdkrequest.GeneratorOpcode) {
				require.NotEmpty(t, rec.Header().Get("Location"))
				require.Len(t, steps, 1)
				require.Equal(t, enums.OpcodeSleep, steps[0].Op)
			},
		},
		{
			name: "panic",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
					return 1, nil
				})
				panic("kaboom")
			},
			expectedStatus: http.StatusInternalServerError,
			expectRun:      true,
			check: func(t *testing.T, rec *httptest.ResponseRecorder, input NewAPIRunData, steps []sdkrequest.GeneratorOpcode) {
				complete := steps[len(steps)-1]
				require.Equal(t, enums.OpcodeRunComplete, complete.Op)
				var result APIResult
				require.NoError(t, json.Unmarshal(complete.Data, &result))
				require.Equal(t, http.StatusInternalServerError, result.Status)
				require.Contains(t, result.Error, "function panicked: kaboom")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("INNGEST_DEV", "")

			api := &blockingAPI{
				called:      make(chan []sdkrequest.GeneratorOpcode, 1),
				release:     make(chan struct{}),
				inputs:      make(chan NewAPIRunData, 1),
				getStepsErr: tt.getStepsErr,
			}
			close(api.release)

			p := Setup(SetupOpts{
				Optional: OptionalSetupOpts{SigningKey: signingKey},
			})
			p.api = api

			handler := func(w http.ResponseWriter, r *http.Request) {
				w, r, end := p.Start(w, r, FnOpts{ID: "start-fn"})
				defer end()
				tt.handler(w, r)
			}

			req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(requestBody))
			if tt.resume {
				runID := ulid.Make().String()
				sig, err := inngestgo.Sign(context.Background(), time.Now(), []byte(signingKey), []byte(runID))
				require.NoError(t, err)
				req.Header.Set(headerRunID, runID)
				req.Header.Set(headerSignature, sig)
			}

			rec := httptest.NewRecorder()
			handler(rec, req)

			require.Equal(t, tt.expectedStatus, rec.Code)

			if !tt.expectRun {
				// a new run checkpoints in a tracked goroutine that sends to called
				// before it returns, so a run shows up in one of these checks.
				require.Zero(t, p.inflight.Load())
				require.Empty(t, api.called)
				return
			}

			var input NewAPIRunData
			select {
			case input = <-api.inputs:
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint was not sent")
			}
			tt.check(t, rec, input, <-api.called)
		})
	}
}

func TestStartEnd(t *testing.T) {
	t.Run("end releases the request once", func(t *testing.T) {
		p := Setup(SetupOpts{})
		p.api = &blockingAPI{called: make(chan []sdkrequest.GeneratorOpcode, 1), release: make(chan struct{})}

		_, _, end := p.Start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil), FnOpts{})

		// Wait must not return while the request is open.
		require.EqualValues(t, 1, p.inflight.Load())

		end()
		end()
		require.Zero(t, p.inflight.Load())
	})

	t.Run("a second end lets a handler panic continue", func(t *testing.T) {
		p := Setup(SetupOpts{})
		p.api = &blockingAPI{called: make(chan []sdkrequest.GeneratorOpcode, 1), release: make(chan struct{})}

		require.PanicsWithValue(t, "kaboom", func() {
			_, _, end := p.Start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil), FnOpts{})
			end()
			defer end()
			panic("kaboom")
		})
		require.Zero(t, p.inflight.Load())
	})
}

func TestStartEmptyIDWarnsOnce(t *testing.T) {
	api := &blockingAPI{
		called:  make(chan []sdkrequest.GeneratorOpcode, 10),
		release: make(chan struct{}),
	}
	close(api.release)

	var logs bytes.Buffer
	p := Setup(SetupOpts{})
	p.api = api
	p.logger = slog.New(slog.NewTextHandler(&logs, nil))

	for _, path := range []string{"/users/1", "/users/2", "/users/3"} {
		func() {
			w, _, end := p.Start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil), FnOpts{TrackAllRequests: true})
			defer end()
			_, _ = w.Write([]byte("ok"))
		}()
	}

	require.Equal(t, 1, strings.Count(logs.String(), "api function has no ID"))
}

func TestStartFailedResume(t *testing.T) {
	const signingKey = "signkey-test-12345678"

	tests := []struct {
		name string
		// handler runs after Start with the w and r that Start returns.  ran
		// records code that must not run.
		handler func(w http.ResponseWriter, r *http.Request, ran *bool)
		// expectPanicLog is true when the handler panics.  a step that stops is
		// not a panic in the log.
		expectPanicLog bool
	}{
		{
			name: "a step does not run",
			handler: func(w http.ResponseWriter, r *http.Request, ran *bool) {
				_, _ = step.Run(r.Context(), "a", func(ctx context.Context) (int, error) {
					*ran = true
					return 1, nil
				})
				*ran = true
			},
		},
		{
			name: "an async step does not run",
			handler: func(w http.ResponseWriter, r *http.Request, ran *bool) {
				step.Sleep(r.Context(), "wait", time.Second)
				*ran = true
			},
		},
		{
			name: "code outside steps gets a cancelled context and its writes are dropped",
			handler: func(w http.ResponseWriter, r *http.Request, ran *bool) {
				*ran = r.Context().Err() == nil
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte("ok"))
			},
		},
		{
			name: "a panic is logged",
			handler: func(w http.ResponseWriter, r *http.Request, ran *bool) {
				panic("kaboom")
			},
			expectPanicLog: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("INNGEST_DEV", "")

			api := &blockingAPI{
				called:      make(chan []sdkrequest.GeneratorOpcode, 1),
				release:     make(chan struct{}),
				getStepsErr: fmt.Errorf("api unavailable"),
			}
			close(api.release)

			var logs bytes.Buffer
			p := Setup(SetupOpts{Optional: OptionalSetupOpts{SigningKey: signingKey}})
			p.api = api
			p.logger = slog.New(slog.NewTextHandler(&logs, nil))

			ran := false
			handler := func(w http.ResponseWriter, r *http.Request) {
				w, r, end := p.Start(w, r, FnOpts{ID: "start-fn"})
				defer end()
				tt.handler(w, r, &ran)
			}

			runID := ulid.Make().String()
			sig, err := inngestgo.Sign(context.Background(), time.Now(), []byte(signingKey), []byte(runID))
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			req.Header.Set(headerRunID, runID)
			req.Header.Set(headerSignature, sig)

			rec := httptest.NewRecorder()
			require.NotPanics(t, func() { handler(rec, req) })

			require.False(t, ran)
			require.Equal(t, http.StatusInternalServerError, rec.Code)
			require.Equal(t, "error loading run state\n", rec.Body.String())
			require.Contains(t, logs.String(), "error loading steps")
			require.Equal(t, tt.expectPanicLog, strings.Contains(logs.String(), "api handler panicked"))
			require.Zero(t, p.inflight.Load())
			require.Empty(t, api.called)
		})
	}
}
