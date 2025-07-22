package stephttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/oklog/ulid/v2"
)

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

// readRequestBody reads and restores the request body
func readRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}

	// Restore body for the handler
	r.Body = io.NopCloser(bytes.NewReader(requestBody))
	return requestBody, nil
}

func (p *provider) getExistingRun(r *http.Request, created chan CheckpointRun) (CheckpointRun, bool) {
	// Check if this is a resume request with Inngest headers
	runIDHeader := r.Header.Get(headerRunID)
	stackHeader := r.Header.Get(headerStack)
	signatureHeader := r.Header.Get(headerSignature)

	if runIDHeader == "" || stackHeader == "" || signatureHeader == "" {
		return CheckpointRun{}, false
	}

	// TODO: Validate signature

	var err error
	run := CheckpointRun{
		Signature: signatureHeader,
	}
	if run.RunID, err = ulid.Parse(runIDHeader); err != nil {
		return CheckpointRun{}, false
	}
	if err = json.Unmarshal([]byte(stackHeader), &run.Stack); err != nil {
		return CheckpointRun{}, false
	}

	// TODO: Use API to fetch run data, then resume.

	// Ensure we signal that the run is created in a goroutine, such that we do not blcok
	// checkpointing.
	go func() { created <- run }()

	return run, true
}

// createResumeManager creates a manager for resumed API requests
// getClientIP extracts the client IP from the request.
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (common in load balancers/proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP header (another common proxy header)
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr (may include port)
	if idx := strings.LastIndex(r.RemoteAddr, ":"); idx != -1 {
		return r.RemoteAddr[:idx]
	}
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
