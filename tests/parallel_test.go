package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/experimental/group"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/require"
)

func TestParallel(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	t.Run("successful with a mix of step kinds", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		state := struct {
			invokedFnCounter int
			step1ACounter    int
			step1BCounter    int
			stepAfterCounter int
			parallelResults  []group.Result
		}{}

		appName := randomSuffix("TestParallel")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)
		h := inngestgo.NewHandler(c, inngestgo.HandlerOpts{})

		fn1, err := inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:   "my-fn-1",
				Name: "my-fn-1",
			},
			inngestgo.EventTrigger("dummy", nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				state.invokedFnCounter++
				return "invoked output", nil
			},
		)
		r.NoError(err)

		eventName := randomSuffix("my-event")
		fn2, err := inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:   "my-fn-2",
				Name: "my-fn-2",
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				state.parallelResults = group.Parallel(
					ctx,
					func(ctx context.Context) (any, error) {
						return step.Invoke[any](ctx, "invoke", step.InvokeOpts{
							FunctionId: fmt.Sprintf("%s-%s", appName, fn1.Config().ID),
						})
					},
					func(ctx context.Context) (any, error) {
						return step.Run(ctx, "1a", func(ctx context.Context) (int, error) {
							state.step1ACounter++
							return 1, nil
						})
					},
					func(ctx context.Context) (any, error) {
						return step.Run(ctx, "1b", func(ctx context.Context) (int, error) {
							state.step1BCounter++
							return 2, nil
						})
					},
					func(ctx context.Context) (any, error) {
						step.Sleep(ctx, "sleep", time.Second)
						return nil, nil
					},
					func(ctx context.Context) (any, error) {
						return step.WaitForEvent[any](ctx, "wait", step.WaitForEventOpts{
							Event:   "never",
							Timeout: time.Second,
						})
					},
				)

				_, err := step.Run(ctx, "after", func(ctx context.Context) (any, error) {
					state.stepAfterCounter++
					return nil, nil
				})
				if err != nil {
					return nil, err
				}

				return nil, nil
			},
		)
		r.NoError(err)
		h.Register(fn1, fn2)

		server, sync := serve(t, h)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{
			Name: eventName,
			Data: map[string]any{"foo": "bar"}},
		)
		r.NoError(err)

		r.Eventually(func() bool {
			return state.stepAfterCounter == 1
		}, 5*time.Second, 10*time.Millisecond)
		r.Equal(1, state.invokedFnCounter)
		r.Equal(1, state.step1ACounter)
		r.Equal(1, state.step1BCounter)
		r.Equal(state.parallelResults, []group.Result{
			{Value: "invoked output"},
			{Value: 1},
			{Value: 2},
			{Value: nil},
			{Error: step.ErrEventNotReceived},
		})
	})

	t.Run("panic", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("TestParallel")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)
		h := inngestgo.NewHandler(c, inngestgo.HandlerOpts{})

		var runID string
		eventName := randomSuffix("my-event")
		fn, err := inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "my-fn",
				Name:    "my-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID

				group.Parallel(
					ctx,
					func(ctx context.Context) (any, error) {
						return step.Run(ctx, "1a", func(ctx context.Context) (int, error) {
							return 1, nil
						})
					},
					func(ctx context.Context) (any, error) {
						return step.Run(ctx, "1b", func(ctx context.Context) (int, error) {
							panic("oops")
						})
					},
				)

				return nil, nil
			},
		)
		r.NoError(err)

		h.Register(fn)

		server, sync := serve(t, h)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{
			Name: eventName,
			Data: map[string]any{"foo": "bar"}},
		)
		r.NoError(err)

		run, err := waitForRun(&runID, StatusFailed)
		r.NoError(err)

		output, ok := run.Output.(map[string]any)
		r.True(ok)

		r.Contains(output["message"], "function panicked: oops")
	})
}
