package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/inngest/inngestgo/internal"
	"github.com/inngest/inngestgo/internal/fn"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
)

// MiddlewareOpts contains configuration for the API middleware
type MiddlewareOpts struct {
	// BaseURL is the Inngest API base URL (e.g., "https://api.inngest.com")
	BaseURL string
	// SigningKey is the Inngest signing key for authentication
	SigningKey string
	// AppID is the application identifier
	AppID string
	// Domain is the domain for this API (e.g., "api.mycompany.com")
	Domain string
}

// Middleware wraps HTTP handlers to provide Inngest step tooling for API functions
type Middleware struct {
	apiManager APIManager
	opts       MiddlewareOpts
	mw         *middleware.MiddlewareManager
}

// NewMiddleware creates a new API middleware instance
func NewMiddleware(opts MiddlewareOpts) *Middleware {
	apiManager := NewAPIManager(opts.BaseURL, opts.SigningKey)
	
	// Create a middleware manager for step execution hooks
	mw := middleware.NewManager()
	
	return &Middleware{
		apiManager: apiManager,
		opts:       opts,
		mw:         mw,
	}
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

		// Create metadata from request
		metadata := map[string]interface{}{
			"ip":         getClientIP(r),
			"user_agent": r.Header.Get("User-Agent"),
			"headers":    r.Header,
		}

		// Create API run in Inngest
		runID, err := m.apiManager.CreateAPIRun(
			r.Context(),
			m.opts.Domain,
			r.URL.Path,
			r.Method,
			requestBody,
			metadata,
		)
		if err != nil {
			// Log error but don't fail the request
			// TODO: Add proper logging
			runID = fmt.Sprintf("local-%d", time.Now().UnixNano())
		}

		// Create sync invocation manager
		syncMgr := NewSyncInvocationManager(
			runID,
			m.apiManager,
			m.opts.SigningKey,
			m.mw,
			nil, // No specific function for API handlers
		)

		// Set up context with managers
		ctx := sdkrequest.SetManager(r.Context(), syncMgr)
		ctx = internal.SetMiddlewareManagerInContext(ctx, m.mw)

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

		if syncMgr.Err() != nil {
			result.Error = syncMgr.Err().Error()
		}

		// Store result in background
		_ = m.apiManager.StoreResult(r.Context(), runID, result)
	}
}

// getClientIP extracts the client IP from the request
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