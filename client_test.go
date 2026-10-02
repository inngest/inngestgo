package inngestgo

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEventKey(t *testing.T) {
	t.Run("env var", func(t *testing.T) {
		c := apiClient{}
		t.Setenv("INNGEST_EVENT_KEY", "env-var")
		assert.Equal(t, "env-var", c.GetEventKey())
	})

	t.Run("field", func(t *testing.T) {
		c := apiClient{
			ClientOpts: ClientOpts{
				EventKey: StrPtr("field"),
			},
		}
		assert.Equal(t, "field", c.GetEventKey())
	})

	t.Run("field overrides env var", func(t *testing.T) {
		t.Setenv("INNGEST_EVENT_KEY", "env-var")
		c := apiClient{
			ClientOpts: ClientOpts{EventKey: StrPtr("field")},
		}
		assert.Equal(t, "field", c.GetEventKey())
	})

	t.Run("no event key in Cloud mode", func(t *testing.T) {
		// t.Setenv("INNGEST_EVENT_KEY", "")
		c := apiClient{}
		assert.Equal(t, "", c.GetEventKey())
	})

	t.Run("no event key in Dev mode", func(t *testing.T) {
		t.Setenv("INNGEST_DEV", "1")
		c := apiClient{}
		assert.Equal(t, "NO_EVENT_KEY_SET", c.GetEventKey())
	})
}

func TestNewClientUsesEnvConfiguredDefaultLogger(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_HANDLER", "json")

	c, err := NewClient(ClientOpts{AppID: "test"})
	require.NoError(t, err)

	opts := c.Options()
	assert.True(t, opts.Logger.Enabled(context.Background(), slog.LevelDebug))
	_, ok := opts.Logger.Handler().(*slog.JSONHandler)
	assert.True(t, ok)
}

func TestNewClientUsesTextLoggerHandler(t *testing.T) {
	t.Setenv("LOG_HANDLER", "txt")

	c, err := NewClient(ClientOpts{AppID: "test"})
	require.NoError(t, err)

	opts := c.Options()
	_, ok := opts.Logger.Handler().(*slog.TextHandler)
	assert.True(t, ok)
}

func TestNewClientKeepsProvidedLogger(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_HANDLER", "json")

	provided := slog.New(slog.DiscardHandler)
	c, err := NewClient(ClientOpts{
		AppID:  "test",
		Logger: provided,
	})
	require.NoError(t, err)

	assert.Same(t, provided, c.Options().Logger)
}

// trackingTransport records every response body that it returns and whether
// the caller closed it.
type trackingTransport struct {
	base   http.RoundTripper
	opened atomic.Int32
	closed atomic.Int32
}

type trackedBody struct {
	io.ReadCloser
	closed *atomic.Int32
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type canceledReadBody struct {
	ctx         context.Context
	readStarted chan struct{}
}

func (b canceledReadBody) Read([]byte) (int, error) {
	b.readStarted <- struct{}{}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (canceledReadBody) Close() error {
	return nil
}

func (b trackedBody) Close() error {
	b.closed.Add(1)
	return b.ReadCloser.Close()
}

func (t *trackingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return resp, err
	}
	t.opened.Add(1)
	resp.Body = trackedBody{ReadCloser: resp.Body, closed: &t.closed}
	return resp, nil
}

func newSendTestClient(t *testing.T, url string, hc *http.Client) Client {
	t.Helper()
	c, err := NewClient(ClientOpts{
		AppID:           "test",
		EventKey:        StrPtr("key"),
		EventAPIBaseURL: StrPtr(url),
		HTTPClient:      hc,
		Logger:          slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	return c
}

func TestSendStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newSendTestClient(t, srv.URL, srv.Client())

	_, err := c.Send(ctx, Event{Name: "test/event", Data: map[string]any{}})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "got %v", err)
	assert.EqualValues(t, 1, hits.Load())
}

func TestSendReturnsContextErrorWhenResponseReadIsCanceled(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			readStarted := make(chan struct{}, 1)
			hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: status,
					Body: canceledReadBody{
						ctx:         r.Context(),
						readStarted: readStarted,
					},
				}, nil
			})}
			c := newSendTestClient(t, "http://example.test", hc)

			done := make(chan error, 1)
			go func() {
				_, err := c.Send(ctx, Event{Name: "test/event", Data: map[string]any{}})
				done <- err
			}()

			select {
			case <-readStarted:
				cancel()
			case <-time.After(3 * time.Second):
				t.Fatal("Send did not read the response body")
			}

			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(3 * time.Second):
				t.Fatal("Send did not return after cancellation")
			}
		})
	}
}

func TestSendAbortsInFlightRequestWhenContextIsDone(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	c := newSendTestClient(t, srv.URL, srv.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.Send(ctx, Event{Name: "test/event", Data: map[string]any{}})
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("Send did not return after the context deadline")
	}
}

func TestSendClosesResponseBodiesBetweenRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer srv.Close()

	tt := &trackingTransport{base: http.DefaultTransport}
	c := newSendTestClient(t, srv.URL, &http.Client{Transport: tt})

	_, err := c.Send(context.Background(), Event{Name: "test/event", Data: map[string]any{}})
	require.Error(t, err)
	assert.EqualValues(t, retryAttempts, tt.opened.Load())
	assert.Equal(t, tt.opened.Load(), tt.closed.Load(), "every response body must be closed")
}
