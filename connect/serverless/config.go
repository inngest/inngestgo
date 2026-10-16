package serverless

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"time"
)

const (
	defaultBootTimeout  = time.Minute
	defaultIdleTimeout  = 30 * time.Second
	defaultDrainTimeout = 30 * time.Second
	defaultMaxLifetime  = 30 * time.Minute

	// maxRequestDuration is the executor's limit for one request to the app
	// (consts.MaxFunctionTimeout in inngest).  a drain after MaxLifetime or
	// MaxRequests waits this long for in-flight requests, so a step that
	// started before the drain can finish.
	maxRequestDuration = 2 * time.Hour

	maxIdleTimeout = 15 * time.Minute
	maxMultiplex   = 255

	platformName = "inngest-serverless"
)

// Config holds the settings for Run and Sync.
type Config struct {
	// AppURL is the app's serve endpoint, such as
	// http://127.0.0.1:3000/api/inngest.  the sync request sends it to the
	// app, so the app's step URLs use it, and requests go to those URLs.
	AppURL string

	// SigningKey signs each request to the app, and its hash authenticates
	// the worker with the API.  SigningKeyFallback is tried after the API
	// rejects SigningKey.
	SigningKey         string
	SigningKeyFallback string
	// Env is the Inngest environment name.
	Env string
	// APIBaseURL is the Inngest API that the worker connects through.  Run
	// fails without it.
	APIBaseURL string
	// InstanceID is a stable ID for this worker.  the gateway routes no more
	// than Multiplex requests to workers that share it.
	InstanceID string
	// Dev connects to the dev server.  a Go app still needs SigningKey to
	// answer the sync.
	Dev bool

	// Multiplex is the number of requests that run at once, 1 to 255.
	Multiplex int

	// MaxLifetime is the maximum lifetime of the function.  once this limit
	// is reached, the worker will drain (not allowing new reqs) and wait up
	// to 2h for all in-progress steps to finish before shutting down.
	// defaults to 30m.
	MaxLifetime time.Duration
	// MaxRequests limits the maximum number of requests that the app will
	// serve before shutting down.  the drain then waits up to 2h for
	// in-progress steps.  0 means no limit.
	MaxRequests int

	// BootTimeout limits the time until the app answers its sync.
	BootTimeout time.Duration
	// IdleTimeout is the time that the serverless function stays listening
	// from when the last step/SDK function ran.
	IdleTimeout time.Duration
	// DrainTimeout limits the wait for in-flight requests once ctx ends.  a
	// request still running after it is retried by the queue once its lease
	// expires.  a drain after MaxLifetime or MaxRequests waits up to 2h for
	// every request, or until DrainTimeout after ctx ends.
	DrainTimeout time.Duration

	Logger *slog.Logger
}

func (c Config) withDefaults() Config {
	if c.BootTimeout == 0 {
		c.BootTimeout = defaultBootTimeout
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = defaultIdleTimeout
	}
	if c.DrainTimeout == 0 {
		c.DrainTimeout = defaultDrainTimeout
	}
	if c.MaxLifetime == 0 {
		c.MaxLifetime = defaultMaxLifetime
	}
	if c.Multiplex == 0 {
		c.Multiplex = 1
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	return c
}

func (c Config) validate() error {
	u, err := url.Parse(c.AppURL)
	switch {
	case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
		return fmt.Errorf("app URL %q must be an http or https URL", c.AppURL)
	case c.Multiplex < 1 || c.Multiplex > maxMultiplex:
		return fmt.Errorf("multiplex %d must be between 1 and %d", c.Multiplex, maxMultiplex)
	case c.IdleTimeout < time.Second || c.IdleTimeout > maxIdleTimeout:
		return fmt.Errorf("idle timeout %s must be between 1s and %s", c.IdleTimeout, maxIdleTimeout)
	case c.MaxLifetime < 0:
		return fmt.Errorf("max lifetime %s must not be negative", c.MaxLifetime)
	case c.MaxRequests < 0:
		return fmt.Errorf("max requests %d must not be negative", c.MaxRequests)
	case !c.Dev && c.SigningKey == "":
		return fmt.Errorf("signing key is required unless Dev is set")
	}
	return nil
}
