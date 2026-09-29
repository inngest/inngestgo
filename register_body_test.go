package inngestgo

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// bodyTrackingTransport counts the response bodies it hands out and the ones
// the caller closes.
type bodyTrackingTransport struct {
	base   http.RoundTripper
	opened atomic.Int32
	closed atomic.Int32
}

type closeCountingBody struct {
	io.ReadCloser
	closed *atomic.Int32
}

func (b closeCountingBody) Close() error {
	b.closed.Add(1)
	return b.ReadCloser.Close()
}

func (t *bodyTrackingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return resp, err
	}
	t.opened.Add(1)
	resp.Body = closeCountingBody{ReadCloser: resp.Body, closed: &t.closed}
	return resp, nil
}

func TestOutOfBandSyncClosesRegisterResponseBody(t *testing.T) {
	cases := []struct {
		name     string
		statuses []int
		wantCode int
	}{
		{name: "success", statuses: []int{http.StatusOK}, wantCode: http.StatusOK},
		{
			name:     "register error",
			statuses: []int{http.StatusInternalServerError},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "signing key fallback",
			statuses: []int{http.StatusUnauthorized, http.StatusOK},
			wantCode: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			var calls atomic.Int32
			mockCloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				i := int(calls.Add(1)) - 1
				w.WriteHeader(tc.statuses[i])
				_, _ = w.Write([]byte(`{"error":"nope"}`))
			}))
			defer mockCloud.Close()

			client, err := NewClient(ClientOpts{
				AppID:              "register-body",
				Env:                toPtr("my-env"),
				RegisterURL:        &mockCloud.URL,
				SigningKey:         toPtr(string(testKey)),
				SigningKeyFallback: toPtr(string(testKeyFallback)),
			})
			r.NoError(err)
			_, err = CreateFunction(
				client,
				FunctionOpts{ID: "my-fn"},
				EventTrigger("my-event", nil),
				func(ctx context.Context, input Input[any]) (any, error) {
					return nil, nil
				},
			)
			r.NoError(err)
			server := httptest.NewServer(client.ServeWithOpts(ServeOpts{
				EnableUnauthedSync: toPtr(true),
			}))
			defer server.Close()

			// The handler registers through http.DefaultClient, which uses
			// http.DefaultTransport.
			tracker := &bodyTrackingTransport{base: http.DefaultTransport}
			original := http.DefaultTransport
			http.DefaultTransport = tracker
			defer func() { http.DefaultTransport = original }()

			req, err := http.NewRequest(http.MethodPut, server.URL, nil)
			r.NoError(err)
			// Use a client that bypasses the tracker for the request under test.
			resp, err := (&http.Client{Transport: original}).Do(req)
			r.NoError(err)
			defer resp.Body.Close()
			_, _ = io.ReadAll(resp.Body)

			r.Equal(tc.wantCode, resp.StatusCode)
			r.EqualValues(len(tc.statuses), calls.Load())
			r.EqualValues(len(tc.statuses), tracker.opened.Load())
			r.EqualValues(tracker.opened.Load(), tracker.closed.Load(), "register response bodies must be closed")
		})
	}
}
