package stephttp

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/oklog/ulid/v2"
)

const (
	headerRunID     = "x-inngest-run-id"
	headerStack     = "x-inngest-stack"
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
	// AppID is the application identifier
	AppID string
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

// WithRequestSizeLimit specifies the maximum request size for the input request.  By default,
// this is set to 4MB.
func WithRequestSizeLimit(limit int) SetupOpt {
	return func(mw *provider) {
		mw.maxRequestSizeLimit = limit
	}
}

// WithBaseURL changes the API URL used for step HTTP operations such as checkpointing.
func WithBaseURL(url string) SetupOpt {
	return func(mw *provider) {
		mw.baseURL = url
	}
}

// WithInngestMiddleware adds Inngest middleware to run whenever steps and functions execute.
func WithInngestMiddleware(mw func() middleware.Middleware) SetupOpt {
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

		// TODO: Panic handler.

		var (
			// startTime tracks the start time
			startTime = time.Now()

			// runID represents the run ID we use for this new API-based run.
			runID = ulid.MustNew(uint64(startTime.UnixMilli()), rand.Reader)

			// createRunLock is a lock which can be awaited on whilst the run is checkpointed in
			// the background
			created = make(chan bool, 1)
		)

		mgr := sdkrequest.NewManager(
			nil, // NOTE: We do not have servable functions here;  this is the next HTTP handler in the chain.
			p.mw,
			func() {},             // Cancel is currently a noop.
			&sdkrequest.Request{}, // TODO
			p.opts.SigningKey,
			// This step mode is always the default for API-based functions.
			sdkrequest.StepModeBackground,
		)
		ctx := sdkrequest.SetManager(r.Context(), mgr)

		if _, ok := p.getExistingRun(r, created); ok {
			// This request is being resumed and is a re-entry.  This always means that we
			// switch from background checkpointing to returning after each invocation.
			mgr.SetStepMode(sdkrequest.StepModeReturn)
		} else {
			// We're creating a net-new run.  In this case, ensure that we hit the API for
			// starting new runs.  We only block on this when checkpointing steps in the
			// background; the API handler itself can continue to run whilst this executes.
			p.handleNewRun(r, runID, created)
		}

		// Execute either the next step (if this is a reentry) or all next steps until the
		// API response or a hijack.
		r = r.WithContext(ctx)
		result, panicErr := p.call(w, r, mgr, next, startTime)

		if panicErr != nil {
			result.Error = panicErr.Error()
		}
		if mgr.Err() != nil {
			result.Error = mgr.Err().Error()
		}

		// At this point, either:
		// 1. The API endpoint has already returned data to the client and now we're finishing up.
		// 2. A step errored, and we must recover, retry, and redirect the user to the retry attempt.
		//
		// Whatever happens here will unfortunately still block the client, so we need to handle the
		// checkpointing in a goroutine on success.

		if result.Error != "" {
			// TODO: Are we retrying?  If so, handle this.
		}

		go func() {
			// Decrease the in-flight counter once done.
			defer func() { p.inflight.Add(-1) }()

			// Note that at this point the request would typically have finished, therefore the
			// context is cancelled.  Stop this from breaking our API calls.
			ctx = context.WithoutCancel(ctx)

			if ok := <-created; !ok {
				// TODO: Log that we cannot checkpoint steps.
				p.logger.Error("cannot checkpoint steps to unsynced run", "run_id", runID)
				return
			}

			// Checkpoint the steps AFTER the run has finished creating.
			if err := p.api.CheckpointSteps(ctx, runID, mgr.Ops()); err != nil {
				p.logger.Error("error checkpointing steps",
					"error", err,
					"run_id", runID,
				)
			}

			if err := p.api.CheckpointResponse(ctx, runID, result); err != nil {
				p.logger.Error("error checkpointing result",
					"error", err,
					"run_id", runID,
				)
			}
		}()
	}
}

// call initializes the hijacking control flow, then executes the API-based Inngest function.
// Depending on the step mode, this may execute all steps or execute a single step then halt
// once the step finishes.
//
// It is the callers responsibility to handle the generated opcodes added to the invocation
// manager.
func (p *provider) call(w http.ResponseWriter, r *http.Request, mgr sdkrequest.InvocationManager, next http.HandlerFunc, startTime time.Time) (APIResult, error) {
	var (
		panicErr error
		ctx      = r.Context()
	)

	defer func() {
		if r := recover(); r != nil {
			callCtx := mgr.CallContext()

			// Was this us attepmting to prevent functions from continuing, using
			// panic as a crappy control flow because go doesn't have generators?
			if _, ok := r.(sdkrequest.ControlHijack); ok {
				// Step attempt ended (completed or errored).
				//
				// NOTE: In this case, for API-based functions, we only get ControlHijack
				// panics when we need to checkpoint via a blocking call.
				//
				// For example, when you `step.sleep` or `step.waitForEvent`, the function
				// turns from a synchronous API to an asynchronous background function
				// automatically.
				mgr.SetStepMode(sdkrequest.StepModeReturn)
				p.mw.AfterExecution(ctx, callCtx, nil, nil)
				return
			}

			// TODO: How many retries does this function have?  If zero, we can ignore
			// any retries and show the error directly to the user, keeping StepModeBackground
			// checkpointing.

			panicStack := string(debug.Stack())
			panicErr = fmt.Errorf("function panicked: %v.  stack:\n%s", r, panicStack)

			p.mw.AfterExecution(ctx, callCtx, nil, nil)
			p.mw.OnPanic(ctx, callCtx, r, panicStack)
		}
	}()

	// Wrap response writer to capture output
	rw := newResponseWriter(w)
	// Execute the handler with step tooling available
	next(rw, r.WithContext(ctx))
	duration := time.Since(startTime)

	// Attempt to flush the response directly to the client immediately, reducing TTFB
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	return APIResult{
		StatusCode: rw.statusCode,
		Headers:    flattenHeaders(rw.Header()),
		Body:       rw.body.Bytes(),
		Duration:   duration,
	}, panicErr
}

// handleNewRun creates a new run with the given request information.  This automatically upserts
// the requried apps and functions via the same API request whilst creating a new run.
func (p *provider) handleNewRun(r *http.Request, runID ulid.ULID, created chan bool) {
	requestBody, err := readRequestBody(r)
	if err != nil {
		p.logger.Error("error reading request body creating new run", "error", err)
	}

	// Create new API-based run in a goroutine.  This can always happen in the background whilst
	// the API is executing.
	//
	// Note that it is important that this finishes before we begin to checkpoint step data.
	go func() {
		// TODO: Retry this up to 3 times.
		// TODO: End to end encryption, if enabled.
		if p.api == nil {
			created <- false
			return
		}

		scheme := r.URL.Scheme
		if scheme == "" {
			scheme = "https://"
		}

		err := p.api.CheckpointNewRun(r.Context(), runID, NewAPIRunData{
			Domain:      scheme + r.Host,
			Method:      r.Method,
			Path:        r.URL.Path,
			IP:          getClientIP(r),
			ContentType: r.Header.Get("Content-Type"),
			QueryParams: r.URL.RawQuery,
			Body:        requestBody,
		})
		if err != nil {
			p.logger.Error("error creating new api-based inngest run", "error", err)
			// lol
			created <- false
			return
		}
		created <- true
	}()
}
