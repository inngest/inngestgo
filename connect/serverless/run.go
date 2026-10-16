// Package serverless connects a running serve() app to Inngest as a connect
// worker.  it reads the app's function configs with an in-band sync, connects
// to the gateway, and forwards each request to the app over HTTP.  it stops
// after an idle period.
//
// Run, Sync, and Config are the package's whole API.  the caller starts the
// app, finds its address, and stops it after Run returns.
package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	connectproto "github.com/inngest/inngest/proto/gen/connect/v1"
	"github.com/inngest/inngestgo/internal/connectworker"
	"github.com/inngest/inngestgo/internal/types"
)

// Run serves the app at cfg.AppURL as a connect worker.  it returns nil after
// the idle timeout, after MaxLifetime or MaxRequests once in-flight requests
// finish or 2h pass, or once ctx ends and in-flight requests finish or
// DrainTimeout passes.  the caller cancels ctx on SIGTERM and when the app exits.  Run
// returns an error when the app does not answer its sync or the worker cannot
// connect.
func Run(ctx context.Context, cfg Config) error {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return err
	}
	if cfg.APIBaseURL == "" {
		return fmt.Errorf("missing API base URL")
	}
	l := cfg.Logger
	keys := signingKeys{primary: cfg.SigningKey, fallback: cfg.SigningKeyFallback}

	var deadline time.Time
	if cfg.MaxLifetime > 0 {
		deadline = time.Now().Add(cfg.MaxLifetime)
	}

	synced, err := boot(ctx, cfg, keys)
	if err != nil {
		return err
	}
	l.Info("app synced", "app_id", synced.ID, "functions", len(synced.stepURLs))

	adm := newAdmission(cfg.Multiplex, cfg.MaxRequests, deadline)
	conn, err := connectApp(ctx, cfg, synced, keys, adm)
	if err != nil {
		return err
	}

	waitForExit(ctx, cfg, adm, deadline)

	// Close sends WORKER_PAUSE, so the gateway stops routing to this worker.
	// the connection stays open for replies from requests still in flight.
	if err := conn.Close(); err != nil {
		l.Error("could not close connection", "error", err)
	}
	waitInFlight(ctx, adm, cfg.DrainTimeout, maxRequestDuration)
	if n := adm.inFlight(); n > 0 {
		l.Warn("returning with requests in flight", "in_flight", n)
	}
	return nil
}

// Sync waits for the app at cfg.AppURL, syncs it, and returns its function
// configs as JSON, with each step URL changed to the connect host.  the JSON
// object has app_id, app_version, sdk_language, sdk_version, and functions.
func Sync(ctx context.Context, cfg Config) (json.RawMessage, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	synced, err := boot(ctx, cfg, signingKeys{primary: cfg.SigningKey, fallback: cfg.SigningKeyFallback})
	if err != nil {
		return nil, err
	}
	return json.Marshal(introspection{
		AppID:       synced.ID,
		AppVersion:  synced.Version,
		SDKLanguage: synced.SDK.Language,
		SDKVersion:  synced.SDK.Version,
		Functions:   synced.Functions,
	})
}

// introspection is the JSON that Sync returns.
type introspection struct {
	AppID       string          `json:"app_id"`
	AppVersion  *string         `json:"app_version,omitempty"`
	SDKLanguage string          `json:"sdk_language"`
	SDKVersion  string          `json:"sdk_version"`
	Functions   json.RawMessage `json:"functions"`
}

// boot waits for the app to answer and then syncs it.
func boot(ctx context.Context, cfg Config, keys signingKeys) (*syncedApp, error) {
	bootCtx, cancel := context.WithTimeout(ctx, cfg.BootTimeout)
	defer cancel()

	client := &http.Client{Timeout: 10 * time.Second}
	if err := waitReady(bootCtx, client, cfg.AppURL, keys); err != nil {
		return nil, err
	}
	return syncApp(bootCtx, client, cfg.AppURL, keys)
}

