package serverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validConfig() Config {
	return Config{
		AppURL:      "http://127.0.0.1:3000/api/inngest",
		Multiplex:   1,
		IdleTimeout: defaultIdleTimeout,
		SigningKey:  testSigningKey,
		APIBaseURL:  "https://api.inngest.com",
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		change  func(c *Config)
		wantErr string
	}{
		{name: "valid", change: func(*Config) {}},
		{name: "no app URL", change: func(c *Config) { c.AppURL = "" }, wantErr: `app URL "" must be an http or https URL`},
		{name: "app URL without host", change: func(c *Config) { c.AppURL = "/api/inngest" }, wantErr: "must be an http or https URL"},
		{name: "app URL with another scheme", change: func(c *Config) { c.AppURL = "ws://127.0.0.1:3000" }, wantErr: "must be an http or https URL"},
		{name: "multiplex too high", change: func(c *Config) { c.Multiplex = 256 }, wantErr: "multiplex 256"},
		{name: "idle too long", change: func(c *Config) { c.IdleTimeout = 16 * time.Minute }, wantErr: "idle timeout 16m0s"},
		{name: "negative max lifetime", change: func(c *Config) { c.MaxLifetime = -time.Second }, wantErr: "max lifetime -1s must not be negative"},
		{name: "negative max requests", change: func(c *Config) { c.MaxRequests = -1 }, wantErr: "max requests -1 must not be negative"},
		{name: "no signing key", change: func(c *Config) { c.SigningKey = "" }, wantErr: "signing key is required"},
		{name: "dev needs no signing key", change: func(c *Config) { c.SigningKey = ""; c.Dev = true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.change(&c)
			err := c.validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestSyncReturnsRewrittenConfigs(t *testing.T) {
	r := require.New(t)

	h, err := newGoApp()
	r.NoError(err)
	server := httptest.NewServer(h)
	defer server.Close()

	cfg := validConfig()
	cfg.AppURL = server.URL + "/api/inngest"
	cfg.Logger = discardLogger()

	byt, err := Sync(context.Background(), cfg)
	r.NoError(err)

	var out struct {
		AppID       string     `json:"app_id"`
		SDKLanguage string     `json:"sdk_language"`
		Functions   []syncedFn `json:"functions"`
	}
	r.NoError(json.Unmarshal(byt, &out), "sync output: %s", byt)
	r.Equal(testAppID, out.AppID)
	r.Equal("go", out.SDKLanguage)
	requireConnectStepURLs(t, out.Functions)
	r.NotContains(string(byt), "feature_observations")
}

func TestSyncReturnsFeatureObservations(t *testing.T) {
	r := require.New(t)

	server := httptest.NewServer(newJSApp(jsFeatureObservations))
	defer server.Close()

	// a JS app in dev mode does not sign its sync response.
	cfg := validConfig()
	cfg.AppURL = server.URL
	cfg.Dev = true
	cfg.Logger = discardLogger()

	byt, err := Sync(context.Background(), cfg)
	r.NoError(err)

	var out struct {
		FeatureObservations json.RawMessage `json:"feature_observations"`
	}
	r.NoError(json.Unmarshal(byt, &out), "sync output: %s", byt)
	r.JSONEq(`{
		"aiMetadataExtraction": {
			"readinessReason": "AI_METADATA_EXTRACTION_READINESS_REASON_READY",
			"otelSetup": {"providerFound": true}
		}
	}`, string(out.FeatureObservations))
}

func TestRunFailsWhenAppNeverAnswers(t *testing.T) {
	r := require.New(t)

	// nothing listens on port 1.
	cfg := validConfig()
	cfg.AppURL = "http://127.0.0.1:1/api/inngest"
	cfg.BootTimeout = 300 * time.Millisecond
	cfg.Logger = discardLogger()

	start := time.Now()
	err := Run(context.Background(), cfg)
	r.ErrorContains(err, "app not ready")
	r.Less(time.Since(start), 5*time.Second)
}

func TestRunStopsConnectingWhenCanceled(t *testing.T) {
	r := require.New(t)

	h, err := newGoApp()
	r.NoError(err)
	app := httptest.NewServer(h)
	defer app.Close()

	// the API never answers the connect start request.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-req.Context().Done()
	}))
	defer api.Close()

	cfg := validConfig()
	cfg.AppURL = app.URL + "/api/inngest"
	cfg.APIBaseURL = api.URL
	cfg.Logger = discardLogger()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)

	start := time.Now()
	err = Run(ctx, cfg)
	r.Error(err)
	r.Less(time.Since(start), 5*time.Second)
}

