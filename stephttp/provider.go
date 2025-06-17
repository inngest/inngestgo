package stephttp

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
)

const (
	headerRunID     = "x-inngest-run-id"
	headerStack     = "x-inngest-stack"
	headerSignature = "x-inngest-signature"
)

// SetupOpts contains configuration for the API middleware
type SetupOpts struct {
	// SigningKey is the Inngest signing key for authentication
	SigningKey string
	// AppID is the application identifier
	AppID string
	// Domain is the domain for this API (e.g., "api.mycompany.com")
	Domain string
}

// provuder wraps HTTP handlers to provide Inngest step tooling for API functions.
// This creates a new manager which handles the associated step and request lifecycles.
type provider struct {
	opts SetupOpts
	api  checkpointAPI
	mw   *middleware.MiddlewareManager

	maxRequestReadLimit int
	baseURL             string
}

type SetupOpt func(p *provider)

func WithRequestReadLimit(limit int) SetupOpt {
	return func(mw *provider) {
		mw.maxRequestReadLimit = limit
	}
}

func WithBaseURL(url string) SetupOpt {
	return func(mw *provider) {
		mw.baseURL = url
	}
}

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
		opts: opts,
		mw:   mw,
	}

	for _, o := range optionalOpts {
		o(p)
	}

	return p
}

// Handler wraps an HTTP handler to provide Inngest step tooling
func (p *provider) ServeHTTP(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		startTime := time.Now()

		var (
			mode = sdkrequest.StepModeBackground

			runID string
			mgr   sdkrequest.InvocationManager
		)

		_, _ = runID, mode

		if _, ok := getExistingRun(r); ok {
			// This request is being resumed and is a re-entry.
			mode = sdkrequest.StepModeBackground
			// TODO: Validate signature
			// TODO: Use API to fetch run data, then resume.
		} else {
			p.handleNewRun(r)
		}

		mgr = sdkrequest.NewManager(
			nil, // NOTE: We do not have servable functions here;  this is the next HTTP handler in the chain.
			p.mw,
			func() {},             // Cancel is currently a noop.
			&sdkrequest.Request{}, // TODO
			p.opts.SigningKey,
			mode,
		)
		ctx = sdkrequest.SetManager(ctx, mgr)

		// Execute either the next step (if this is a reentry) or all next steps until the
		// API response or a hijack.

		var (
			panicErr error
			result   APIResult
		)

		func() {
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
						mgr.SetStepMode(sdkrequest.StepModeCheckpoint)
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

			// Store the API result
			result = APIResult{
				StatusCode: rw.statusCode,
				Headers:    flattenHeaders(rw.Header()),
				Body:       rw.body.Bytes(),
				Duration:   duration,
			}
		}()

		// TODO: Handle panics and errors separately.
		if panicErr != nil {
			result.Error = panicErr.Error()
		}
		if mgr.Err() != nil {
			result.Error = mgr.Err().Error()
		}

		// Handle the result of the API as expected.  If this is a step error, we must retry
		// the step by changing the checkpoint to a blocking operation, then redirecting on the
		// client side.

		spew.Dump(result)
		spew.Dump(mgr.Ops())
	}
}

// handleNewRun creates a new run with the given request information.  This automatically upserts
// the requried apps and functions via the same API request whilst creating a new run.
func (p *provider) handleNewRun(r *http.Request) {
	// // This is a new request - create a new API run
	// requestBody, err := m.readRequestBody(r)
	// if err != nil {
	// 	http.Error(w, "Failed to read request body", http.StatusInternalServerError)
	// 	return
	// }

	// // Create new API run
	// newRunID, err := m.api.CreateAPIRun(r.Context(), m.opts.Domain, r.URL.Path, r.Method, requestBody, nil)
	// if err != nil {
	// 	http.Error(w, "Failed to create API run", http.StatusInternalServerError)
	// 	return
	// }
	// runID = newRunID
}
