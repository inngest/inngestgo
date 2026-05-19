package middleware_test

import (
	"context"
	"testing"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/middleware"
	"github.com/inngest/inngestgo/stephttp"
)

type contextKey struct{}

type customMiddleware struct {
	middleware.BaseMiddleware
}

func (m *customMiddleware) TransformInput(
	ctx context.Context,
	call middleware.CallContext,
	input *middleware.TransformableInput,
) {
	input.WithContext(context.WithValue(input.Context(), contextKey{}, "value"))
}

func TestPublicMiddlewareConfiguresClientOpts(t *testing.T) {
	var _ middleware.Middleware = (*customMiddleware)(nil)

	newMiddleware := func() middleware.Middleware {
		return &customMiddleware{}
	}

	if _, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID:      "test",
		Middleware: []func() middleware.Middleware{newMiddleware},
	}); err != nil {
		t.Fatalf("expected public middleware to configure client: %v", err)
	}
}

func TestPublicMiddlewareConfiguresStepHTTP(t *testing.T) {
	newMiddleware := func() middleware.Middleware {
		return &customMiddleware{}
	}

	stephttp.Setup(stephttp.SetupOpts{
		Domain: "example.com",
		Optional: stephttp.OptionalSetupOpts{
			Middleware: []func() middleware.Middleware{newMiddleware},
		},
	})
}
