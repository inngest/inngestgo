package serverless

import (
	"context"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
)

// Introspection is an app's function configs, as Introspect reads them from
// the app.
type Introspection struct {
	AppID       string  `json:"app_id"`
	AppVersion  *string `json:"app_version,omitempty"`
	SDKLanguage string  `json:"sdk_language"`
	SDKVersion  string  `json:"sdk_version"`

	// Capabilities is the app's capabilities object as the app sent it, such
	// as {"in_band_sync":"v1","trust_probe":"v1"}.  it is nil when the app
	// sends none.  the Go SDK sends none.  the worker adds "connect":"v1" when
	// it sends the capabilities in the connect handshake.
	Capabilities json.RawMessage `json:"capabilities,omitempty"`

	// Functions is the JSON array of function configs, with each step URL
	// changed to the connect host.  fields that the Go SDK does not model
	// pass through unchanged.
	Functions json.RawMessage `json:"functions"`

	// FeatureObservations is protobuf JSON for the SDK features that the app
	// set up, such as AI metadata and extended traces.  it is nil when the
	// app sends none.  the Go SDK sends none.
	FeatureObservations json.RawMessage `json:"feature_observations,omitempty"`
}

// Introspect waits for the app at cfg.AppURL and reads its function configs.
// it does not register the app with Inngest.
func Introspect(ctx context.Context, cfg Config) (*Introspection, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	app, err := boot(ctx, cfg, signingKeys{primary: cfg.SigningKey, fallback: cfg.SigningKeyFallback})
	if err != nil {
		return nil, err
	}

	var observations json.RawMessage
	if app.FeatureObservations != nil {
		if observations, err = protojson.Marshal(app.FeatureObservations); err != nil {
			return nil, err
		}
	}

	return &Introspection{
		AppID:               app.ID,
		AppVersion:          app.Version,
		SDKLanguage:         app.SDK.Language,
		SDKVersion:          app.SDK.Version,
		Capabilities:        app.RawCapabilities,
		Functions:           app.Functions,
		FeatureObservations: observations,
	}, nil
}
