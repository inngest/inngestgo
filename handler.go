package inngestgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/execution/state"
	"github.com/inngest/inngest/pkg/inngest"
	"github.com/inngest/inngest/pkg/publicerr"
	"github.com/inngest/inngest/pkg/sdk"
	"github.com/inngest/inngestgo/errors"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/step"
	"golang.org/x/exp/slog"
)

var (
	// DefaultHandler provides a default handler for registering and serving functions
	// globally.
	//
	// It's recommended to call SetOptions() to set configuration before serving
	// this in production environments;  this is set up for development and will
	// attempt to connect to the dev server.
	DefaultHandler Handler = NewHandler("Go app", HandlerOpts{})

	ErrTypeMismatch = fmt.Errorf("cannot invoke function with mismatched types")

	// DefaultMaxBodySize is the default maximum size read within a single incoming
	// invoke request (100MB).
	DefaultMaxBodySize = 1024 * 1024 * 100

	capabilities = sdk.Capabilities{
		TrustProbe: true,
	}
)

const (
	defaultRegisterURL = "https://api.inngest.com/fn/register"
)

// Register adds the given functions to the default handler for serving.  You must register all
// functions with a handler prior to serving the handler for them to be enabled.
func Register(funcs ...ServableFunction) {
	DefaultHandler.Register(funcs...)
}

// Serve serves all registered functions within the default handler.
func Serve(w http.ResponseWriter, r *http.Request) {
	DefaultHandler.ServeHTTP(w, r)
}

type HandlerOpts struct {
	// Logger is the structured logger to use from Go's builtin structured
	// logging package.
	Logger *slog.Logger

	// SigningKey is the signing key for your app.  If nil, this defaults
	// to os.Getenv("INNGEST_SIGNING_KEY").
	SigningKey *string

	// SigningKeyFallback is the fallback signing key for your app. If nil, this
	// defaults to os.Getenv("INNGEST_SIGNING_KEY_FALLBACK").
	SigningKeyFallback *string

	// Env is the branch environment to deploy to.  If nil, this uses
	// os.Getenv("INNGEST_ENV").  This only deploys to branches if the
	// signing key is a branch signing key.
	Env *string

	// RegisterURL is the URL to use when registering functions.  If nil
	// this defaults to Inngest's API.
	//
	// This only needs to be set when self hosting.
	RegisterURL *string

	// MaxBodySize is the max body size to read for incoming invoke requests
	MaxBodySize int

	// URL that the function is served at.  If not supplied this is taken from
	// the incoming request's data.
	URL *url.URL

	// UseStreaming enables streaming - continued writes to the HTTP writer.  This
	// differs from true streaming in that we don't support server-sent events.
	UseStreaming bool
}

// GetSigningKey returns the signing key defined within HandlerOpts, or the default
// defined within INNGEST_SIGNING_KEY.
//
// This is the private key used to register functions and communicate with the private
// API.
func (h HandlerOpts) GetSigningKey() string {
	if h.SigningKey == nil {
		return os.Getenv("INNGEST_SIGNING_KEY")
	}
	return *h.SigningKey
}

// GetSigningKeyFallback returns the signing key fallback defined within
// HandlerOpts, or the default defined within INNGEST_SIGNING_KEY_FALLBACK.
//
// This is the fallback private key used to register functions and communicate
// with the private API. If a request fails auth with the signing key then we'll
// try again with the fallback
func (h HandlerOpts) GetSigningKeyFallback() string {
	if h.SigningKeyFallback == nil {
		return os.Getenv("INNGEST_SIGNING_KEY_FALLBACK")
	}
	return *h.SigningKeyFallback
}

// GetEnv returns the env defined within HandlerOpts, or the default
// defined within INNGEST_ENV.
//
// This is the environment name used for preview/branch environments within Inngest.
func (h HandlerOpts) GetEnv() string {
	if h.Env == nil {
		return os.Getenv("INNGEST_ENV")
	}
	return *h.Env
}

// GetRegisterURL returns the registration URL defined wtihin HandlerOpts,
// defaulting to the production Inngest URL if nil.
func (h HandlerOpts) GetRegisterURL() string {
	if h.RegisterURL == nil {
		return "https://www.inngest.com/fn/register"
	}
	return *h.RegisterURL
}

