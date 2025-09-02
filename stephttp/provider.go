package stephttp

import (
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/internal/middleware"
)

const (
	headerRunID     = "x-run-id"
	headerSignature = "x-inngest-signature"
)

type Provider interface {
	// ServeHTTP is the middleware that allows the Inngest handler to work.
	ServeHTTP(next http.HandlerFunc) http.HandlerFunc

	// Wait provides a mechanism to wait for all cehckpoints to finish before shutting down.
	Wait() chan bool
}

// SetupOpts contains required configuration for the API middleware.  Optional
// configuration is supplied via SetupOpt adapters.
type SetupOpts struct {
	// SigningKey is the Inngest signing key for authentication
	SigningKey string
	// Domain is the domain for this API (e.g., "api.mycompany.com")
	Domain string
}

// provider wraps HTTP handlers to provide Inngest step tooling for API functions.
// This creates a new manager which handles the associated step and request lifecycles.
type provider struct {
	opts   SetupOpts
	api    checkpointAPI
	mw     *middleware.MiddlewareManager
	logger *slog.Logger

	// inflight records the total number of in flight requests.
	inflight *atomic.Int32

	maxRequestSizeLimit int
	baseURL             string
}

type SetupOpt func(p *provider)

// SetupRequestSizeLimit specifies the maximum request size for the input request.  By default,
// this is set to 4MB.
func SetupRequestSizeLimit(limit int) SetupOpt {
	return func(mw *provider) {
		mw.maxRequestSizeLimit = limit
	}
}

// SetupBaseURL changes the API URL used for step HTTP operations such as checkpointing.
func SetupBaseURL(url string) SetupOpt {
	return func(mw *provider) {
		mw.baseURL = url
	}
}

// SetupInngestMiddleware adds Inngest middleware to run whenever steps and functions execute.
func SetupInngestMiddleware(mw func() middleware.Middleware) SetupOpt {
	return func(httpmw *provider) {
		httpmw.mw.Add(mw)
	}
}

// Setup creates a new API provider instance
func Setup(opts SetupOpts, optionalOpts ...SetupOpt) *provider {
	// Create a middleware manager for step execution hooks
	mw := middleware.New()

	p := &provider{
		opts:     opts,
		mw:       mw,
		inflight: &atomic.Int32{},
		logger:   slog.Default(),
		baseURL:  inngestgo.APIServerURL(),
	}

	for _, o := range optionalOpts {
		o(p)
	}

	p.api = NewAPIClient(p.baseURL, p.opts.SigningKey)

	return p
}

// Handler wraps an HTTP handler to provide Inngest step tooling directly inside of
// your APIs.
func (p *provider) ServeHTTP(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.inflight.Add(1)
		defer func() { p.inflight.Add(-1) }()

		if err := processRequest(p, r, w, next); err != nil {
			p.logger.Error("error handling api request", "error", err)
		}
	}
}
