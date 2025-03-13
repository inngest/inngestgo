package middleware

import "context"

type Middleware struct {
	// AfterExecution is called after executing "new code". Called multiple
	// times per run when using steps.
	AfterExecution func(ctx context.Context)
}