// Handler represents a handler which serves the Inngest API via HTTP.  This provides
// function registration to Inngest, plus the invocation of registered functions via
// an HTTP POST.
type Handler interface {
	http.Handler

	// SetAppName updates the handler's app name.  This is used to group functions
	// and track deploys within the UI.
	SetAppName(name string) Handler

	// SetOptions sets the handler's options used to register functions.
	SetOptions(h HandlerOpts) Handler

	// Register registers the given functions with the handler, allowing them to
	// be invoked by Inngest.
	Register(...ServableFunction)
}

// NewHandler returns a new Handler for serving Inngest functions.
func NewHandler(appName string, opts HandlerOpts) Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	if opts.MaxBodySize == 0 {
		opts.MaxBodySize = DefaultMaxBodySize
	}

	return &handler{
		HandlerOpts: opts,
		appName:     appName,
		funcs:       []ServableFunction{},
	}
}

type handler struct {
	HandlerOpts

	appName string
	funcs   []ServableFunction
	// lock prevents reading the function maps while serving
	l sync.RWMutex
}

func (h *handler) SetOptions(opts HandlerOpts) Handler {
	h.HandlerOpts = opts

	if opts.MaxBodySize == 0 {
		opts.MaxBodySize = DefaultMaxBodySize
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return h
}

func (h *handler) SetAppName(name string) Handler {
	h.appName = name
	return h
}

func (h *handler) Register(funcs ...ServableFunction) {
	h.l.Lock()
	defer h.l.Unlock()

	// Create a map of functions by slug.  If we're registering a function
	// that already exists, clear it.
	slugs := map[string]ServableFunction{}
	for _, f := range h.funcs {
		slugs[f.Slug()] = f
	}

	for _, f := range funcs {
		slugs[f.Slug()] = f
	}

	newFuncs := make([]ServableFunction, len(slugs))
	i := 0
	for _, f := range slugs {
		newFuncs[i] = f
		i++
	}

	h.funcs = newFuncs
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Logger.Debug("received http request", "method", r.Method)
	SetBasicResponseHeaders(w)

	switch r.Method {
	case http.MethodGet:
		if err := h.introspect(w, r); err != nil {
			_ = publicerr.WriteHTTP(w, err)
		}
		return
	case http.MethodPost:
		probe := r.URL.Query().Get("probe")
		if probe == "trust" {
			h.trust(r.Context(), w, r)
			return
		}

		if err := h.invoke(w, r); err != nil {
			_ = publicerr.WriteHTTP(w, err)
		}
		return
	case http.MethodPut:
		if err := h.register(w, r); err != nil {
			h.Logger.Error("error registering functions", "error", err.Error())

			w.WriteHeader(500)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message": err.Error(),
			})
		}
		return
	}
}

