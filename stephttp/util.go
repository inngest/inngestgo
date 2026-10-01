package stephttp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/oklog/ulid/v2"
)

func defaultRedirectURL(o SetupOpts, runID ulid.ULID, token string) string {
	return fmt.Sprintf(
		"%s/v1/http/runs/%s/output?token=%s",
		o.baseURL(),
		runID,
		token,
	)
}

// responseWriter captures the response for storing as the API result
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
	hijacked   bool
	// wroteHeader is true once the status line goes to the client.  after
	// that, the status code cannot change.
	wroteHeader bool

	// onHeader runs once, just before the status line and headers go to the
	// client.  it is the last point at which a header can be added.
	onHeader func(http.Header)
	// capture reports whether a write is copied into body.  nil copies every
	// write.  a write that it skips is missing from the stored response.
	capture func() bool
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		body:           &bytes.Buffer{},
		hijacked:       false,
	}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.beforeHeader()
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	rw.beforeHeader()
	// Don't capture response body after hijacking
	if !rw.hijacked && (rw.capture == nil || rw.capture()) {
		rw.body.Write(data)
	}
	return rw.ResponseWriter.Write(data)
}

// beforeHeader marks the headers as sent and runs onHeader the first time.
func (rw *responseWriter) beforeHeader() {
	if rw.wroteHeader {
		return
	}
	rw.wroteHeader = true
	if rw.onHeader != nil {
		rw.onHeader(rw.Header())
	}
}

// Hijack implements http.Hijacker interface, passing through to the underlying writer if supported
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}

	// Mark as hijacked so we stop capturing response data
	rw.hijacked = true

	return hijacker.Hijack()
}

// Flush implements http.Flusher interface, passing through to the underlying writer if supported
func (rw *responseWriter) Flush() {
	rw.beforeHeader()
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Push implements http.Pusher interface, passing through to the underlying writer if supported
func (rw *responseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := rw.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

// bodyRecorder copies the request body as the handler reads it.  the new run
// stores this copy, because the handler reads the body before the run is
// checkpointed and nothing else can read it again.
type bodyRecorder struct {
	body   io.ReadCloser
	buf    bytes.Buffer
	closed bool
}

func newBodyRecorder(body io.ReadCloser) *bodyRecorder {
	if body == nil {
		body = http.NoBody
	}
	return &bodyRecorder{body: body}
}

func (b *bodyRecorder) Read(p []byte) (int, error) {
	if b.closed {
		return 0, http.ErrBodyReadAfterClose
	}
	n, err := b.body.Read(p)
	b.buf.Write(p[:n])
	return n, err
}

// Close stops the handler from reading more of the body.  it leaves the
// underlying body open so that readAll can read the rest.  the HTTP server
// closes the underlying body when the handler returns.
func (b *bodyRecorder) Close() error {
	b.closed = true
	return nil
}

// readAll reads the part of the body that the handler did not read and returns
// the full body.  call it before the handler returns, because the HTTP server
// closes the underlying body after that.
func (b *bodyRecorder) readAll() ([]byte, error) {
	_, err := io.Copy(&b.buf, b.body)
	return b.buf.Bytes(), err
}

// recorded returns only the part of the body that the handler read.  use it
// after a hijack, because the request body must not be read after that.
func (b *bodyRecorder) recorded() []byte {
	return b.buf.Bytes()
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
