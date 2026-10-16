package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteStepURLs(t *testing.T) {
	tests := []struct {
		name         string
		functions    string
		wantErr      string
		wantFns      string
		wantStepURLs map[string]string
	}{
		{
			name: "keeps every other field",
			functions: `[{
				"id": "app-fn",
				"name": "fn",
				"jsOnly": {"nested": [1, 2]},
				"steps": {"step": {
					"id": "step",
					"retries": {"attempts": 3},
					"runtime": {"type": "http", "url": "http://127.0.0.1:3000/api/inngest?fnId=app-fn&step=step"}
				}}
			}]`,
			wantFns: `[{
				"id": "app-fn",
				"name": "fn",
				"jsOnly": {"nested": [1, 2]},
				"steps": {"step": {
					"id": "step",
					"retries": {"attempts": 3},
					"runtime": {"type": "http", "url": "ws://connect?fnId=app-fn&step=step"}
				}}
			}]`,
			wantStepURLs: map[string]string{
				"app-fn": "http://127.0.0.1:3000/api/inngest?fnId=app-fn&step=step",
			},
		},
		{
			name:      "function without id",
			functions: `[{"steps": {}}]`,
			wantErr:   "function config 0 has no id",
		},
		{
			name:      "step without url",
			functions: `[{"id": "app-fn", "steps": {"step": {"runtime": {}}}}]`,
			wantErr:   `step "step" in function "app-fn" has no valid url`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)

			var functions []json.RawMessage
			r.NoError(json.Unmarshal([]byte(tt.functions), &functions))

			fns, stepURLs, err := rewriteStepURLs(functions)
			if tt.wantErr != "" {
				r.ErrorContains(err, tt.wantErr)
				return
			}
			r.NoError(err)
			assert.JSONEq(t, tt.wantFns, string(fns))
			r.Equal(tt.wantStepURLs, stepURLs)
		})
	}
}

func TestSyncAppWithGoSDK(t *testing.T) {
	r := require.New(t)

	h, err := newGoApp()
	r.NoError(err)
	server := httptest.NewServer(h)
	defer server.Close()

	appURL := server.URL + "/api/inngest"
	app, err := syncApp(context.Background(), server.Client(), appURL, signingKeys{primary: testSigningKey})
	r.NoError(err)

	r.Equal(testAppID, app.ID)
	r.Equal("go", app.SDK.Language)
	r.Contains(app.stepURLs, testFnSlug)
	r.Contains(app.stepURLs[testFnSlug], server.URL)

	var fns []syncedFn
	r.NoError(json.Unmarshal(app.Functions, &fns))
	requireConnectStepURLs(t, fns)
}

func TestSyncAppErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name: "out-of-band sync",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"message":"ok"}`))
			},
			wantErr: "app did not answer with an in-band sync",
		},
		{
			name: "bad signature",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(headerSyncKind, syncKindInBand)
				w.Header().Set(headerSignature, "t=1&s=bad")
				_, _ = w.Write([]byte(`{"app_id":"app","functions":[]}`))
			},
			wantErr: "sync response signature does not match",
		},
		{
			name: "error status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			},
			wantErr: "sync request returned status 401",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			_, err := syncApp(context.Background(), server.Client(), server.URL, signingKeys{primary: testSigningKey})
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
