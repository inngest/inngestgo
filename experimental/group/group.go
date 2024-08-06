package group

import (
	"context"
	"fmt"

	"github.com/inngest/inngestgo/step"
)

type Result struct {
	Error error
	Value any
}

func Parallel(
	ctx context.Context,
	fns ...func(ctx context.Context,
	) (any, error)) []Result {
	ctx = context.WithValue(ctx, step.ParallelKey, true)

	results := []Result{}
	isPlanned := false
	ch := make(chan struct{}, 1)
	for _, fn := range fns {
		fn := fn
		go func(fn func(ctx context.Context) (any, error)) {
			defer func() {
				if r := recover(); r != nil {
					if _, ok := r.(step.ControlHijack); ok {
						isPlanned = true
					} else {
						// TODO: What to do here?
						fmt.Println("TODO")
					}
				}
				ch <- struct{}{}
			}()

			value, err := fn(ctx)
			results = append(results, Result{Error: err, Value: value})
		}(fn)
		<-ch
	}

	if isPlanned {
		panic(step.ControlHijack{})
	}

	return results
}
