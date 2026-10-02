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
		expectStartErr bool
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
		{
			name:        "resume that cannot load its steps",
			resume:      true,
			getStepsErr: fmt.Errorf("api unavailable"),
			handler: func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("the handler ran after Start returned an error")
			},
			expectStartErr: true,
			expectedStatus: http.StatusInternalServerError,
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

			var startErr error
			handler := func(w http.ResponseWriter, r *http.Request) {
				w, r, end, err := p.Start(w, r, FnOpts{ID: "start-fn"})
				defer end()
				if err != nil {
					startErr = err
					return
				}
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
			require.Equal(t, tt.expectStartErr, startErr != nil)

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

		_, _, end, err := p.Start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil), FnOpts{})
		require.NoError(t, err)

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
			_, _, end, err := p.Start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil), FnOpts{})
			require.NoError(t, err)
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
			w, _, end, err := p.Start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil), FnOpts{TrackAllRequests: true})
			defer end()
			require.NoError(t, err)
			_, _ = w.Write([]byte("ok"))
		}()
	}

	require.Equal(t, 1, strings.Count(logs.String(), "api function has no ID"))
}

func TestStartErrorReleasesInflight(t *testing.T) {
	const signingKey = "signkey-test-12345678"

	tests := []struct {
		name string
		// deferFirst defers end before it checks the error.  false returns on the
		// error before it defers end.
		deferFirst bool
	}{
		{name: "defer end before the error check", deferFirst: true},
		{name: "return on the error before defer end"},
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

			p := Setup(SetupOpts{Optional: OptionalSetupOpts{SigningKey: signingKey}})
			p.api = api

			handler := func(w http.ResponseWriter, r *http.Request) {
				w, r, end, err := p.Start(w, r, FnOpts{ID: "start-fn"})
				if tt.deferFirst {
					defer end()
				}
				if err != nil {
					return
				}
				defer end()
				_, _ = w.Write([]byte("ok"))
				_ = r
			}

			runID := ulid.Make().String()
			sig, err := inngestgo.Sign(context.Background(), time.Now(), []byte(signingKey), []byte(runID))
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/test", nil)
			req.Header.Set(headerRunID, runID)
			req.Header.Set(headerSignature, sig)

			rec := httptest.NewRecorder()
			handler(rec, req)

			require.Equal(t, http.StatusInternalServerError, rec.Code)
			require.Zero(t, p.inflight.Load())
		})
	}
}
