package tests

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/require"
)

func TestStepRetry(t *testing.T) {
	devEnv(t)

	t.Run("Step-level error with Retries: inngestgo.IntPtr(0) means no retry", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID atomic.Value
		var stepError error
		var stepExecutions atomic.Int32

		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)

				_, stepError = step.Run(ctx,
					"a",
					func(ctx context.Context) (any, error) {
						stepExecutions.Add(1)
						return nil, fmt.Errorf("oh no")
					},
				)

				return nil, stepError
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		waitForRun(t, &runID, enums.RunStatusFailed.String())

		r.Equal(int32(1), stepExecutions.Load(), "step should execute exactly once (no retries)")

		r.Error(stepError)
		r.Equal("oh no", stepError.Error())
	})

	t.Run("Step-level error with Retries: inngestgo.IntPtr(1) means one retry", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID atomic.Value
		var stepError error
		var stepExecutions atomic.Int32

		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(1),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)

				_, stepError = step.Run(ctx,
					"a",
					func(ctx context.Context) (any, error) {
						stepExecutions.Add(1)
						return nil, inngestgo.RetryAtError(
							fmt.Errorf("oh no"),
							time.Now().Add(100*time.Millisecond),
						)
					},
				)

				return nil, stepError
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		waitForRun(t, &runID, enums.RunStatusFailed.String())

		r.Equal(int32(2), stepExecutions.Load(), "step should execute twice (initial + 1 retry)")

		r.Error(stepError)
		r.Equal("oh no", stepError.Error())
	})

	t.Run("Step-level NoRetryError with Retries: inngestgo.IntPtr(1) means no retry", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID atomic.Value
		var stepError error
		var stepExecutions atomic.Int32

		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(1),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)

				_, stepError = step.Run(ctx,
					"a",
					func(ctx context.Context) (any, error) {
						stepExecutions.Add(1)
						return nil, inngestgo.NoRetryError(fmt.Errorf("permanent failure"))
					},
				)

				return nil, stepError
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		waitForRun(t, &runID, enums.RunStatusFailed.String())

		r.Equal(int32(1), stepExecutions.Load(), "step should execute exactly once (NoRetryError prevents retries)")

		r.Error(stepError)
		r.Equal("permanent failure", stepError.Error())
	})

	t.Run("Function-level error with Retries: inngestgo.IntPtr(0) means no retry", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID atomic.Value
		var functionExecutions atomic.Int32

		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)
				functionExecutions.Add(1)

				// Return a function-level error (not from a step)
				return nil, fmt.Errorf("function error")
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		waitForRun(t, &runID, enums.RunStatusFailed.String())

		r.Equal(int32(1), functionExecutions.Load(), "function should execute exactly once (no retries)")
	})

	t.Run("Function-level NoRetryError with Retries: inngestgo.IntPtr(1) means no retry", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID atomic.Value
		var functionExecutions atomic.Int32

		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(1),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)
				functionExecutions.Add(1)

				// Return a function-level NoRetryError (not from a step)
				return nil, inngestgo.NoRetryError(fmt.Errorf("permanent function error"))
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		waitForRun(t, &runID, enums.RunStatusFailed.String())

		r.Equal(int32(1), functionExecutions.Load(), "function should execute exactly once (NoRetryError prevents retries)")
	})

	t.Run("Step-level error that is not returned does not fail the run", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID atomic.Value
		var stepError error
		var stepExecutions atomic.Int32
		var functionExecutions atomic.Int32

		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)
				functionExecutions.Add(1)

				_, stepError = step.Run(ctx,
					"a",
					func(ctx context.Context) (any, error) {
						stepExecutions.Add(1)
						return nil, fmt.Errorf("step failed")
					},
				)

				// Swallow the error - don't return it. The run should succeed
				return "success", nil
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		waitForRun(t, &runID, enums.RunStatusCompleted.String())

		// The step executes once and fails, but the function continues and returns success
		// The function may execute multiple times (once for the step, once to complete)
		r.Equal(int32(1), stepExecutions.Load(), "step should execute exactly once")
		r.GreaterOrEqual(functionExecutions.Load(), int32(1), "function should execute at least once")

		// Step error should still be captured
		r.Error(stepError)
		r.Equal("step failed", stepError.Error())
	})
}
