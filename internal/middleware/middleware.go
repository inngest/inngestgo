package middleware

import "context"

type Middleware struct {
	// AfterExecution is called after executing "new code".
	AfterExecution func(ctx context.Context)

	// BeforeExecution is called before executing "new code".
	BeforeExecution func(ctx context.Context)
}
