package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkfeatureobs "github.com/inngest/inngest/proto/gen/sdk_feature_observations/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
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

func TestReadAppConfigWithGoSDK(t *testing.T) {
	r := require.New(t)

	h, err := newGoApp()
	r.NoError(err)
	server := httptest.NewServer(h)
	defer server.Close()

	appURL := server.URL + "/api/inngest"
	app, err := readAppConfig(context.Background(), server.Client(), appURL, signingKeys{primary: testSigningKey}, false)
	r.NoError(err)

	r.Equal(testAppID, app.ID)
	r.Equal("go", app.SDK.Language)
	r.Contains(app.stepURLs, testFnSlug)
	r.Contains(app.stepURLs[testFnSlug], server.URL)

	var fns []fnConfig
	r.NoError(json.Unmarshal(app.Functions, &fns))
	requireConnectStepURLs(t, fns)
}

// jsFeatureObservations is the featureObservations object a JS app sends in
// its sync response.  ts-proto writes enum names, not numbers.  futureFeature
// is a field the vendored proto does not have.
const jsFeatureObservations = `{
	"aiMetadataExtraction": {
		"readinessReason": "AI_METADATA_EXTRACTION_READINESS_REASON_READY",
		"otelSetup": {"providerFound": true}
	},
	"futureFeature": {"enabled": true}
}`

// newJSApp answers the readiness GET and an in-band sync PUT as a JS app in
// dev mode does, with an unsigned sync response that has observations.
func newJSApp(observations string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set(headerSDK, "js:v3.40.0")
		if req.Method != http.MethodPut {
			return
		}
		w.Header().Set(headerSyncKind, syncKindInBand)
		body := `{"app_id":"app","sdk_language":"js","sdk_version":"3.40.0","functions":[]`
		if observations != "" {
			body += `,"featureObservations":` + observations
		}
		_, _ = w.Write([]byte(body + `}`))
	}
}

func TestReadAppConfigFeatureObservations(t *testing.T) {
	tests := []struct {
		name         string
		observations string
		want         *sdkfeatureobs.FeatureObservations
		wantErr      string
	}{
		{
			name: "absent",
		},
		{
			name:         "null",
			observations: `null`,
		},
		{
			name:         "JS observations drop unknown fields",
			observations: jsFeatureObservations,
			want: &sdkfeatureobs.FeatureObservations{
				AiMetadataExtraction: &sdkfeatureobs.AIMetadataExtraction{
					ReadinessReason: sdkfeatureobs.AIMetadataExtractionReadinessReason_AI_METADATA_EXTRACTION_READINESS_REASON_READY,
					OtelSetup:       &sdkfeatureobs.OTelSetup{ProviderFound: true},
				},
			},
		},
		{
			name:         "not an object",
			observations: `"ready"`,
			wantErr:      "could not decode feature observations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			server := httptest.NewServer(newJSApp(tt.observations))
			defer server.Close()

			app, err := readAppConfig(context.Background(), server.Client(), server.URL, signingKeys{primary: testSigningKey}, true)
			if tt.wantErr != "" {
				r.ErrorContains(err, tt.wantErr)
				return
			}
			r.NoError(err)
			r.True(proto.Equal(tt.want, app.FeatureObservations), "got %v", app.FeatureObservations)
		})
	}
}

func TestReadAppConfigErrors(t *testing.T) {
	unsigned := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerSyncKind, syncKindInBand)
		_, _ = w.Write([]byte(`{"app_id":"app","functions":[]}`))
	}

	tests := []struct {
		name          string
		handler       http.HandlerFunc
		allowUnsigned bool
		wantErr       string
	}{
		{
			name:    "unsigned response",
			handler: unsigned,
			wantErr: "sync response has no X-Inngest-Signature header",
		},
		{
			name:          "unsigned response in dev",
			handler:       unsigned,
			allowUnsigned: true,
		},
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

			_, err := readAppConfig(context.Background(), server.Client(), server.URL, signingKeys{primary: testSigningKey}, tt.allowUnsigned)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
