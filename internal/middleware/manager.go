package middleware

import (
	"context"

	"github.com/inngest/inngestgo/internal/types"
)

func NewMiddlewareManager() *MiddlewareManager {
	return &MiddlewareManager{
		idempotentHooks: &types.Set[string]{},
		items:           []Middleware{},
	}
}

// MiddlewareManager is a thin wrapper around middleware, allowing our business
// logic to be oblivious of how many middlewares exist.
type MiddlewareManager struct {
	idempotentHooks *types.Set[string]
	items           []Middleware
}

// Add adds middleware to the manager.
func (m *MiddlewareManager) Add(mw ...Middleware) *MiddlewareManager {
	m.items = append(m.items, mw...)
	return m
}

func (m *MiddlewareManager) AfterExecution(ctx context.Context) {
	for _, mw := range m.items {
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
