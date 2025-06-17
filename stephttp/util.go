package stephttp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/oklog/ulid/v2"
)

type existingRun struct {
	RunID     ulid.ULID
	Stack     []string
	Signature string
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

func getExistingRun(r *http.Request) (existingRun, bool) {
	// Check if this is a resume request with Inngest headers
	runIDHeader := r.Header.Get(headerRunID)
	stackHeader := r.Header.Get(headerStack)
	signatureHeader := r.Header.Get(headerSignature)

	if runIDHeader == "" || stackHeader == "" || signatureHeader == "" {
		return existingRun{}, false
	}

	var err error
	er := existingRun{
		Signature: signatureHeader,
	}
	if er.RunID, err = ulid.Parse(runIDHeader); err != nil {
		return existingRun{}, false
	}
	if err = json.Unmarshal([]byte(stackHeader), &er.Stack); err != nil {
		return existingRun{}, false
	}
	return er, true
}

// createResumeManager creates a manager for resumed API requests
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
