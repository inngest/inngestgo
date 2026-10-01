package stephttp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/internal/opcode"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/pkg/env"
	"github.com/inngest/inngestgo/pkg/httputil"
	"github.com/inngest/inngestgo/pkg/version"
	"github.com/oklog/ulid/v2"
)

func processRequest(p *provider, opts FnOpts, r *http.Request, w http.ResponseWriter, next http.HandlerFunc) error {
	cfg := p.resolveConfig(opts, r)

	owner := &requestOwner{
		r:        r,
		w:        newResponseWriter(w),
		next:     next,
		provider: p,
		mgr: sdkrequest.NewManager(sdkrequest.Opts{
			SigningKey: p.opts.signingKey(),
			Mode:       sdkrequest.StepModeManual,
			APIBaseURL: env.APIServerURL(nil),
		}),

		config:    &cfg,
		startTime: time.Now(),
	}
	owner.mgr.SetFn(servableRestFn{cfg})

	owner.run = CheckpointRun{
		RunID: ulid.MustNew(
			uint64(owner.startTime.UnixMilli()),
			rand.Reader,
		),
	}

	return owner.handle(r.Context())
}

// requestOwner represents a manager for a single request to a sync function.
// this is short lived and only exists for one request.
type requestOwner struct {
	// # Dependency injection options

	// r represents the incoming http request for the sync function.  This may be
	// an end user's request, or it may be a re-entry from Inngest.
	r *http.Request
	// w responds to the request. This is always our wrapped responseWriter.
	w *responseWriter
	// next is the API handler to call to execute the sync function, ie. next in
	// the middleware chain.
	next http.HandlerFunc
	// provider references the parent provider that created the
	// request.
	provider *provider
	// sdkrequest is the step execution manager for the underlying function.
	mgr sdkrequest.InvocationManager

	// # Run-specific options

	// config represents function-specific config.  It is never nil.
	config *FnOpts
	// startTime tracks the start time of the API request. We must track this
	// as early as possible.
	startTime time.Time
	// run represents the IDs for the current sync run.
	run CheckpointRun
	// body records the request body for a new run.  It is nil when Inngest
	// resumes an existing run.
	body *bodyRecorder
}