// register self-registers the handler's functions with Inngest.  This upserts
// all functions and automatically allows all functions to immediately be triggered
// by incoming events or schedules.
func (h *handler) register(w http.ResponseWriter, r *http.Request) error {
	h.l.Lock()
	defer h.l.Unlock()

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host

	// Get the sync ID from the URL and then remove it, since we don't want the
	// sync ID to show in the function URLs (that would affect the checksum and
	// is ugly in the UI)
	qp := r.URL.Query()
	syncID := qp.Get("deployId")
	qp.Del("deployId")
	r.URL.RawQuery = qp.Encode()

	pathAndParams := r.URL.String()

	config := sdk.RegisterRequest{
		URL:        fmt.Sprintf("%s://%s%s", scheme, host, pathAndParams),
		V:          "1",
		DeployType: "ping",
		SDK:        HeaderValueSDK,
		AppName:    h.appName,
		Headers: sdk.Headers{
			Env:      h.GetEnv(),
			Platform: platform(),
		},
		Capabilities: capabilities,
	}

	for _, fn := range h.funcs {
		c := fn.Config()

		var retries *sdk.StepRetries
		if c.Retries != nil {
			retries = &sdk.StepRetries{
				Attempts: *c.Retries,
			}
		}

		// Modify URL to contain fn ID, step params
		url := h.url(r)
		values := url.Query()
		values.Set("fnId", fn.Slug())
		values.Set("step", "step")
		url.RawQuery = values.Encode()

		f := sdk.SDKFunction{
			Name:        fn.Name(),
			Slug:        h.appName + "-" + fn.Slug(),
			Idempotency: c.Idempotency,
			Priority:    fn.Config().Priority,
			Triggers:    inngest.MultipleTriggers{},
			RateLimit:   fn.Config().GetRateLimit(),
			Cancel:      fn.Config().Cancel,
			Timeouts:    (*inngest.Timeouts)(fn.Config().Timeouts),
			Throttle:    (*inngest.Throttle)(fn.Config().Throttle),
			Steps: map[string]sdk.SDKStep{
				"step": {
					ID:      "step",
					Name:    fn.Name(),
					Retries: retries,
					Runtime: map[string]any{
						"url": url.String(),
					},
				},
			},
		}

		if c.Debounce != nil {
			f.Debounce = &inngest.Debounce{
				Key:    &c.Debounce.Key,
				Period: c.Debounce.Period.String(),
			}
			if c.Debounce.Timeout != nil {
				str := c.Debounce.Timeout.String()
				f.Debounce.Timeout = &str
			}
		}

		if c.BatchEvents != nil {
			f.EventBatch = map[string]any{
				"maxSize": c.BatchEvents.MaxSize,
				"timeout": c.BatchEvents.Timeout,
				"key":     c.BatchEvents.Key,
			}
		}

		if len(c.Concurrency) > 0 {
			// Marshal as an array, as the sdk/handler unmarshals correctly.
			f.Concurrency = &inngest.ConcurrencyLimits{Limits: c.Concurrency}
		}

		triggers := fn.Trigger().Triggers()
		for _, trigger := range triggers {
			if trigger.EventTrigger != nil {
				f.Triggers = append(f.Triggers, inngest.Trigger{
					EventTrigger: &inngest.EventTrigger{
						Event:      trigger.Event,
						Expression: trigger.Expression,
					},
				})
			} else {
				f.Triggers = append(f.Triggers, inngest.Trigger{
					CronTrigger: &inngest.CronTrigger{
						Cron: trigger.Cron,
					},
				})
			}
		}

		config.Functions = append(config.Functions, f)
	}

	registerURL := defaultRegisterURL
	if IsDev() {
		// TODO: Check if dev server is up.  If not, error.  We can't deploy to production.
		registerURL = fmt.Sprintf("%s/fn/register", DevServerURL())
	}
	if h.RegisterURL != nil {
		registerURL = *h.RegisterURL
	}

	createRequest := func() (*http.Request, error) {
		byt, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("error marshalling function config: %w", err)
		}

		req, err := http.NewRequest(http.MethodPost, registerURL, bytes.NewReader(byt))
		if err != nil {
			return nil, fmt.Errorf("error creating new request: %w", err)
		}
		if syncID != "" {
			qp := req.URL.Query()
			qp.Set("deployId", syncID)
			req.URL.RawQuery = qp.Encode()
		}

		// If the request specifies a server kind then include it as an expectation
		// in the outgoing request
		if r.Header.Get(HeaderKeyServerKind) != "" {
			req.Header.Set(
				HeaderKeyExpectedServerKind,
				r.Header.Get(HeaderKeyServerKind),
			)
		}

		if h.GetEnv() != "" {
			req.Header.Add(HeaderKeyEnv, h.GetEnv())
		}

		SetBasicRequestHeaders(req)

		return req, nil
	}

	resp, err := fetchWithAuthFallback(
		createRequest,
		h.GetSigningKey(),
		h.GetSigningKeyFallback(),
	)
	if err != nil {
		return fmt.Errorf("error performing registration request: %w", err)
	}
	if resp.StatusCode > 299 {
		body := map[string]any{}
		byt, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(byt, &body); err != nil {
			return fmt.Errorf("error reading register response: %w\n\n%s", err, byt)
		}
		return fmt.Errorf("Error registering functions: %s", body["error"])
	}
	return nil
}

func (h *handler) url(r *http.Request) *url.URL {
	if h.URL != nil {
		return h.URL
	}

	// Get the current URL.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	u, _ := url.Parse(fmt.Sprintf("%s://%s%s", scheme, r.Host, r.RequestURI))
	return u
}

