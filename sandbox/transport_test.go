package sandbox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type blockedBody struct {
	ctx context.Context
}

func (b blockedBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (blockedBody) Close() error { return nil }

func TestTransportDeadlineClassifiesUnconfirmedOperation(t *testing.T) {
	tests := []struct {
		name      string
		stage     string
		action    Action
		wantCode  ErrorCode
		retryable bool
		ambiguous bool
	}{
		{name: "request", stage: "request", action: ActionExec, wantCode: CodeOperationAmbiguous, ambiguous: true},
		{name: "fallback request", stage: "fallback", action: ActionProcessStart, wantCode: CodeOperationAmbiguous, ambiguous: true},
		{name: "success response", stage: "response", action: ActionExec, wantCode: CodeOperationAmbiguous, ambiguous: true},
		{name: "error response", stage: "error response", action: ActionExec, wantCode: CodeOperationAmbiguous, ambiguous: true},
		{name: "safe request", stage: "request", action: ActionGet, wantCode: CodeComputeUnavailable, retryable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if tt.stage == "fallback" && calls == 1 {
					return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
				}
				if tt.stage == "request" || tt.stage == "fallback" {
					<-req.Context().Done()
					return nil, req.Context().Err()
				}
				status := http.StatusOK
				if tt.stage == "error response" {
					status = http.StatusInternalServerError
				}
				return &http.Response{StatusCode: status, Body: blockedBody{ctx: req.Context()}, Request: req}, nil
			})}
			transport := &transport{baseURL: "http://sandbox.test", dev: true, http: client}
			if tt.stage == "fallback" {
				transport.key = "primary"
				transport.fallbackKey = "fallback"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, _, err := transport.json(ctx, request{
				action: tt.action, method: http.MethodPost, path: "/v2/sandboxes/test", body: map[string]string{"command": "test"}, statuses: []int{http.StatusOK},
			})
			var sandboxErr *Error
			require.ErrorAs(t, err, &sandboxErr)
			assert.Equal(t, tt.wantCode, sandboxErr.Code)
			assert.Equal(t, tt.retryable, sandboxErr.Retryable)
			assert.Equal(t, tt.ambiguous, sandboxErr.Ambiguous)
			assert.True(t, errors.Is(err, context.DeadlineExceeded))
		})
	}
}
