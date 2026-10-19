package serverless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	sdkfeatureobs "github.com/inngest/inngest/proto/gen/sdk_feature_observations/v1"
	"github.com/inngest/inngestgo/internal/types"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	headerContentType = "Content-Type"
	headerSignature   = "X-Inngest-Signature"
	headerSyncKind    = "x-inngest-sync-kind"
	headerSDK         = "X-Inngest-SDK"
	headerNoRetry     = "X-Inngest-No-Retry"
	headerRetryAfter  = "Retry-After"
	headerReqVersion  = "x-inngest-req-version"
	headerRunID       = "X-Run-ID"
	headerRequestID   = "x-request-id"
	headerJobID       = "x-inngest-job-id"
	headerStepID      = "X-Inngest-Step-ID"
	headerAccountID   = "account-id"
	headerTraceParent = "traceparent"
	headerTraceState  = "tracestate"

	syncKindInBand = "in_band"

	// connectStepHost is the host that connect apps use in step URLs.  the
	// executor sends requests for these steps over the gateway, not HTTP.
	connectStepHost = "connect"
)

// syncedApp is what the daemon learns from the app's in-band sync.
type syncedApp struct {
	ID         string
	Version    *string
	SDK        sdkInfo
	Capability types.Capabilities

	// Functions is the JSON array of function configs with each step URL
	// changed to the connect host.
	Functions json.RawMessage

	// FeatureObservations tells the gateway which optional SDK features the
	// app set up, such as AI metadata and extended traces.  it is nil when the
	// app sends none.  the Go SDK sends none.
	FeatureObservations *sdkfeatureobs.FeatureObservations

	// stepURLs holds the app's own step URL for each function slug.  the
	// forwarder posts requests for that function to it.
	stepURLs map[string]string
}

type sdkInfo struct {
	Language string
	Version  string
}

// inBandSyncResponse holds the fields of an in-band sync response that the
// daemon uses.  the Go and JS SDKs both send these.
type inBandSyncResponse struct {
	AppID        string             `json:"app_id"`
	AppVersion   *string            `json:"appVersion"`
	Capabilities types.Capabilities `json:"capabilities"`
	Functions    []json.RawMessage  `json:"functions"`
	SDKLanguage  string             `json:"sdk_language"`
	SDKVersion   string             `json:"sdk_version"`
	// FeatureObservations is protobuf JSON for sdkfeatureobs.FeatureObservations.
	FeatureObservations json.RawMessage `json:"featureObservations"`
}

// syncApp sends a signed in-band sync request to the app and reads its
// function configs.  the response stays in the daemon.  if it went to the API,
// the app would sync as an HTTP app.  the response sets the URL of each request
// the daemon forwards, so it must carry a valid signature unless allowUnsigned
// is set.  a JS app in dev mode does not sign it.
func syncApp(ctx context.Context, client *http.Client, appURL string, keys signingKeys, allowUnsigned bool) (*syncedApp, error) {
	body, err := json.Marshal(map[string]string{"url": appURL})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, appURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set(headerContentType, "application/json")
	req.Header.Set(headerSyncKind, syncKindInBand)
	sig, err := keys.sign(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("could not sign sync request: %w", err)
	}
	if sig != "" {
		req.Header.Set(headerSignature, sig)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not send sync request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not read sync response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sync request returned status %d: %s", resp.StatusCode, respBody)
	}
	if kind := resp.Header.Get(headerSyncKind); kind != syncKindInBand {
		return nil, fmt.Errorf("app did not answer with an in-band sync (%s %q).  set INNGEST_ALLOW_IN_BAND_SYNC=1 or upgrade the SDK", headerSyncKind, kind)
	}
	switch sig := resp.Header.Get(headerSignature); {
	case sig != "":
		if !keys.validResponse(ctx, sig, respBody) {
			return nil, fmt.Errorf("sync response signature does not match the signing key")
		}
	case !allowUnsigned:
		return nil, fmt.Errorf("sync response has no %s header.  the app must not run in dev mode unless Dev is set", headerSignature)
	}

	var parsed inBandSyncResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("could not decode sync response: %w", err)
	}
	if parsed.AppID == "" {
		return nil, fmt.Errorf("sync response has no app_id")
	}

	fns, stepURLs, err := rewriteStepURLs(parsed.Functions)
	if err != nil {
		return nil, err
	}
	observations, err := decodeFeatureObservations(parsed.FeatureObservations)
	if err != nil {
		return nil, fmt.Errorf("could not decode feature observations: %w", err)
	}

	return &syncedApp{
		ID:                  parsed.AppID,
		Version:             parsed.AppVersion,
		SDK:                 sdkInfo{Language: parsed.SDKLanguage, Version: parsed.SDKVersion},
		Capability:          parsed.Capabilities,
		Functions:           fns,
		FeatureObservations: observations,
		stepURLs:            stepURLs,
	}, nil
}

// decodeFeatureObservations reads the featureObservations object of a sync
// response as the API does for an HTTP app.  it drops fields that the vendored
// proto does not have, so a newer SDK does not fail the sync.
func decodeFeatureObservations(raw json.RawMessage) (*sdkfeatureobs.FeatureObservations, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	observations := &sdkfeatureobs.FeatureObservations{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, observations); err != nil {
		return nil, err
	}
	return observations, nil
}

// rewriteStepURLs changes each step URL to the connect host and keeps the
// query, which holds fnId and the step.  every other field passes through
// unchanged, so config the Go SDK does not model still reaches the gateway.
func rewriteStepURLs(functions []json.RawMessage) (json.RawMessage, map[string]string, error) {
	stepURLs := make(map[string]string, len(functions))
	out := make([]map[string]json.RawMessage, len(functions))

	for i, raw := range functions {
		var fn map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fn); err != nil {
			return nil, nil, fmt.Errorf("could not decode function config %d: %w", i, err)
		}

		var slug string
		if err := json.Unmarshal(fn["id"], &slug); err != nil || slug == "" {
			return nil, nil, fmt.Errorf("function config %d has no id", i)
		}

		var steps map[string]map[string]json.RawMessage
		if err := json.Unmarshal(fn["steps"], &steps); err != nil {
			return nil, nil, fmt.Errorf("could not decode steps of function %q: %w", slug, err)
		}

		for stepID, step := range steps {
			var runtime map[string]any
			if err := json.Unmarshal(step["runtime"], &runtime); err != nil {
				return nil, nil, fmt.Errorf("could not decode runtime of step %q in function %q: %w", stepID, slug, err)
			}
			appStepURL, _ := runtime["url"].(string)
			parsed, err := url.Parse(appStepURL)
			if err != nil || appStepURL == "" {
				return nil, nil, fmt.Errorf("step %q in function %q has no valid url", stepID, slug)
			}

			stepURLs[slug] = appStepURL
			runtime["url"] = (&url.URL{
				Scheme:   "ws",
				Host:     connectStepHost,
				RawQuery: parsed.RawQuery,
			}).String()

			encoded, err := json.Marshal(runtime)
			if err != nil {
				return nil, nil, err
			}
			step["runtime"] = encoded
			steps[stepID] = step
		}

		encodedSteps, err := json.Marshal(steps)
		if err != nil {
			return nil, nil, err
		}
		fn["steps"] = encodedSteps
		out[i] = fn
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}
	return encoded, stepURLs, nil
}
