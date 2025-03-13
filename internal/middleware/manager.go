package middleware

import "context"

func NewMiddlewareManager() *MiddlewareManager {
	return &MiddlewareManager{
		items: []Middleware{},
	}
}

// MiddlewareManager is a thin wrapper around middleware, allowing our business
// logic to be oblivious of how many middlewares exist.
type MiddlewareManager struct {
	items []Middleware
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
