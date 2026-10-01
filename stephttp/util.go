package stephttp

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

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

// responseWriter captures the response for storing as the API result.
//
// it has no Unwrap method.  every header and body write must go through it,
// because a write to the underlying writer sends no run headers, is missing
// from the stored response, and lets a later async response or panic response
// write over a response that the client already has.  http.ResponseController
// finds the methods below on this type before it looks for Unwrap.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
	hijacked   bool
	// wroteHeader is true once the status line goes to the client.  after
	// that, the status code cannot change.
	wroteHeader bool

	// onHeader runs just before the status line and headers go to the client.
	// it is the last point at which a header can be added.  it can run again
	// when a flush fails, so it must be safe to run more than once.
	onHeader func(http.Header)
	// capture reports whether a write is copied into body.  nil copies every
	// write.  a write that it skips is missing from the stored response.
	capture func() bool
	// maxBody is the most bytes that body holds.  zero has no limit.  without a
	// limit, a long response such as a stream stays in memory until the handler
	// returns.
	maxBody int
	// truncated is true when body stopped at maxBody.
	truncated bool
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
	// a 1xx status other than 101 goes to the client at once, and the handler
	// still sends a final status after it.  without this check, the run headers
	// are decided at the 1xx status and the final status is not recorded.
	if code >= 100 && code <= 199 && code != http.StatusSwitchingProtocols {
		rw.ResponseWriter.WriteHeader(code)
		return
	}

	// net/http keeps the first final status and ignores later ones, so only the
	// first one is recorded.
	if !rw.wroteHeader {
		rw.beforeHeader()
		rw.statusCode = code
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(data []byte) (int, error) {
	rw.beforeHeader()
	// Don't capture response body after hijacking
	if !rw.hijacked && (rw.capture == nil || rw.capture()) {
		rw.copyBody(data)
	}
	return rw.ResponseWriter.Write(data)
}

// setMaxBody changes maxBody.  a copy already longer than the new limit is cut
// to it, so a lower limit also applies to bytes stored before the change.
func (rw *responseWriter) setMaxBody(limit int) {
	rw.maxBody = limit
	if limit > 0 && rw.body.Len() > limit {
		rw.body.Truncate(limit)
		rw.truncated = true
	}
}

// copyBody adds data to body until body holds maxBody bytes.
func (rw *responseWriter) copyBody(data []byte) {
	if rw.maxBody > 0 {
		if remaining := rw.maxBody - rw.body.Len(); len(data) > remaining {
			data = data[:max(remaining, 0)]
			rw.truncated = true
		}
	}
	rw.body.Write(data)
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
	_ = rw.FlushError()
}

// FlushError is Flush with an error.  http.ResponseController calls it.  the
// headers count as sent only when the underlying writer flushes.  without this
// check, a writer that cannot flush stops a later panic response and the run
// headers.
func (rw *responseWriter) FlushError() error {
	if rw.wroteHeader {
		return http.NewResponseController(rw.ResponseWriter).Flush()
	}

	if rw.onHeader != nil {
		rw.onHeader(rw.Header())
	}
	if err := http.NewResponseController(rw.ResponseWriter).Flush(); err != nil {
		return err
	}
	rw.wroteHeader = true
	return nil
}

// SetReadDeadline passes through to the underlying writer.
// http.ResponseController calls it.
func (rw *responseWriter) SetReadDeadline(deadline time.Time) error {
	return http.NewResponseController(rw.ResponseWriter).SetReadDeadline(deadline)
}

// SetWriteDeadline passes through to the underlying writer.
// http.ResponseController calls it.  a handler that runs many steps can use it
// to extend the server's WriteTimeout.
func (rw *responseWriter) SetWriteDeadline(deadline time.Time) error {
	return http.NewResponseController(rw.ResponseWriter).SetWriteDeadline(deadline)
}

// EnableFullDuplex passes through to the underlying writer.
// http.ResponseController calls it.
func (rw *responseWriter) EnableFullDuplex() error {
	return http.NewResponseController(rw.ResponseWriter).EnableFullDuplex()
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
	// max is the most bytes that buf holds.  zero has no limit.  without a
	// limit, a large upload stays in memory twice and goes to the Inngest API.
	max int
	// truncated is true when buf stopped at max.
	truncated bool
}

func newBodyRecorder(body io.ReadCloser, limit int) *bodyRecorder {
	if body == nil {
		body = http.NoBody
	}
	return &bodyRecorder{body: body, max: limit}
}

func (b *bodyRecorder) Read(p []byte) (int, error) {
	if b.closed {
		return 0, http.ErrBodyReadAfterClose
	}
	n, err := b.body.Read(p)
	b.copy(p[:n])
	return n, err
}

// setMax changes max.  a copy already longer than the new limit is cut to it,
// so a lower limit also applies to bytes stored before the change.
func (b *bodyRecorder) setMax(limit int) {
	b.max = limit
	if limit > 0 && b.buf.Len() > limit {
		b.buf.Truncate(limit)
		b.truncated = true
	}
}

// copy adds data to buf until buf holds max bytes.
func (b *bodyRecorder) copy(data []byte) {
	if b.max > 0 {
		if remaining := b.max - b.buf.Len(); len(data) > remaining {
			data = data[:max(remaining, 0)]
			b.truncated = true
		}
	}
	b.buf.Write(data)
}

// Close stops the handler from reading more of the body.  it leaves the
// underlying body open so that readAll can read the rest.  the HTTP server
// closes the underlying body when the handler returns.
func (b *bodyRecorder) Close() error {
	b.closed = true
	return nil
}

// readAll reads the part of the body that the handler did not read, up to max,
// and returns the stored body.  it reads one byte past max to find out whether
// the body is longer, and reads nothing more.  call it before the handler
// returns, because the HTTP server closes the underlying body after that.
func (b *bodyRecorder) readAll() ([]byte, error) {
	if b.max <= 0 {
		_, err := io.Copy(&b.buf, b.body)
		return b.buf.Bytes(), err
	}
	if b.truncated {
		return b.buf.Bytes(), nil
	}

	var rest bytes.Buffer
	_, err := io.CopyN(&rest, b.body, int64(b.max-b.buf.Len()+1))
	if errors.Is(err, io.EOF) {
		err = nil
	}
	b.copy(rest.Bytes())
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