func TestRunNeedsAPIBaseURL(t *testing.T) {
	cfg := validConfig()
	cfg.APIBaseURL = ""
	require.EqualError(t, Run(context.Background(), cfg), "missing API base URL")
}

func TestWaitForExit(t *testing.T) {
	tests := []struct {
		name string
		// setup returns the admission, the deadline, and a ctx that the test
		// may cancel.
		setup func(t *testing.T) (*admission, time.Time, context.Context)
	}{
		{
			name: "max lifetime",
			setup: func(t *testing.T) (*admission, time.Time, context.Context) {
				deadline := time.Now().Add(100 * time.Millisecond)
				adm := newAdmission(2, 0, deadline)
				require.Equal(t, admitted, adm.admit())
				return adm, deadline, context.Background()
			},
		},
		{
			name: "max requests",
			setup: func(t *testing.T) (*admission, time.Time, context.Context) {
				adm := newAdmission(2, 1, time.Time{})
				require.Equal(t, admitted, adm.admit())
				return adm, time.Time{}, context.Background()
			},
		},
		{
			name: "ctx ends",
			setup: func(t *testing.T) (*admission, time.Time, context.Context) {
				adm := newAdmission(2, 0, time.Time{})
				require.Equal(t, admitted, adm.admit())
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return adm, time.Time{}, ctx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := require.New(t)
			adm, deadline, ctx := tt.setup(t)
			cfg := validConfig()
			cfg.IdleTimeout = time.Minute
			cfg.Logger = discardLogger()

			done := make(chan struct{})
			go func() {
				waitForExit(ctx, cfg, adm, deadline)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("waitForExit did not return")
			}

			// the request admitted in setup is still in flight, and no new
			// request starts.
			r.True(adm.draining())
			r.Equal(1, adm.inFlight())
			r.Equal(rejectDraining, adm.admit())
		})
	}
}

func TestWaitInFlight(t *testing.T) {
	t.Run("waits for every request until ctx ends", func(t *testing.T) {
		r := require.New(t)
		adm := newAdmission(1, 0, time.Time{})
		r.Equal(admitted, adm.admit())
		adm.drain()

		// the request runs longer than the drain timeout, and ctx never ends.
		go func() {
			time.Sleep(300 * time.Millisecond)
			adm.release()
		}()
		start := time.Now()
		waitInFlight(context.Background(), adm, 50*time.Millisecond, time.Hour)
		r.GreaterOrEqual(time.Since(start), 250*time.Millisecond)
		r.Equal(0, adm.inFlight())
	})

	t.Run("stops at maxWait", func(t *testing.T) {
		r := require.New(t)
		adm := newAdmission(1, 0, time.Time{})
		r.Equal(admitted, adm.admit())
		adm.drain()

		start := time.Now()
		waitInFlight(context.Background(), adm, time.Hour, 100*time.Millisecond)
		r.Less(time.Since(start), 2*time.Second)
		r.Equal(1, adm.inFlight())
	})

	t.Run("stops drainTimeout after ctx ends", func(t *testing.T) {
		r := require.New(t)
		adm := newAdmission(1, 0, time.Time{})
		r.Equal(admitted, adm.admit())
		adm.drain()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		waitInFlight(ctx, adm, 100*time.Millisecond, time.Hour)
		r.Less(time.Since(start), 2*time.Second)
		r.Equal(1, adm.inFlight())
	})
}

func TestConfigDefaults(t *testing.T) {
	r := require.New(t)
	c := Config{}.withDefaults()
	r.Equal(30*time.Minute, c.MaxLifetime)
	r.Equal(0, c.MaxRequests)
	r.Equal(1, c.Multiplex)

	c = Config{MaxLifetime: time.Hour}.withDefaults()
	r.Equal(time.Hour, c.MaxLifetime)
}