func (o *requestOwner) handle(ctx context.Context) error {
	// Always add the run ID to the header.
	o.w.Header().Add("x-run-id", o.run.RunID.String())
	o.w.Header().Add("X-Inngest-SDK", version.GetVersion())

	// Always add the manager to context.
	ctx = sdkrequest.SetManager(ctx, o.mgr)
	// Add an updater, allowing the handler to change config via ctx (eg. in UpdateOmitResponseBody)
	ctx = o.withConfigUpdater(ctx)

	resumed, err := o.getExistingRun(ctx)
	if err != nil {
		// Inngest sent this request to resume a run.  a 500 makes the executor send
		// it again.  without this, the request runs the handler as a new run and
		// repeats every step after the last saved one.
		http.Error(o.w, "error loading run state", http.StatusInternalServerError)
		return err
	}
	if resumed {
		// In this case, we're re-entering an existing run, which means we're now
		// running async and are responding to an Inngest's executor call.
		//
		// In this case, we always want to start returning opcodes to the HTTP request
		// directly so that the async engine can take over.
		o.mgr.SetStepMode(sdkrequest.StepModeYield)

		// Call the handler to execute the next steps.
		_ = o.call(ctx)

		if len(o.mgr.Ops()) > 0 {
			// Write the ops to the response writer.  We know that this request is from Inngest,
			// therefore its safe to write opcodes directly to the response.
			byt, err := json.Marshal(o.mgr.Ops())
			if err != nil {
				o.provider.logger.Error("error marshalling opcodes", "error", err)
				return err
			}
			o.w.WriteHeader(206)
			_, _ = o.w.Write(byt)
		}
		return nil
	}

	// Here, we're always creating a net-new run.  Firstly, we must hit the API endpoint
	// to begin the logic and check for any function config.  This will continue to execute
	// step.run calls until either an error, an async step, or the fn finishes.
	//
	// a new run gets one attempt, so a step error is final and step.Run returns it
	// to the handler.  without this, a step error that the handler catches still
	// makes the run async, and the async response is written after the handler's
	// own response.
	maxAttempts := 1
	o.mgr.Request().CallCtx.MaxAttempts = &maxAttempts

	// record the body as the handler reads it.  without this, the new run stores
	// an empty body whenever the handler reads the request body.
	o.body = newBodyRecorder(o.r.Body)
	o.r.Body = o.body

	result := o.call(ctx)

	// Note that at this point the request would typically have finished, therefore the
	// context could be cancelled.  Stop this from breaking our API calls.
	ctx = context.WithoutCancel(ctx)

	if opcode.HasAsyncOps(o.mgr.Ops(), o.run.Attempt, 0) {
		// Always checkpoint first, then handle the async conversion.
		token := o.handleFirstCheckpoint(ctx)
		return o.handleAsyncConversion(ctx, token)
	}

	// Attempt to flush the response directly to the client immediately, reducing TTFB
	// Only flush if the connection hasn't been hijacked (e.g., for WebSocket upgrades)
	if !o.w.hijacked {
		o.w.Flush()
	}

	if len(o.mgr.Ops()) == 0 && !o.config.TrackAllRequests {
		// If there are no steps and TrackAllRequests is disabled, we don't actually
		// need to do anything.
		return nil
	}

	// In this case, the run must have finished - as no async conversion happened.
	//
	// Append the run complete result to the ops, which finalizes the run in
	// a single call.
	if err := o.appendResult(ctx, result); err != nil {
		o.provider.logger.Error("error appending run complete op",
			"error", err,
			"run_id", o.run.RunID,
		)
	}

	// the client already has its response, so commit the run in the background.
	// the provider counts this as in flight, so Wait does not return until the
	// commit request to the Inngest API finishes.
	o.handleFinalCheckpointAsync()

	return nil
}

// handleAsyncConversion handles the conversion of sync -> async functions, which
// essetially means checkpointing the steps in the foreground (blocking) so that
// we can handle them with the async executor.
//
// We also need to handle the API response to our user, which is either a token,
// a redirect, or a custom response.
func (o *requestOwner) handleAsyncConversion(ctx context.Context, token string) error {
	if !opcode.HasAsyncOps(o.mgr.Ops(), o.run.Attempt, 0) {
		return nil
	}

	// Then handle the response to our user.  resolveConfig always sets
	// AsyncResponse.
	var url string

	switch v := o.config.AsyncResponse.(type) {
	case AsyncResponseToken:
		return json.NewEncoder(o.w).Encode(asyncResponseToken{
			RunID: o.run.RunID,
			Token: token,
		})
	case AsyncResponseCustom:
		v(o.w, o.r)
		return nil
	case AsyncResponseRedirect:
		if v.URL != nil {
			url = v.URL(o.run.RunID, token)
		}
	}

	if url == "" {
		url = defaultRedirectURL(o.provider.opts, o.run.RunID, token)
	}

	http.Redirect(o.w, o.r, url, http.StatusSeeOther)
	return nil
}

