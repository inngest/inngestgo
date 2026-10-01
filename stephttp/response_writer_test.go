package stephttp

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// plainWriter implements only http.ResponseWriter.  it cannot flush, hijack,
// or set deadlines.
type plainWriter struct {
	header http.Header
	codes  []int
	body   bytes.Buffer
}

func newPlainWriter() *plainWriter {
	return &plainWriter{header: http.Header{}}
}

func (p *plainWriter) Header() http.Header { return p.header }

func (p *plainWriter) Write(b []byte) (int, error) { return p.body.Write(b) }

func (p *plainWriter) WriteHeader(code int) { p.codes = append(p.codes, code) }

// controlledWriter records each http.ResponseController call that reaches it.
type controlledWriter struct {
	*plainWriter
	flushed       bool
	readDeadline  time.Time
	writeDeadline time.Time
	fullDuplex    bool
}

func (c *controlledWriter) Flush() { c.flushed = true }

func (c *controlledWriter) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	return nil
}

func (c *controlledWriter) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return nil
}

func (c *controlledWriter) EnableFullDuplex() error {
	c.fullDuplex = true
	return nil
}

func TestResponseWriterStatus(t *testing.T) {
	tests := []struct {
		name string
		// write runs the handler's calls on the wrapped writer.
		write          func(w http.ResponseWriter)
		expectedStatus int
		// expectedOnHeader is how many times onHeader runs.
		expectedOnHeader int
		// expectedSentBefore is how many statuses the client already has when
		// onHeader runs.  a 1xx status goes out before the final headers.
		expectedSentBefore int
	}{
		{
			name: "1xx then a final status",
			write: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusEarlyHints)
				w.WriteHeader(http.StatusCreated)
			},
			expectedStatus:     http.StatusCreated,
			expectedOnHeader:   1,
			expectedSentBefore: 1,
		},
		{
			name: "1xx then a write",
			write: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusEarlyHints)
				_, _ = w.Write([]byte("ok"))
			},
			expectedStatus:     http.StatusOK,
			expectedOnHeader:   1,
			expectedSentBefore: 1,
		},
		{
			name: "only a 1xx",
			write: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusEarlyHints)
			},
			expectedStatus:   http.StatusOK,
			expectedOnHeader: 0,
		},
		{
			name: "101 is final",
			write: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusSwitchingProtocols)
			},
			expectedStatus:   http.StatusSwitchingProtocols,
			expectedOnHeader: 1,
		},
		{
			name: "write then a status",
			write: func(w http.ResponseWriter) {
				_, _ = w.Write([]byte("ok"))
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedStatus:   http.StatusOK,
			expectedOnHeader: 1,
		},
		{
			name: "two statuses",
			write: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusNotFound)
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectedStatus:   http.StatusNotFound,
			expectedOnHeader: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			underlying := newPlainWriter()
			rw := newResponseWriter(underlying)
			calls, sentBefore := 0, 0
			rw.onHeader = func(http.Header) {
				calls++
				sentBefore = len(underlying.codes)
			}

			tt.write(rw)

			require.Equal(t, tt.expectedStatus, rw.statusCode)
			require.Equal(t, tt.expectedOnHeader, calls)
			require.Equal(t, tt.expectedSentBefore, sentBefore)
			require.Equal(t, tt.expectedOnHeader > 0, rw.wroteHeader)
		})
	}
}

func TestResponseWriterFlush(t *testing.T) {
	tests := []struct {
		name            string
		underlying      http.ResponseWriter
		expectErr       bool
		expectedHeaders bool
	}{
		{
			name:            "underlying writer flushes",
			underlying:      &controlledWriter{plainWriter: newPlainWriter()},
			expectedHeaders: true,
		},
		{
			name:       "underlying writer cannot flush",
			underlying: newPlainWriter(),
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw := newResponseWriter(tt.underlying)

			err := http.NewResponseController(rw).Flush()
			if tt.expectErr {
				require.ErrorIs(t, err, http.ErrNotSupported)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.expectedHeaders, rw.wroteHeader)

			// a status after a failed flush is still the final status.
			rw.WriteHeader(http.StatusNotFound)
			if tt.expectedHeaders {
				require.Equal(t, http.StatusOK, rw.statusCode)
			} else {
				require.Equal(t, http.StatusNotFound, rw.statusCode)
			}
		})
	}
}

func TestResponseControllerUsesWrapper(t *testing.T) {
	rw := newResponseWriter(&controlledWriter{plainWriter: newPlainWriter()})
	underlying := rw.ResponseWriter.(*controlledWriter)

	// the wrapper must not expose the underlying writer.
	_, ok := any(rw).(interface{ Unwrap() http.ResponseWriter })
	require.False(t, ok)

	calls := 0
	rw.onHeader = func(http.Header) { calls++ }

	rc := http.NewResponseController(rw)
	deadline := time.Now().Add(time.Minute)

	require.NoError(t, rc.Flush())
	require.True(t, underlying.flushed)
	require.Equal(t, 1, calls)

	require.NoError(t, rc.SetReadDeadline(deadline))
	require.Equal(t, deadline, underlying.readDeadline)

	require.NoError(t, rc.SetWriteDeadline(deadline))
	require.Equal(t, deadline, underlying.writeDeadline)

	require.NoError(t, rc.EnableFullDuplex())
	require.True(t, underlying.fullDuplex)

	// a writer without these methods reports that they are not supported.
	plain := http.NewResponseController(newResponseWriter(newPlainWriter()))
	require.True(t, errors.Is(plain.SetWriteDeadline(deadline), http.ErrNotSupported))
	require.True(t, errors.Is(plain.EnableFullDuplex(), http.ErrNotSupported))

	// a wrapped route on a real server can set its deadlines.
	p := Setup(SetupOpts{})
	var deadlineErr error
	server := httptest.NewServer(p.HandleFunc(FnOpts{}, func(w http.ResponseWriter, r *http.Request) {
		deadlineErr = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.NoError(t, deadlineErr)
}
