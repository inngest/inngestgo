package stephttp

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/inngest/inngestgo/internal/logger"
	"github.com/inngest/inngestgo/middleware"
	"github.com/inngest/inngestgo/pkg/env"
)

const (
	headerRunID     = "x-run-id"
	headerSignature = "x-inngest-signature"
)

type Provider interface {
	// Handle wraps next as an Inngest API function that uses opts.  Only wrapped
	// handlers create runs and accept resume requests from Inngest.
	Handle(opts FnOpts, next http.Handler) http.Handler

	// HandleFunc is Handle for a handler function.
	HandleFunc(opts FnOpts, next http.HandlerFunc) http.HandlerFunc

	// Middleware returns middleware that wraps any handler with opts, eg. a whole
	// router.  A request through it stores its response only once it has a
	// function ID, calls Configure, or runs a step.
	Middleware(opts FnOpts) func(http.Handler) http.Handler

	// Start begins an Inngest API function inside a handler, without a wrapper.
	// The handler must defer the returned end func.  See provider.Start.
	Start(w http.ResponseWriter, r *http.Request, opts FnOpts) (http.ResponseWriter, *http.Request, func(), error)

	// Wait provides a mechanism to wait for all cehckpoints to finish before shutting down.
	// Cancel the incoming context to quit polling for checkpoint progres.
	Wait(ctx context.Context) chan bool
}

// SetupOpts contains configuration for the API middleware.  Optional
// configuration is supplied via SetupOpt adapters.
type SetupOpts struct {
	// Domain is the domain for this API (e.g., "api.mycompany.com")
	Domain string
	// Optional represents optional setup options that you can confgure.
	Optional OptionalSetupOpts
}

type OptionalSetupOpts struct {
	// DefaultAsyncResponse defines the default async response type.  Each function
	// can override the async repsonse type using function configuration.
	DefaultAsyncResponse AsyncResponse

	// SigningKey is the Inngest signing key for authentication.  If empty, this defaults
	// to os.Getenv("INNGEST_SIGNING_KEY").
	SigningKey string
	// SigningKeyFallback is the optional signing key fallback. If empty, this defaults
	// to os.Getenv("INNGEST_SIGNING_KEY_FALLBACK").
	SigningKeyFallback string
	// Env is the branch environment to use. If nil, this defaults to
	// os.Getenv("INNGEST_ENV").
	Env *string
	// BaseURL is the URL of the Inngest API.  If empty, this:
	//
	//   1. Checks to see if INNGEST_DEV is set, indicating dev mode.  If set, we
	//      attempt to use the INNGEST_DEV env var as the base URL if set to a URL,
	//      or default to "http://127.0.0.1:8288" for dev mode.
	//   2. If INNGEST_DEV is not set, we default to the production URL:
	//      "https://api.inngest.com".
	BaseURL string
	// Middleware represents optional middleware to run before and after processing.
	Middleware []func() middleware.Middleware
}

func (o SetupOpts) signingKey() string {
	if o.Optional.SigningKey == "" {
		return os.Getenv("INNGEST_SIGNING_KEY")
	}
	return o.Optional.SigningKey
}

func (o SetupOpts) signingKeyFallback() string {
	if o.Optional.SigningKeyFallback == "" {
		return os.Getenv("INNGEST_SIGNING_KEY_FALLBACK")
	}
	return o.Optional.SigningKeyFallback
}

func (o SetupOpts) environment() string {
	if o.Optional.Env == nil {
		return os.Getenv("INNGEST_ENV")
	}
	return *o.Optional.Env
}

func (o SetupOpts) baseURL() string {
	if o.Optional.BaseURL != "" {
		return o.Optional.BaseURL
	}
	return env.APIServerURL(nil)
}

// provider wraps HTTP handlers to provide Inngest step tooling for API functions.
// This creates a new manager which handles the associated step and request lifecycles.
type provider struct {
	opts   SetupOpts
	api    checkpointAPI
	mw     *middleware.MiddlewareManager
	logger *slog.Logger

	// inflight records the total number of in flight requests and background
	// checkpoints.  Wait returns only when this is zero.
	inflight *atomic.Int32
	// startEmptyID logs the empty function ID warning once for all Start calls.
	// a Start call has no wrapper that lives across requests.
	startEmptyID *sync.Once
}

// Setup creates a new API provider instance
func Setup(opts SetupOpts) *provider {
	// Create a middleware manager for step execution hooks
	mw := middleware.New()
	for _, m := range opts.Optional.Middleware {
		mw.Add(m)
	}

	p := &provider{
		opts:         opts,
		mw:           mw,
		inflight:     &atomic.Int32{},
		startEmptyID: &sync.Once{},
		logger:       logger.Default(),
	}

	apiClient := NewAPIClient(p.opts.baseURL(), p.opts.signingKey(), p.opts.signingKeyFallback())
	apiClient.environment = p.opts.environment()
	p.api = apiClient

	return p
}

// Handle wraps next as an Inngest API function that uses opts.  Only wrapped
// handlers create runs and accept resume requests from Inngest.
func (p *provider) Handle(opts FnOpts, next http.Handler) http.Handler {
	return p.HandleFunc(opts, next.ServeHTTP)
}

// HandleFunc is Handle for a handler function.
func (p *provider) HandleFunc(opts FnOpts, next http.HandlerFunc) http.HandlerFunc {
	return p.serve(opts, true, next)
}

