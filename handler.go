package inngestgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sync"

	"github.com/inngest/inngest/pkg/execution/state"
	"github.com/inngest/inngest/pkg/inngest"
	"github.com/inngest/inngest/pkg/sdk"
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
	DefaultHandler Handler = NewHandler("Go app", HandlerOpts{
		UseDevServer: true,
	})

	ErrTypeMismatch = fmt.Errorf("cannot invoke function with mismatched types")
)

const (
	DefaultRegisterURL = "https://api.inngest.com/fn/register"
	DevServerURL       = "http://127.0.0.1:8288"
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

	// Env is the branch environment to deploy to.  If nil, this uses
	// os.Getenv("INNGEST_ENV").  This only deploys to branches if the
	// signing key is a branch signing key.
	Env *string

	// UseDevServer attempts to use the development server if available.
	//
	// This should ALWAYS be set to false in your production environment,
	// else calls to Inngest have increased latency as we attempt to check
	// for the dev server running on port 8288 locally.
	UseDevServer bool

	// DevServerURL sets the URL for the dev server, defaulting to
	// localhost:8288.
	DevServerURL *string

	// RegisterURL is the URL to use when registering functions.  If nil
	// this defaults to Inngest's API.
	//
	// This only needs to be set when self hosting.
	RegisterURL *string
}

func Str(s string) *string {
	return &s
}

func (h HandlerOpts) GetSigningKey() string {
	if h.SigningKey == nil {
		return os.Getenv("INNGEST_SIGNING_KEY")
	}
	return *h.SigningKey
}

func (h HandlerOpts) GetEnv() string {
	if h.SigningKey == nil {
		return os.Getenv("INNGEST_ENV")
	}
	return *h.Env
}

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
	return h
}

func (h *handler) SetAppName(name string) Handler {
	h.appName = name
	return h
}

func (h *handler) Register(funcs ...ServableFunction) {
	h.l.Lock()
	defer h.l.Unlock()
	for _, f := range funcs {
		h.funcs = append(h.funcs, f)
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Logger.Debug("received http request", "method", r.Method)

	switch r.Method {
	case http.MethodPost:
		h.invoke(w, r)
		return
	case http.MethodPut:
		if err := h.register(w, r); err != nil {
			h.Logger.Error("error registering functions", "error", err.Error())
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

	// TODO: Use open-source code
	config := sdk.RegisterRequest{
		URL:        r.URL.String(),
		V:          "1",
		DeployType: "ping",
		SDK:        "go:v0.0.1",
		AppName:    h.appName,
		Headers: sdk.Headers{
			Env:      h.GetEnv(),
			Platform: platform(),
		},
	}

	for _, fn := range h.funcs {
		c := fn.Config()

		var retries *sdk.StepRetries
		if c.Retries > 0 {
			retries = &sdk.StepRetries{
				Attempts: c.Retries,
			}
		}

		// Modify URL to contain fn ID, step params
		url := h.url(r)
		values := url.Query()
		values.Add("fnId", fn.Slug())
		values.Add("step", "step")
		url.RawQuery = values.Encode()

		f := sdk.SDKFunction{
			Name:        fn.Name(),
			Slug:        fn.Slug(),
			Idempotency: c.Idempotency,
			Triggers:    []inngest.Trigger{{}},
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

		if c.Concurrency > 0 {
			f.Concurrency = &inngest.Concurrency{
				Limit: c.Concurrency,
			}
		}

		trigger := fn.Trigger()
		if trigger.EventTrigger != nil {
			f.Triggers[0].EventTrigger = &inngest.EventTrigger{
				Event:      trigger.Event,
				Expression: trigger.Expression,
			}
		} else {
			f.Triggers[0].CronTrigger = &inngest.CronTrigger{
				Cron: trigger.Cron,
			}
		}

		config.Functions = append(config.Functions, f)
	}

	registerURL := DefaultRegisterURL
	if h.UseDevServer {
		// Check if dev server is up.  If not, error.  We can't deploy to production.
		url := DevServerURL
		if h.DevServerURL != nil {
			url = *h.DevServerURL
		}
		registerURL = fmt.Sprintf("%s/fn/register", url)
	}
	if h.RegisterURL != nil {
		registerURL = *h.RegisterURL
	}

	byt, err := json.Marshal(config)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, registerURL, bytes.NewReader(byt))
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode > 299 {
		body := map[string]string{}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return fmt.Errorf("error reading register response: %w", err)
		}
		return fmt.Errorf("Error registering functions: %s", body["error"])
	}
	return nil
}

func (h *handler) url(r *http.Request) *url.URL {
	// Get the current URL.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	u, _ := url.Parse(fmt.Sprintf("%s://%s%s", scheme, r.Host, r.RequestURI))
	return u
}

// invoke handles incoming POST calls to invoke a function.
func (h *handler) invoke(w http.ResponseWriter, r *http.Request) {

	// TODO: Check signature.

	fnID := r.URL.Query().Get("fnId")

	request := &sdkrequest.Request{}
	if err := json.NewDecoder(r.Body).Decode(request); err != nil {
		h.Logger.Error("error decoding function request", "error", err)

		w.WriteHeader(400)
		w.Write([]byte(`{"error":"malformed input"}`))
		return
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
		w.WriteHeader(410)
		w.Write([]byte(`{"error":"function not found"}`))
		return
	}

	l := h.Logger.With("fn", fnID, "call_ctx", request.CallCtx)
	l.Debug("calling function")

	resp, ops, err := invoke(r.Context(), fn, request)
	if err != nil {
		// TODO: Handle errors appropriately, including retryable/non-retryable
		// errors using nice types.

		l.Error("error calling function", "error", err)
		w.WriteHeader(500)

		// TODO: Write standard response.
		json.NewEncoder(w).Encode(map[string]any{
			"error": err.Error(),
		})
		return
	}

	if len(ops) > 0 {
		// Return the function opcode returned here so that we can re-invoke this
		// function and manage state appropriately.  Any opcode here takes precedence
		// over function return values as the function has not yet finished.
		w.WriteHeader(206)
		_ = json.NewEncoder(w).Encode(ops)
		return
	}

	// Return the function response.
	_ = json.NewEncoder(w).Encode(resp)
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
	fCtx = sdkrequest.SetManager(ctx, mgr)

	// Create a new copy of the event.
	evtPtr := reflect.New(reflect.TypeOf(sf.ZeroEvent())).Interface()
	if err := json.Unmarshal(input.Event, evtPtr); err != nil {
		return nil, nil, fmt.Errorf("error unmarshalling event for function: %w", err)
	}
	evt := reflect.ValueOf(evtPtr).Elem()

	// Create a new Input type.  We don't know ahead of time the type signature as
	// this is generic;  we instead grab the generic event element and instantiate
	// it using the data within request.
	fVal := reflect.ValueOf(sf.Func())
	inputVal := reflect.New(fVal.Type().In(1)).Elem()
	inputVal.FieldByName("Event").Set(evt)

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

func platform() string {
	// TODO: Better Platform detection, eg. vercel, lambda.
	if region := os.Getenv("AWS_REGION"); region != "" {
		return fmt.Sprintf("aws-%s", region)
	}
	if os.Getenv("VERCEL") != "" {
		return "vercel"
	}
	return ""
}