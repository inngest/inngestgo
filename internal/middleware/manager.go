package middleware

import (
	"context"

	"github.com/inngest/inngestgo/internal/fn"
	"github.com/inngest/inngestgo/internal/types"
)

// ensure that the MiddlewareManager implements Middleware at compile time.
var _ Middleware = &MiddlewareManager{}

// NewMiddlewareManager returns a new middleware manager which invokes
// each registered middleware.
func NewMiddlewareManager() *MiddlewareManager {
	return &MiddlewareManager{
		idempotentHooks: &types.Set[string]{},
		items:           []Middleware{},
	}
}

// MiddlewareManager is a thin wrapper around middleware, allowing our business
// logic to be oblivious of how many middlewares exist.
type MiddlewareManager struct {
	// idempotentHooks used to ensure idempotent hooks are only called once per
	// request.
	idempotentHooks *types.Set[string]

	items []Middleware
}

// Add adds middleware to the manager.
func (m *MiddlewareManager) Add(mw ...func() Middleware) *MiddlewareManager {
	for _, mw := range mw {
		m.items = append(m.items, mw())
	}
	return m
}

func (m *MiddlewareManager) BeforeExecution(ctx context.Context, call CallContext) {
	// Only allow BeforeExecution to be called once. This simplifies code since
	// execution can start at the function or step level.
	hook := "BeforeExecution"
	if m.idempotentHooks.Contains(hook) {
		return
	}
	m.idempotentHooks.Add(hook)

	for _, mw := range m.items {
		mw.BeforeExecution(ctx, call)
	}
}

func (m *MiddlewareManager) AfterExecution(ctx context.Context, call CallContext, result any, err error) {
	for i := range m.items {
		// We iterate in reverse order so that the innermost middleware is
		// executed first.
		mw := m.items[len(m.items)-1-i]

		mw.AfterExecution(ctx, call, result, err)
	}
}

func (m *MiddlewareManager) TransformInput(
	input *TransformableInput,
	fn fn.ServableFunction,
) {
	for _, mw := range m.items {
		mw.TransformInput(input, fn)
	}
}

func (m *MiddlewareManager) OnPanic(ctx context.Context, call CallContext, recovered any, stack string) {
	for i := range m.items {
		// We iterate in reverse order so that the innermost middleware is
		// executed first.
		mw := m.items[len(m.items)-1-i]

		mw.OnPanic(ctx, call, recovered, stack)
	}
}