// getExistingRun loads the saved steps when Inngest sends the request to resume
// a run.  it returns false for a request that starts a new run, and an error
// when the request resumes a run whose steps cannot be loaded.
func (o *requestOwner) getExistingRun(ctx context.Context) (bool, error) {
	// Validate signature and extract run information
	if !validateResumeRequestSignature(ctx, o.r, o.provider.opts.signingKey(), o.provider.opts.signingKeyFallback()) {
		return false, nil
	}

	// Extract headers after validation passes
	runID, err := ulid.Parse(o.r.Header.Get(headerRunID))
	if err != nil {
		return false, nil
	}

	o.run.RunID = runID
	o.run.Signature = o.r.Header.Get(headerSignature)

	// XXX: Use V2 API when created.
	steps, err := o.provider.api.GetSteps(ctx, o.run.RunID)
	if err != nil {
		return false, fmt.Errorf("error loading steps for run %s: %w", o.run.RunID, err)
	}

	// This is now always async.
	o.mgr.SetSteps(steps)
	o.mgr.SetStepMode(sdkrequest.StepModeYield)

	// XXX: When using the V2 API, we should update o.run with the new run context.

	return true, nil
}

// call initializes the hijacking control flow, then executes the API-based Inngest function.
// Depending on the step mode, this may execute all steps or execute a single step then halt
// once the step finishes.
//
// It is the callers responsibility to handle the generated opcodes added to the invocation
// manager.
func (o *requestOwner) call(ctx context.Context) (result APIResult) {
	defer func() {
		if r := recover(); r != nil {
			callCtx := o.mgr.CallContext()

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
				o.mgr.SetStepMode(sdkrequest.StepModeYield)
				o.provider.mw.AfterExecution(ctx, callCtx, nil, nil)
				return
			}

			// TODO: How many retries does this function have?  If zero, we can ignore
			// any retries and show the error directly to the user, keeping StepModeBackground
			// checkpointing.

			panicStack := string(debug.Stack())
			o.provider.logger.Error("api handler panicked",
				"error", r,
				"run_id", o.run.RunID,
				"stack", panicStack,
			)

			o.provider.mw.AfterExecution(ctx, callCtx, nil, nil)
			o.provider.mw.OnPanic(ctx, callCtx, r, panicStack)

			// the panic is recovered here, so net/http does not abort the response.
			// without this, a client gets 200 with an empty body from a handler that
			// crashed.
			if !o.w.wroteHeader && !o.w.hijacked {
				http.Error(o.w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}

			result = o.result()
			result.Error = fmt.Sprintf("function panicked: %v.  stack:\n%s", r, panicStack)
		}
	}()

	// Execute the handler with step tooling available (o.w is already wrapped)
	o.next(o.w, o.r.WithContext(ctx))
	return o.result()
}

// result returns the API result from the response that the handler wrote.
func (o *requestOwner) result() APIResult {
	result := APIResult{
		Status:   o.w.statusCode,
		Headers:  flattenHeaders(o.w.Header()),
		Body:     o.w.body.String(),
		Duration: time.Since(o.startTime),
	}

	if o.mgr.Err() != nil {
		result.Error = o.mgr.Err().Error()
	}

	return result
}

// handleFirstCheckpoint creates a new run with the given request information.
//
// This automatically upserts the requried apps and functions via the same API
// request whilst creating a new run.
//
// It also checkpoints the first N steps (potentially including the entire function).
//
// This is a blocking operation;  handleFinalCheckpointAsync runs it in the background.
//
// This returns an optional token used when redirecting to async outputs.
func (o *requestOwner) handleFirstCheckpoint(ctx context.Context) string {
	resp, err := o.provider.api.CheckpointNewRun(ctx, o.run.RunID, o.newRunData(), o.mgr.Ops()...)
	if err != nil {
		o.provider.logger.Error("error creating new api-based inngest run", "error", err, "run_id", o.run.RunID)
		return ""
	}

	o.run = *resp
	return resp.Token
}

// handleFinalCheckpointAsync creates a new run and checkpoints every op of a
// finished run in a goroutine that the provider tracks.  it reads the request
// and the ops before it returns, because the HTTP server closes the request
// body once the handler returns.  the API call uses a new context, so it keeps
// no values or deadlines from the request.
func (o *requestOwner) handleFinalCheckpointAsync() {
	var (
		runID  = o.run.RunID
		data   = o.newRunData()
		ops    = o.mgr.Ops()
		api    = o.provider.api
		logger = o.provider.logger
	)

	o.provider.goTracked(func() {
		if _, err := api.CheckpointNewRun(context.Background(), runID, data, ops...); err != nil {
			logger.Error("error creating new api-based inngest run", "error", err, "run_id", runID)
		}
	})
}