// invoke handles incoming POST calls to invoke a function, delegating to invoke() after validating
// the request.
func (h *handler) invoke(w http.ResponseWriter, r *http.Request) error {
	var sig string
	defer r.Body.Close()

	if !IsDev() {
		if sig = r.Header.Get(HeaderKeySignature); sig == "" {
			return publicerr.Error{
				Message: "unauthorized",
				Status:  401,
			}
		}
	}

	max := h.HandlerOpts.MaxBodySize
	if max == 0 {
		max = DefaultMaxBodySize
	}
	byt, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(max)))
	if err != nil {
		h.Logger.Error("error decoding function request", "error", err)
		return publicerr.Error{
			Message: "Error reading request",
			Status:  500,
		}
	}

	if valid, _, err := ValidateSignature(
		r.Context(),
		sig,
		h.GetSigningKey(),
		h.GetSigningKeyFallback(),
		byt,
	); !valid {
		h.Logger.Error("unauthorized inngest invoke request", "error", err)
		return publicerr.Error{
			Message: "unauthorized",
			Status:  401,
		}
	}

	fnID := r.URL.Query().Get("fnId")

	request := &sdkrequest.Request{}
	if err := json.Unmarshal(byt, request); err != nil {
		h.Logger.Error("error decoding function request", "error", err)
		return publicerr.Error{
			Message: "malformed input",
			Status:  400,
		}
	}

	if request.UseAPI {
		// TODO: implement this
		// retrieve data from API
		// request.Steps =
		// request.Events =
		_ = 0 // no-op to avoid linter error
	}

	h.l.RLock()
	var fn ServableFunction
	for _, f := range h.funcs {
		if f.Slug() == fnID {
			fn = f
			break
		}
	}
	h.l.RUnlock()

	if fn == nil {
		// XXX: This is a 500 within the JS SDK.  We should probably change
		// the JS SDK's status code to 410.  404 indicates that the overall
		// API for serving Inngest isn't found.
		return publicerr.Error{
			Message: fmt.Sprintf("function not found: %s", fnID),
			Status:  410,
		}
	}

	l := h.Logger.With("fn", fnID, "call_ctx", request.CallCtx)
	l.Debug("calling function")

	stream, streamCancel := context.WithCancel(context.Background())
	if h.UseStreaming {
		w.WriteHeader(201)
		go func() {
			for {
				if stream.Err() != nil {
					return
				}
				_, _ = w.Write([]byte(" "))
				<-time.After(5 * time.Second)
			}
		}()
	}

	// Invoke the function, then immediately stop the streaming buffer.
	resp, ops, err := invoke(r.Context(), fn, request)
	streamCancel()

	// NOTE: When triggering step errors, we should have an OpcodeStepError
	// within ops alongside an error.  We can safely ignore that error, as its
	// onyl used for checking wither the step used a NoRetryError or RetryAtError
	//
	// For that reason, we check those values first.
	noRetry := errors.IsNoRetryError(err)
	retryAt := errors.GetRetryAtTime(err)
	if len(ops) == 1 && ops[0].Op == enums.OpcodeStepError {
		// Now we've handled error types we can ignore step
		// errors safely.
		err = nil
	}

	// Now that we've handled the OpcodeStepError, if we *still* ahve
	// a StepError kind returned from a function we must have an unhandled
	// step error.  This is a NonRetryableError, as the most likely code is:
	//
	// 	_, err := step.Run(ctx, func() (any, error) { return fmt.Errorf("") })
	// 	if err != nil {
	// 	     return err
	// 	}
	if errors.IsStepError(err) {
		err = fmt.Errorf("Unhandled step error: %s", err)
		noRetry = true
	}

	if h.UseStreaming {
		if err != nil {
			// TODO: Add retry-at.
			return json.NewEncoder(w).Encode(StreamResponse{
				StatusCode: 500,
				Body:       fmt.Sprintf("error calling function: %s", err.Error()),
				NoRetry:    noRetry,
				RetryAt:    retryAt,
			})
		}
		if len(ops) > 0 {
			return json.NewEncoder(w).Encode(StreamResponse{
				StatusCode: 206,
				Body:       ops,
			})
		}
		return json.NewEncoder(w).Encode(StreamResponse{
			StatusCode: 200,
			Body:       resp,
		})
	}

	// These may be added even for 2xx codes with step errors.
	if noRetry {
		w.Header().Add(HeaderKeyNoRetry, "true")
	}
	if retryAt != nil {
		w.Header().Add(HeaderKeyRetryAfter, retryAt.Format(time.RFC3339))
	}

	if err != nil {
		l.Error("error calling function", "error", err)
		return publicerr.Error{
			Message: fmt.Sprintf("error calling function: %s", err.Error()),
			Status:  500,
		}
	}

	if len(ops) > 0 {
		// Return the function opcode returned here so that we can re-invoke this
		// function and manage state appropriately.  Any opcode here takes precedence
		// over function return values as the function has not yet finished.
		w.WriteHeader(206)
		return json.NewEncoder(w).Encode(ops)
	}

	// Return the function response.
	return json.NewEncoder(w).Encode(resp)
}

