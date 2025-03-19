package middleware

import (
	"context"

	"github.com/inngest/inngestgo/internal/event"
	"github.com/inngest/inngestgo/internal/fn"
)

type Middleware interface {
	// AfterExecution is called after executing "new code".
	AfterExecution(ctx context.Context)

	// BeforeExecution is called before executing "new code".
	BeforeExecution(ctx context.Context)

	// TransformInput is called before entering the Inngest function. It gives
	// an opportunity to modify the input before it is sent to the function.
	TransformInput(
		input *TransformableInput,
		fn fn.ServableFunction,
	)
}

type BaseMiddleware struct{}

func (m *BaseMiddleware) AfterExecution(ctx context.Context) {
	// Noop.
}

func (m *BaseMiddleware) BeforeExecution(ctx context.Context) {
	// Noop.
}

func (m *BaseMiddleware) TransformInput(
	input *TransformableInput,
	fn fn.ServableFunction,
) {
	// Noop.
}

type TransformableInput struct {
	Event  *event.Event
	Events []*event.Event
	Steps  map[string]string

	context context.Context
}

// Context returns the context.
func (t *TransformableInput) Context() context.Context {
	return t.context
}

// WithContext sets the context.
func (t *TransformableInput) WithContext(ctx context.Context) {
	t.context = ctx
}