func connectApp(ctx context.Context, cfg Config, app *syncedApp, keys signingKeys, adm *admission) (connectworker.WorkerConnection, error) {
	hashed, hashedFallback, err := keys.hashed()
	if err != nil {
		return nil, err
	}

	caps := app.Capability
	caps.Connect = types.ConnectV1
	platform := platformName
	maxConcurrency := int64(cfg.Multiplex)

	var env, instanceID *string
	if cfg.Env != "" {
		env = &cfg.Env
	}
	if cfg.InstanceID != "" {
		instanceID = &cfg.InstanceID
	}

	// the worker outlives ctx, so canceling ctx drains it through Close and
	// does not cut off a reconnect.
	return connectworker.Connect(context.WithoutCancel(ctx), connectworker.Opts{
		Env: env,
		Apps: []*connectproto.AppConfiguration{{
			AppName:    app.ID,
			AppVersion: app.Version,
			Functions:  app.Functions,
		}},
		Capabilities:             caps,
		HashedSigningKey:         hashed,
		HashedSigningKeyFallback: hashedFallback,
		MaxWorkerConcurrency:     &maxConcurrency,
		APIBaseURL:               cfg.APIBaseURL,
		IsDev:                    cfg.Dev,
		InstanceID:               instanceID,
		Platform:                 &platform,
		SDKVersion:               app.SDK.Version,
		SDKLanguage:              app.SDK.Language,
		Handler: &forwarder{
			client:    &http.Client{},
			keys:      keys,
			stepURLs:  app.stepURLs,
			admission: adm,
			logger:    cfg.Logger,
		},
	}, cfg.Logger)
}

// waitForExit blocks until ctx ends, the worker is idle for the idle timeout,
// the deadline passes, or the worker admits MaxRequests requests.  each case
// leaves adm draining, so no new request starts.
func waitForExit(ctx context.Context, cfg Config, adm *admission, deadline time.Time) {
	l := cfg.Logger
	tick := time.NewTicker(idleCheckInterval(cfg.IdleTimeout))
	defer tick.Stop()

	var lifetime <-chan time.Time
	if !deadline.IsZero() {
		t := time.NewTimer(time.Until(deadline))
		defer t.Stop()
		lifetime = t.C
	}

	for {
		select {
		case <-ctx.Done():
			adm.drain()
			l.Info("draining", "in_flight", adm.inFlight())
			return
		case <-lifetime:
			adm.drain()
			l.Info("draining after max lifetime", "max_lifetime", cfg.MaxLifetime.String(), "in_flight", adm.inFlight())
			return
		case <-adm.maxAdmitted:
			adm.drain()
			l.Info("draining after max requests", "max_requests", cfg.MaxRequests, "in_flight", adm.inFlight())
			return
		case now := <-tick.C:
			if adm.idleFor(now) >= cfg.IdleTimeout && adm.tryDrain() {
				l.Info("exiting after idle timeout", "idle_timeout", cfg.IdleTimeout.String())
				return
			}
		}
	}
}

// idleCheckInterval keeps an idle exit within a tenth of the idle timeout.
func idleCheckInterval(idle time.Duration) time.Duration {
	return max(idle/10, 10*time.Millisecond)
}

// waitInFlight waits until no request is in flight, for at most maxWait.  once
// ctx ends it waits at most drainTimeout more.  a request still running when
// it returns is retried by the queue once its lease expires.
func waitInFlight(ctx context.Context, adm *admission, drainTimeout, maxWait time.Duration) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	limit := time.NewTimer(maxWait)
	defer limit.Stop()

	done := ctx.Done()
	var cutoff <-chan time.Time
	for adm.inFlight() > 0 {
		select {
		case <-done:
			done = nil
			cutoff = time.After(drainTimeout)
		case <-cutoff:
			return
		case <-limit.C:
			return
		case <-tick.C:
		}
	}
}