type insecureIntrospection struct {
	FunctionCount int    `json:"function_count"`
	HasEventKey   bool   `json:"has_event_key"`
	HasSigningKey bool   `json:"has_signing_key"`
	Mode          string `json:"mode"`
}

type secureIntrospection struct {
	insecureIntrospection
	Capabilities           sdk.Capabilities `json:"capabilities"`
	SigningKeyFallbackHash *string          `json:"signing_key_fallback_hash"`
	SigningKeyHash         *string          `json:"signing_key_hash"`
}

func (h *handler) introspect(w http.ResponseWriter, r *http.Request) error {
	defer r.Body.Close()

	mode := "cloud"
	if IsDev() {
		mode = "dev"
	}

	sig := r.Header.Get(HeaderKeySignature)
	valid, _, _ := ValidateSignature(
		r.Context(),
		sig,
		h.GetSigningKey(),
		h.GetSigningKeyFallback(),
		[]byte{},
	)
	if valid {
		var signingKeyHash *string
		if h.GetSigningKey() != "" {
			key, err := hashedSigningKey([]byte(h.GetSigningKey()))
			if err != nil {
				return fmt.Errorf("error hashing signing key: %w", err)
			}
			hash := string(key)
			signingKeyHash = &hash
		}

		var signingKeyFallbackHash *string
		if h.GetSigningKeyFallback() != "" {
			key, err := hashedSigningKey([]byte(h.GetSigningKeyFallback()))
			if err != nil {
				return fmt.Errorf("error hashing signing key fallback: %w", err)
			}
			hash := string(key)
			signingKeyFallbackHash = &hash
		}

		introspection := secureIntrospection{
			insecureIntrospection: insecureIntrospection{
				FunctionCount: len(h.funcs),
				HasEventKey:   os.Getenv("INNGEST_EVENT_KEY") != "",
				HasSigningKey: h.GetSigningKey() != "",
				Mode:          mode,
			},
			Capabilities:           capabilities,
			SigningKeyFallbackHash: signingKeyFallbackHash,
			SigningKeyHash:         signingKeyHash,
		}

		w.Header().Set(HeaderKeyContentType, "application/json")
		return json.NewEncoder(w).Encode(introspection)
	}

	introspection := insecureIntrospection{
		FunctionCount: len(h.funcs),
		HasEventKey:   os.Getenv("INNGEST_EVENT_KEY") != "",
		HasSigningKey: h.GetSigningKey() != "",
		Mode:          mode,
	}

	w.Header().Set(HeaderKeyContentType, "application/json")
	return json.NewEncoder(w).Encode(introspection)

}

type trustProbeResponse struct {
	Error *string `json:"error,omitempty"`
}

func (h *handler) trust(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
) {
	w.Header().Add("Content-Type", "application/json")
	sig := r.Header.Get(HeaderKeySignature)
	if sig == "" {
		_ = publicerr.WriteHTTP(w, publicerr.Error{
			Message: fmt.Sprintf("missing %s header", HeaderKeySignature),
			Status:  401,
		})
		return
	}

	valid, key, err := ValidateSignature(
		ctx,
		r.Header.Get("X-Inngest-Signature"),
		h.GetSigningKey(),
		h.GetSigningKeyFallback(),
		[]byte{},
	)
	if err != nil {
		_ = publicerr.WriteHTTP(w, publicerr.Error{
			Message: fmt.Sprintf("error validating signature: %s", err),
		})
		return
	}
	if !valid {
		_ = publicerr.WriteHTTP(w, publicerr.Error{
			Message: "invalid signature",
			Status:  401,
		})
		return
	}

	byt, err := json.Marshal(trustProbeResponse{})
	if err != nil {
		_ = publicerr.WriteHTTP(w, err)
		return
	}

	w.Header().Add("X-Inngest-Signature", Sign(ctx, time.Now(), []byte(key), byt))
	w.WriteHeader(200)
	_, err = w.Write(byt)
	if err != nil {
		h.Logger.Error("error writing trust probe response", "error", err)
	}
}

type StreamResponse struct {
	StatusCode int               `json:"status"`
	Body       any               `json:"body"`
	RetryAt    *time.Time        `json:"retryAt"`
	NoRetry    bool              `json:"noRetry"`
	Headers    map[string]string `json:"headers"`
}

