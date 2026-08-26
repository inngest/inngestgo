package sdkrequest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadFromAPI(t *testing.T) {
	t.Run("hydrates events and steps", func(t *testing.T) {
		var paths sync.Map
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer primary", r.Header.Get("Authorization"))
			paths.Store(r.URL.Path, true)

			switch r.URL.Path {
			case "/v0/runs/run-id/batch":
				_, _ = w.Write([]byte(`[{"name":"one"},{"name":"two"}]`))
			case "/v0/runs/run-id/actions":
				_, _ = w.Write([]byte(`{"step-id":{"value":true}}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		request := &Request{
			Event:  json.RawMessage(`{"name":"one"}`),
			UseAPI: true,
			CallCtx: CallCtx{
				RunID: "run-id",
			},
		}

		err := LoadFromAPI(context.Background(), request, LoadFromAPIOpts{
			APIBaseURL: server.URL,
			AuthToken:  "primary",
		})
		require.NoError(t, err)
		require.Len(t, request.Events, 2)
		assert.JSONEq(t, `{"name":"one"}`, string(request.Events[0]))
		assert.JSONEq(t, `{"name":"two"}`, string(request.Events[1]))
		require.Contains(t, request.Steps, "step-id")
		assert.JSONEq(t, `{"value":true}`, string(request.Steps["step-id"]))
		assert.JSONEq(t, `{"name":"one"}`, string(request.Event))

		_, fetchedBatch := paths.Load("/v0/runs/run-id/batch")
		_, fetchedSteps := paths.Load("/v0/runs/run-id/actions")
		assert.True(t, fetchedBatch)
		assert.True(t, fetchedSteps)
	})

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status)+" uses fallback key", func(t *testing.T) {
			var (
				mu          sync.Mutex
				authHeaders = map[string][]string{}
			)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				authHeaders[r.URL.Path] = append(authHeaders[r.URL.Path], r.Header.Get("Authorization"))
				mu.Unlock()

				if r.Header.Get("Authorization") == "Bearer primary" {
					w.WriteHeader(status)
					return
				}

				switch r.URL.Path {
				case "/v0/runs/run-id/batch":
					_, _ = w.Write([]byte(`[]`))
				case "/v0/runs/run-id/actions":
					_, _ = w.Write([]byte(`{}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			request := &Request{UseAPI: true, CallCtx: CallCtx{RunID: "run-id"}}
			err := LoadFromAPI(context.Background(), request, LoadFromAPIOpts{
				APIBaseURL:        server.URL,
				AuthToken:         "primary",
				AuthTokenFallback: "fallback",
			})
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			expected := []string{"Bearer primary", "Bearer fallback"}
			assert.Equal(t, expected, authHeaders["/v0/runs/run-id/batch"])
			assert.Equal(t, expected, authHeaders["/v0/runs/run-id/actions"])
		})
	}
}

func TestLoadFromAPIFailureDoesNotPartiallyHydrate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/runs/run-id/batch":
			_, _ = w.Write([]byte(`[{"name":"new"}]`))
		case "/v0/runs/run-id/actions":
			http.Error(w, `{"error":"unavailable"}`, http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	request := &Request{
		Events: []json.RawMessage{json.RawMessage(`{"name":"original"}`)},
		Steps:  map[string]json.RawMessage{"original": json.RawMessage(`true`)},
		UseAPI: true,
		CallCtx: CallCtx{
			RunID: "run-id",
		},
	}

	err := LoadFromAPI(context.Background(), request, LoadFromAPIOpts{APIBaseURL: server.URL})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to retrieve")
	require.Len(t, request.Events, 1)
	assert.JSONEq(t, `{"name":"original"}`, string(request.Events[0]))
	require.Contains(t, request.Steps, "original")
}

func TestLoadFromAPIRequiresRunID(t *testing.T) {
	err := LoadFromAPI(context.Background(), &Request{UseAPI: true}, LoadFromAPIOpts{})
	require.EqualError(t, err, "cannot retrieve request data from API without a run ID")
}
