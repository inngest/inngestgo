package middleware

import (
	"context"

	"github.com/inngest/inngestgo/internal/fn"
	"github.com/inngest/inngestgo/internal/types"
)

func NewMiddlewareManager() *MiddlewareManager {
	return &MiddlewareManager{
		idempotentHooks: &types.Set[string]{},
		items:           []*Middleware{},
	}
}

// MiddlewareManager is a thin wrapper around middleware, allowing our business
// logic to be oblivious of how many middlewares exist.
type MiddlewareManager struct {
	// idempotentHooks used to ensure idempotent hooks are only called once per
	// request.
	idempotentHooks *types.Set[string]

	items []*Middleware
}

// Add adds middleware to the manager.
func (m *MiddlewareManager) Add(mw ...*Middleware) *MiddlewareManager {
	m.items = append(m.items, mw...)
	return m
}

func (m *MiddlewareManager) AfterExecution(ctx context.Context) {
	for i := range m.items {
		// We iterate in reverse order so that the innermost middleware is
		// executed first.
		mw := m.items[len(m.items)-1-i]

		if mw.AfterExecution != nil {
			mw.AfterExecution(ctx)
		}
	}
}

func (m *MiddlewareManager) BeforeExecution(ctx context.Context) {
	// Only allow BeforeExecution to be called once. This simplifies code since
	// execution can start at the function or step level.
	hook := "BeforeExecution"
	if m.idempotentHooks.Contains(hook) {
		return
	}
	m.idempotentHooks.Add(hook)

	for _, mw := range m.items {
		if mw.BeforeExecution != nil {
			mw.BeforeExecution(ctx)
		}
	}
}

func (m *MiddlewareManager) TransformInput(
	input *TransformableInput,
	fn fn.ServableFunction,
) {
	for _, mw := range m.items {
		if mw.TransformInput != nil {
			mw.TransformInput(input, fn)
		}
	}
}