// invoke calls a given servable function with the specified input event.  The input event must
// be fully typed.
func invoke(ctx context.Context, sf ServableFunction, input *sdkrequest.Request) (any, []state.GeneratorOpcode, error) {
	if sf.Func() == nil {
		// This should never happen, but as sf.Func returns a nillable type we
		// must check that the function exists.
		return nil, nil, fmt.Errorf("no function defined")
	}

	// Create a new context.  This context is cancellable and stores the opcode that ran
	// within a step.  This allows us to prevent any execution of future tools after a
	// tool has run.
	fCtx, cancel := context.WithCancel(context.Background())
	// This must be a pointer so that it can be mutated from within function tools.
	mgr := sdkrequest.NewManager(cancel, input)
	fCtx = sdkrequest.SetManager(fCtx, mgr)

	// Create a new Input type.  We don't know ahead of time the type signature as
	// this is generic;  we instead grab the generic event element and instantiate
	// it using the data within request.
	fVal := reflect.ValueOf(sf.Func())
	inputVal := reflect.New(fVal.Type().In(1)).Elem()

	// If we have an actual value to add to the event, vs `Input[any]`, set it.
	if sf.ZeroEvent() != nil {
		eventType := reflect.TypeOf(sf.ZeroEvent())

		// Create a new copy of the event.
		evtPtr := reflect.New(eventType).Interface()
		if err := json.Unmarshal(input.Event, evtPtr); err != nil {
			return nil, nil, fmt.Errorf("error unmarshalling event for function: %w", err)
		}
		evt := reflect.ValueOf(evtPtr).Elem()
		inputVal.FieldByName("Event").Set(evt)

		// events
		sliceType := reflect.SliceOf(eventType)
		evtList := reflect.MakeSlice(sliceType, 0, len(input.Events))

		for _, rawjson := range input.Events {
			newEvent := reflect.New(eventType).Interface()

			if err := json.Unmarshal(rawjson, &newEvent); err != nil {
				return nil, nil, fmt.Errorf("non-zero event: error unmarshalling event in event list: %w", err)
			}

			evtList = reflect.Append(evtList, reflect.ValueOf(newEvent).Elem())
		}
		inputVal.FieldByName("Events").Set(evtList)
	} else {
		// Use a raw map to hold the input.
		val := map[string]any{}
		if err := json.Unmarshal(input.Event, &val); err != nil {
			return nil, nil, fmt.Errorf("error unmarshalling event for function: %w", err)
		}
		inputVal.FieldByName("Event").Set(reflect.ValueOf(val))

		// events
		events := make([]any, len(input.Events))
		for i, rawjson := range input.Events {
			var val map[string]any

			if err := json.Unmarshal(rawjson, &val); err != nil {
				return nil, nil, fmt.Errorf("zero event: error unmarshalling event in event list: %w", err)
			}

			events[i] = val
		}
		inputVal.FieldByName("Events").Set(reflect.ValueOf(events))
	}

	// Set InputCtx
	callCtx := InputCtx{
		Env:        input.CallCtx.Env,
		FunctionID: input.CallCtx.FunctionID,
		RunID:      input.CallCtx.RunID,
		StepID:     input.CallCtx.StepID,
		Attempt:    input.CallCtx.Attempt,
	}
	inputVal.FieldByName("InputCtx").Set(reflect.ValueOf(callCtx))

	var (
		res       []reflect.Value
		panickErr error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Was this us attepmting to prevent functions from continuing, using
				// panic as a crappy control flow because go doesn't have generators?
				//
				// XXX: I'm not very happy with using this;  it is dirty
				if _, ok := r.(step.ControlHijack); ok {
					return
				}
				panickErr = fmt.Errorf("function panicked: %v", r)
			}
		}()

		// Call the defined function with the input data.
		res = fVal.Call([]reflect.Value{
			reflect.ValueOf(fCtx),
			inputVal,
		})
	}()

	var err error
	if panickErr != nil {
		err = panickErr
	} else if mgr.Err() != nil {
		// This is higher precedence than a return error.
		err = mgr.Err()
	} else if res != nil && !res[1].IsNil() {
		// The function returned an error.
		err = res[1].Interface().(error)
	}

	var response any
	if res != nil {
		// Panicking in tools interferes with grabbing the response;  it's always
		// an empty array if tools panic to hijack control flow.
		response = res[0].Interface()
	}

	return response, mgr.Ops(), err
}