// Middleware returns middleware that wraps any handler with opts, eg. a whole
// router.  A request through it stores its response only once it has a
// function ID, calls Configure, or runs a step.  Without this, every route
// behind the middleware keeps a copy of its response.
func (p *provider) Middleware(opts FnOpts) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return p.serve(opts, opts.ID != "", next.ServeHTTP)
	}
}

// serve wraps next.  known is true when the wrapper knows that next is an
// Inngest function, so the request stores its response from the first write.
func (p *provider) serve(opts FnOpts, known bool, next http.HandlerFunc) http.HandlerFunc {
	w := &routeWrapper{opts: opts, known: known, emptyID: &sync.Once{}}
	return func(rw http.ResponseWriter, r *http.Request) {
		p.inflight.Add(1)
		defer func() { p.inflight.Add(-1) }()

		if err := processRequest(p, w, r, rw, next); err != nil {
			p.logger.Error("error handling api request", "error", err)
		}
	}
}

// routeWrapper holds what one Handle, HandleFunc, or Middleware call shares
// across its requests.
type routeWrapper struct {
	opts FnOpts
	// known is true when the wrapper knows that the handler is an Inngest
	// function.  See requestOwner.captureResponse.
	known bool
	// emptyID logs the empty function ID warning once for this wrapper, so a
	// route with an ID in each URL does not log on every request.
	emptyID *sync.Once
}

// Start begins an Inngest API function inside a handler, without a wrapper.
// use it when the provider cannot wrap the handler, eg. routes that generated
// code registers.
//
//	func (a api) ListCredits(w http.ResponseWriter, r *http.Request) {
//		w, r, end, err := a.steps.Start(w, r, stephttp.FnOpts{ID: "list-credits"})
//		defer end()
//		if err != nil {
//			return // Start already wrote the 500
//		}
//		// the handler, with steps
//	}
//
// call Start before the handler reads the request body or writes the response.
// the handler must use the w and r that Start returns.  shadowing them as in
// the example does that.  a write to the handler's own w is missing from the
// run, and a step that gets the handler's own r creates no run.
//
// the handler must defer end directly.  without it, a finished run is not sent
// to Inngest, and an async step or a request from Inngest that resumes a run
// panics up to net/http, which drops the connection.  end runs once.  when
// Start returns an error, the 500 is already written, and the handler must
// return.
func (p *provider) Start(w http.ResponseWriter, r *http.Request, opts FnOpts) (http.ResponseWriter, *http.Request, func(), error) {
	p.inflight.Add(1)

	wrap := &routeWrapper{opts: opts, known: true, emptyID: p.startEmptyID}
	o := newRequestOwner(p, wrap, r, w, nil)

	var once sync.Once
	if err := o.begin(r.Context()); err != nil {
		end := func() {
			once.Do(func() { p.inflight.Add(-1) })
		}
		return o.w, r, end, err
	}

	end := func() {
		// recover only stops a panic when end is the deferred function.
		recovered := recover()
		ran := false
		once.Do(func() {
			ran = true
			defer p.inflight.Add(-1)
			if err := o.finish(recovered); err != nil {
				p.logger.Error("error handling api request", "error", err)
			}
		})
		// a second call has nothing to finish.  a panic that it recovers belongs
		// to the handler, so it continues.
		if !ran && recovered != nil {
			panic(recovered)
		}
	}
	return o.w, o.handlerReq, end, nil
}

// resolveConfig returns the config for one request to a wrapped handler.  each
// request gets its own copy, because UpdateOmitResponseBody changes it.
func (p *provider) resolveConfig(opts FnOpts, r *http.Request) FnOpts {
	if opts.ID == "" {
		opts.ID = defaultFunctionID(r)
	}
	if opts.AsyncResponse == nil {
		opts.AsyncResponse = p.opts.Optional.DefaultAsyncResponse
	}
	if opts.AsyncResponse == nil {
		opts.AsyncResponse = AsyncResponseRedirect{}
	}
	if opts.MaxRequestBodySize <= 0 {
		opts.MaxRequestBodySize = DefaultMaxRequestBodySize
	}
	if opts.MaxResponseBodySize <= 0 {
		opts.MaxResponseBodySize = DefaultMaxResponseBodySize
	}
	return opts
}

// defaultFunctionID returns the http.ServeMux pattern that routed r, with the
// method added when the pattern has none.  for example, "POST /users/{id}".
// it returns "" when no ServeMux routed r, and Inngest then builds the ID from
// the method and the path.  without the pattern, each value in a path such as
// /users/123 creates a separate function.
func defaultFunctionID(r *http.Request) string {
	if r.Pattern == "" {
		return ""
	}
	if strings.Contains(r.Pattern, " ") {
		return r.Pattern
	}
	return r.Method + " " + r.Pattern
}

// goTracked runs fn in a goroutine and counts it as in flight until fn returns.
// without this, Wait can return and the process can exit before a checkpoint
// reaches the Inngest API, which loses the run.
func (p *provider) goTracked(fn func()) {
	p.inflight.Add(1)
	go func() {
		defer p.inflight.Add(-1)
		fn()
	}()
}

// Wait returns a channel that is sent when all in progress checkpoints finish.
func (p *provider) Wait(ctx context.Context) chan bool {
	c := make(chan bool)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				// Continue on.
			}

			if p.inflight.Load() == 0 {
				c <- true
				return
			}
		}
	}()
	return c
}