// newRunData reads the incoming request into the run data for a new run.  it
// reads the request body unless the function config omits it.
func (o *requestOwner) newRunData() NewAPIRunData {
	var (
		requestBody []byte
		err         error
	)

	// Only read the request body if the config specifies so.
	if o.body != nil && !o.config.OmitRequestBody {
		if o.w.hijacked {
			requestBody = o.body.recorded()
		} else if requestBody, err = o.body.readAll(); err != nil {
			o.provider.logger.Error("error reading request body creating new run", "error", err)
		}

		// TODO: End to end encryption, if enabled.
	}

	// Create new API-based run in a goroutine.  This can always happen in the background whilst
	// the API is executing.
	//
	// Note that it is important that this finishes before we begin to checkpoint step data.
	scheme := httputil.GetScheme(o.r)

	return NewAPIRunData{
		Domain:      scheme + "://" + o.r.Host,
		Method:      o.r.Method,
		Path:        o.r.URL.Path,
		IP:          getClientIP(o.r),
		ContentType: o.r.Header.Get("Content-Type"),
		QueryParams: o.r.URL.RawQuery,
		Body:        requestBody,
		// Fn is the optional function slug to use.  If this is empty, our API
		// generates a slug using the URL and method.
		Fn: o.config.ID,
	}
}

// validateResumeRequestSignature validates the signature for resume requests.
// The signature is computed over the X-Run-ID header value, not the request body.
// Returns true if validation passes or if in dev mode, false otherwise.
func validateResumeRequestSignature(ctx context.Context, r *http.Request, signingKey, signingKeyFallback string) bool {
	// Skip validation in dev mode
	if env.IsDev() {
		return true
	}

	// Extract required headers
	signatureHeader := r.Header.Get(headerSignature)
	runIDHeader := r.Header.Get(headerRunID)

	// Require both headers in non-dev mode
	if signatureHeader == "" || runIDHeader == "" {
		return false
	}

	// Validate signature with primary and fallback keys using the run ID as the payload
	valid, _, err := inngestgo.ValidateRequestSignature(
		ctx,
		signatureHeader,
		signingKey,
		signingKeyFallback,
		[]byte(runIDHeader),
		false, // not dev mode
	)
	if err != nil {
		return false
	}

	return valid
}

func (o *requestOwner) appendResult(ctx context.Context, res APIResult) error {
	// When appending API results, never yield (panic).
	o.mgr.SetStepMode(sdkrequest.StepModeManual)

	defer func() {
		// Always ignore any control hijacks, just in case.
		if r := recover(); r != nil {
			if _, ok := r.(sdkrequest.ControlHijack); ok {
				return
			}
			// Repanic.
			panic(r)
		}
	}()

	var (
		responseBody []byte
		err          error
	)

	if !o.config.OmitResponseBody {
		responseBody, err = json.Marshal(res)
		if err != nil {
			return err
		}
	}

	mgrOp := o.mgr.NewOp(enums.OpcodeRunComplete, "complete")
	op := sdkrequest.GeneratorOpcode{
		ID:       mgrOp.MustHash(),
		Op:       enums.OpcodeRunComplete,
		Data:     responseBody,
		Userland: mgrOp.Userland(),
	}

	o.mgr.AppendOp(ctx, op)
	return nil
}

// withConfigUpdater allows a caller to update the request's function config from a nested
// call via ctx.
func (o *requestOwner) withConfigUpdater(ctx context.Context) context.Context {
	return context.WithValue(ctx, fnUpdateCtx, func(update func(*FnOpts)) {
		update(o.config)
	})
}
