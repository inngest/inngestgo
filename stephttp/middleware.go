package stephttp

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/inngest/inngestgo/internal/event"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/oklog/ulid/v2"
)

// MiddlewareOpts contains configuration for the API middleware
type MiddlewareOpts struct {
	// SigningKey is the Inngest signing key for authentication
	SigningKey string
	// AppID is the application identifier
	AppID string
	// Domain is the domain for this API (e.g., "api.mycompany.com")
	Domain string
}

// Middleware wraps HTTP handlers to provide Inngest step tooling for API functions
type Middleware struct {
	opts MiddlewareOpts
	mw   *middleware.MiddlewareManager

	maxRequestReadLimit int
	baseURL             string
}

type MiddlewareOpt func(mw *Middleware)

func WithRequestReadLimit(limit int) MiddlewareOpt {
	return func(mw *Middleware) {
		mw.maxRequestReadLimit = limit
	}
}

func WithBaseURL(url string) MiddlewareOpt {
	return func(mw *Middleware) {
		mw.baseURL = url
	}
}

func WithInngestMiddleware(mw func() middleware.Middleware) MiddlewareOpt {
	return func(httpmw *Middleware) {
		httpmw.mw.Add(mw)
	}
}

// NewMiddleware creates a new API middleware instance
func NewMiddleware(opts MiddlewareOpts, optionalOpts ...MiddlewareOpt) *Middleware {
	// apiManager := NewAPIManager(opts.BaseURL, opts.SigningKey)

	// Create a middleware manager for step execution hooks
	mw := middleware.New()

	http := &Middleware{
		opts: opts,
		mw:   mw,
	}

	for _, o := range optionalOpts {
		o(http)
	}

	return http
}

// responseWriter captures the response for storing as the API result
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		body:           &bytes.Buffer{},
	}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	rw.body.Write(data)
	return rw.ResponseWriter.Write(data)
}

// Handler wraps an HTTP handler to provide Inngest step tooling
func (m *Middleware) Handler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: Is this an incoming request with existing steps?
		var runID *ulid.ULID

		if runID == nil {
			startTime := time.Now()

			// Read request body for function input
			var requestBody []byte
			if r.Body != nil {
				var err error
				requestBody, err = io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "Failed to read request body", http.StatusInternalServerError)
					return
				}
				// Restore body for the handler
				r.Body = io.NopCloser(bytes.NewReader(requestBody))
			}

			event := event.GenericEvent[apiEventData]{
				Name: "inngest/api.request",
				Data: apiEventData{
					IP:      getClientIP(r),
					Method:  r.Method,
					Path:    r.URL.Path,
					Headers: flattenHeaders(r.Header),
				},
			}
		}

		// TODO Create a new function run in Inngest for this api request.
		// TODO: Create sync invocation manager

		// Set up context with managers
		ctx := sdkrequest.SetManager(r.Context(), syncMgr)

		// Wrap response writer to capture output
		rw := newResponseWriter(w)

		// Execute the handler with step tooling available
		// API functions shouldn't panic with ControlHijack due to StepModeContinue
		next(rw, r.WithContext(ctx))

		duration := time.Since(startTime)

		// Store the API result
		result := APIResult{
			StatusCode: rw.statusCode,
			Headers:    flattenHeaders(rw.Header()),
			Body:       rw.body.Bytes(),
			Duration:   duration,
		}

		// if syncMgr.Err() != nil {
		// 	result.Error = syncMgr.Err().Error()
		// }

		// Finalize run and store the output via an API call
		// _ = m.apiManager.StoreResult(r.Context(), runID, result)
	}
}

// getClientIP extracts the client IP from the request.
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// flattenHeaders converts http.Header to map[string]string
func flattenHeaders(headers http.Header) map[string]string {
	result := make(map[string]string)
	for key, values := range headers {
		if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result
}

type apiEventData struct {
	IP     string
	Method string
	Path   string
	// Headers stores the request headers.
	// TODO: This should have the authorization header removed.
	Headers map[string]string
	// ContentType represents the content type of the incoming request.
	ContentType string
	// Data represents the request body
	Data []byte
}
