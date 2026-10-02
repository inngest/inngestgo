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

// processRequest runs next as an Inngest API function for one request.
func processRequest(p *provider, wrap *routeWrapper, r *http.Request, w http.ResponseWriter, next http.HandlerFunc) (err error) {
	o := newRequestOwner(p, wrap, r, w, next)
	if err := o.begin(r.Context()); err != nil {
		return err
	}

	defer func() {
		err = o.finish(recover())
	}()
	o.next(o.w, o.handlerReq)
	return nil
}

// newRequestOwner prepares one request to an Inngest API function.  next is nil
// for Provider.Start, where the handler runs between begin and finish.
func newRequestOwner(p *provider, wrap *routeWrapper, r *http.Request, w http.ResponseWriter, next http.HandlerFunc) *requestOwner {
	cfg := p.resolveConfig(wrap.opts, r)

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

		wrapper:   wrap,
		config:    &cfg,
		known:     wrap.known,
		startTime: time.Now(),
	}
	owner.mgr.SetFn(servableRestFn{cfg})
	owner.w.onHeader = owner.setRunHeaders
	owner.w.capture = owner.captureResponse
	owner.w.maxBody = cfg.MaxResponseBodySize

	owner.run = CheckpointRun{
		RunID: ulid.MustNew(
			uint64(owner.startTime.UnixMilli()),
			rand.Reader,
		),
	}

	return owner
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
	// wrapper is the Handle, HandleFunc, or Middleware call that wraps next.
	wrapper *routeWrapper
	// handlerReq is the request that next gets.  an http.ServeMux behind
	// Provider.Middleware sets Pattern on this request, not on r.
	handlerReq *http.Request
	// ctx is the request context with the step manager and the config updater.
	// begin sets it.
	ctx context.Context

	// # Run-specific options

	// config represents function-specific config.  It is never nil.
	config *FnOpts
	// startTime tracks the start time of the API request. We must track this
	// as early as possible.
	startTime time.Time
	// run represents the IDs for the current sync run.
	run CheckpointRun
	// body records the request body for a new run.  It is nil when Inngest
	// resumes an existing run, or when a function's own config omits the request
	// body.  Behind Provider.Middleware it passes the body through with no copy
	// until the handler opts in.
	body *bodyRecorder
	// resumed is true when Inngest sent this request to resume a run.
	resumed bool
	// tracked is true once this request has a run.  See tracking.
	tracked bool
	// known is true once the handler is an Inngest function.  Handle,
	// HandleFunc, Start, and a resumed run set it at the start.  behind
	// Provider.Middleware, Configure sets it.
	known bool
	// warnedBody is true once this request logs that its body was read before
	// Configure.
	warnedBody bool
	// started is true once the new run is sent to the Inngest API.  after that,
	// the run keeps its function ID.
	started bool
}

// begin prepares the request before the handler runs.  it puts the step
// manager and the config updater into the request context, finds a request
// from Inngest that resumes a run, and starts the request body recorder for a
// new run.  it returns an error when a resumed run cannot load its steps, and
// it has already written the 500.  the handler must not run after that.
func (o *requestOwner) begin(ctx context.Context) error {
	// Always add the manager to context.
	ctx = sdkrequest.SetManager(ctx, o.mgr)
	// Add an updater, allowing the handler to change config via ctx with Configure
	ctx = o.withConfigUpdater(ctx)
	o.ctx = ctx

	resumed, err := o.getExistingRun(ctx)
	if err != nil {
		// Inngest sent this request to resume a run.  a 500 makes the executor send
		// it again.  without this, the request runs the handler as a new run and
		// repeats every step after the last saved one.
		http.Error(o.w, "error loading run state", http.StatusInternalServerError)
		return err
	}

	if resumed {
		o.resumed = true
		o.known = true

		// In this case, we're re-entering an existing run, which means we're now
		// running async and are responding to an Inngest's executor call.
		//
		// In this case, we always want to start returning opcodes to the HTTP request
		// directly so that the async engine can take over.
		o.mgr.SetStepMode(sdkrequest.StepModeYield)
	} else {
		// Here, we're always creating a net-new run.  This will continue to execute
		// step.run calls until either an error, an async step, or the fn finishes.
		//
		// a new run gets one attempt, so a step error is final and step.Run returns it
		// to the handler.  without this, a step error that the handler catches still
		// makes the run async, and the async response is written after the handler's
		// own response.
		maxAttempts := 1
		o.mgr.Request().CallCtx.MaxAttempts = &maxAttempts

		// record the body as the handler reads it.  without this, the new run stores
		// an empty body whenever the handler reads the request body.  the handler can
		// read the body before its first step, so this starts before the run does.
		// behind Provider.Middleware, the body passes through until Configure, so a
		// route that never opts in keeps no copy.
		switch {
		case o.known && !o.config.OmitRequestBody:
			o.body = newBodyRecorder(o.r.Body, o.config.MaxRequestBodySize, true)
		case !o.known:
			o.body = newBodyRecorder(o.r.Body, o.config.MaxRequestBodySize, o.wrapper.recordAll)
		}
		if o.body != nil {
			o.r.Body = o.body
		}
	}

	// the handler gets this request, so it must carry the body recorder.
	o.handlerReq = o.r.WithContext(ctx)
	return nil
}

// finish completes the request after the handler returns.  recovered is the
// value from recover() in the function that ran the handler.  for a request
// from Inngest, it writes the opcodes.  for a new run, it writes the async
// response or sends the finished run to Inngest in the background.
func (o *requestOwner) finish(recovered any) error {
	result := o.handlerResult(recovered)

	if o.resumed {
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

	if !o.known {
		return o.finishWithoutRun()
	}

	// Note that at this point the request would typically have finished, therefore the
	// context could be cancelled.  Stop this from breaking our API calls.
	ctx := context.WithoutCancel(o.ctx)

	if opcode.HasAsyncOps(o.mgr.Ops(), o.run.Attempt, 0) {
		// Always checkpoint first, then handle the async conversion.
		token, err := o.handleFirstCheckpoint(ctx)
		if err != nil {
			// the run does not exist in Inngest, so nothing continues it.  without
			// this, the async response sends the client to a run that never
			// finishes.
			if !o.w.wroteHeader && !o.w.hijacked {
				http.Error(o.w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
			return err
		}
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

// finishWithoutRun completes a request behind Provider.Middleware whose handler
// did not call Configure.  its steps already ran, and no run is created.  an
// async step stopped the handler, and no run exists to continue it, so the
// client gets a 500.
func (o *requestOwner) finishWithoutRun() error {
	if opcode.HasAsyncOps(o.mgr.Ops(), o.run.Attempt, 0) {
		if !o.w.wroteHeader && !o.w.hijacked {
			http.Error(o.w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return fmt.Errorf("handler for %s %s ran an async step without stephttp.Configure, so no run exists to continue it.  call stephttp.Configure before the step",
			o.r.Method, o.r.URL.Path,
		)
	}

	if !o.w.hijacked {
		o.w.Flush()
	}
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
		o.w.Header().Set("Content-Type", "application/json")
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

// tracking reports whether this request has a run.  a function gets a run when
// its first step runs, or at the start when its config sets TrackAllRequests.  a
// request from Inngest always resumes a run.  a request without a run sends no
// run headers and makes no call to the Inngest API.
func (o *requestOwner) tracking() bool {
	if !o.tracked {
		o.tracked = o.resumed || (o.known && (o.config.TrackAllRequests || len(o.mgr.Ops()) > 0))
	}
	return o.tracked
}

// setRunHeaders adds the run headers when the request has a run.  it runs just
// before the headers go to the client, so a handler that writes before its
// first step sends no run ID.
func (o *requestOwner) setRunHeaders(h http.Header) {
	if !o.tracking() {
		return
	}
	h.Set(headerRunID, o.run.RunID.String())
	h.Set("X-Inngest-SDK", version.GetVersion())
}

// captureResponse reports whether a response write is stored with the run.  a
// function stores from its first write, because a handler can respond before
// it runs its steps.  behind Provider.Middleware, a handler stores from the
// point it calls Configure, so routes that never opt in keep no copy.
// MaxResponseBodySize limits the stored copy.
func (o *requestOwner) captureResponse() bool {
	return !o.config.OmitResponseBody && o.known
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

// handlerResult returns the API result after the handler returns.  recovered
// is the value from recover() in the function that ran the handler.  a
// ControlHijack means that a step stopped the handler, eg. an async step.  any
// other value is a panic in the handler.
func (o *requestOwner) handlerResult(recovered any) APIResult {
	if recovered == nil {
		return o.result()
	}

	ctx := o.ctx
	callCtx := o.mgr.CallContext()

	// Was this us attepmting to prevent functions from continuing, using
	// panic as a crappy control flow because go doesn't have generators?
	if _, ok := recovered.(sdkrequest.ControlHijack); ok {
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
		return APIResult{}
	}

	// TODO: How many retries does this function have?  If zero, we can ignore
	// any retries and show the error directly to the user, keeping StepModeBackground
	// checkpointing.

	panicStack := string(debug.Stack())
	o.provider.logger.Error("api handler panicked",
		"error", recovered,
		"run_id", o.run.RunID,
		"stack", panicStack,
	)

	o.provider.mw.AfterExecution(ctx, callCtx, nil, nil)
	o.provider.mw.OnPanic(ctx, callCtx, recovered, panicStack)

	// the panic is recovered here, so net/http does not abort the response.
	// without this, a client gets 200 with an empty body from a handler that
	// crashed.
	if !o.w.wroteHeader && !o.w.hijacked {
		http.Error(o.w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}

	result := o.result()
	result.Error = fmt.Sprintf("function panicked: %v.  stack:\n%s", recovered, panicStack)
	return result
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
func (o *requestOwner) handleFirstCheckpoint(ctx context.Context) (string, error) {
	o.started = true
	resp, err := o.provider.api.CheckpointNewRun(ctx, o.run.RunID, o.newRunData(), o.mgr.Ops()...)
	if err != nil {
		return "", fmt.Errorf("error creating new api-based inngest run %s: %w", o.run.RunID, err)
	}

	o.run = *resp
	return resp.Token, nil
}

// handleFinalCheckpointAsync creates a new run and checkpoints every op of a
// finished run in a goroutine that the provider tracks.  it reads the request
// and the ops before it returns, because the HTTP server closes the request
// body once the handler returns.  the API call uses a new context, so it keeps
// no values or deadlines from the request.
func (o *requestOwner) handleFinalCheckpointAsync() {
	o.started = true

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
	if o.body != nil && o.body.recording && !o.config.OmitRequestBody {
		if o.w.hijacked {
			requestBody = o.body.recorded()
		} else if requestBody, err = o.body.readAll(); err != nil {
			o.provider.logger.Error("error reading request body creating new run", "error", err)
		}
		if o.body.truncated {
			o.provider.logger.Warn("api request body is larger than MaxRequestBodySize and was truncated in the run",
				"run_id", o.run.RunID,
				"max_request_body_size", o.config.MaxRequestBodySize,
			)
		}

		// TODO: End to end encryption, if enabled.
	}

	scheme := httputil.GetScheme(o.r)
	o.resolveFunctionID()

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

// routedRequest returns the request that next got, once next runs.  a route
// pattern from an http.ServeMux behind Provider.Middleware is only on that
// request.  a ServeMux behind other middleware that copies the request sets the
// pattern on a copy that the wrapper cannot see.
func (o *requestOwner) routedRequest() *http.Request {
	if o.handlerReq != nil {
		return o.handlerReq
	}
	return o.r
}

// resolveFunctionID sets an empty function ID from the route pattern.  if the
// ID is still empty, the Inngest API builds it from the method and the path,
// which creates a separate function for each value in a path such as
// /users/123.  a warning shows this once for each wrapper.
func (o *requestOwner) resolveFunctionID() {
	if o.config.ID == "" {
		o.config.ID = defaultFunctionID(o.routedRequest())
	}
	if o.config.ID != "" {
		return
	}
	o.wrapper.emptyID.Do(func() {
		o.provider.logger.Warn("api function has no ID, so each URL creates a separate function.  set FnOpts.ID or call stephttp.Configure",
			"method", o.r.Method,
			"path", o.r.URL.Path,
		)
	})
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
		if o.w.truncated {
			o.provider.logger.Warn("api response body is larger than MaxResponseBodySize and was truncated in the run",
				"run_id", o.run.RunID,
				"max_response_body_size", o.config.MaxResponseBodySize,
			)
		}
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
// call via ctx.  It is how Configure reaches this request.
func (o *requestOwner) withConfigUpdater(ctx context.Context) context.Context {
	return context.WithValue(ctx, fnUpdateCtx, func(update func(*FnOpts)) {
		cfg := *o.config
		update(&cfg)
		cfg = o.provider.resolveConfig(cfg, o.routedRequest())

		// the Inngest API already has the run under the old ID.
		if o.started && cfg.ID != o.config.ID {
			o.provider.logger.Warn("stephttp.Configure changed the function ID after the run started, and the run keeps its ID",
				"run_id", o.run.RunID,
				"id", o.config.ID,
				"ignored_id", cfg.ID,
			)
			cfg.ID = o.config.ID
		}

		*o.config = cfg
		o.known = true
		o.mgr.SetFn(servableRestFn{cfg})
		o.w.setMaxBody(cfg.MaxResponseBodySize)
		if o.body != nil {
			o.updateBodyRecorder(cfg)
		}
	})
}

// updateBodyRecorder applies a function's config to the request body recorder.
// a limit cannot go past the buffer of Provider.Middleware, because those bytes
// were not kept.  a function that stores its request body starts recording,
// which only works if the handler has not read the body yet.
func (o *requestOwner) updateBodyRecorder(cfg FnOpts) {
	limit := cfg.MaxRequestBodySize
	if buffer := o.wrapper.requestBodyBuffer; buffer > 0 && limit > buffer {
		limit = buffer
	}
	o.body.setMax(limit)

	if cfg.OmitRequestBody || o.body.startRecording() || o.warnedBody {
		return
	}
	o.warnedBody = true
	o.provider.logger.Warn("the handler read the request body before stephttp.Configure, so the run stores no request body.  call Configure first, or set MiddlewareOpts.RecordRequestBodies",
		"method", o.r.Method,
		"path", o.r.URL.Path,
	)
}
