package tests

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/group"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParallel(t *testing.T) {
	devEnv(t)

	t.Run("successful with a mix of step kinds", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		var invokedFnCounter int
		var step1ACounter int
		var step1BCounter int
		var stepAfterCounter int
		var results group.Results
		var requestCount int32

		appName := randomSuffix("TestParallel")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		fn1, err := inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:   "my-fn-1",
				Name: "my-fn-1",
			},
			inngestgo.EventTrigger("dummy", nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				invokedFnCounter++
				return "invoked output", nil
			},
		)
		r.NoError(err)

		var runID atomic.Value
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:   "my-fn-2",
				Name: "my-fn-2",
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)
				atomic.AddInt32(&requestCount, 1)

				results = group.Parallel(
					ctx,
					func(ctx context.Context) (any, error) {
						return step.Invoke[any](ctx, "invoke", step.InvokeOpts{
							FunctionId: fmt.Sprintf("%s-%s", appName, fn1.Config().ID),
						})
					},
					func(ctx context.Context) (any, error) {
						return step.Run(ctx, "1a", func(ctx context.Context) (int, error) {
							step1ACounter++
							return 1, nil
						})
					},
					func(ctx context.Context) (any, error) {
						return step.Run(ctx, "1b", func(ctx context.Context) (int, error) {
							step1BCounter++
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
				var err error
				for _, r := range results {
					if r.Error != nil {
						if r.Error == step.ErrEventNotReceived {
							continue
						}
						err = r.Error
					}
				}
				if err != nil {
					return nil, err
				}

				_, err = step.Run(ctx, "after", func(ctx context.Context) (any, error) {
					stepAfterCounter++
					return nil, nil
				})
				if err != nil {
					return nil, err
				}

				return nil, nil
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)
		waitForRun(t, &runID, enums.RunStatusCompleted.String())

		r.Equal(1, invokedFnCounter)
		r.Equal(1, step1ACounter)
		r.Equal(1, step1BCounter)
		r.Equal(1, stepAfterCounter)
		r.Equal(results, group.Results{
			{Value: "invoked output"},
			{Value: 1},
			{Value: 2},
			{Value: nil},
			{Error: step.ErrEventNotReceived},
		})
		r.Equal(6, int(requestCount))
	})

	t.Run("panic", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("TestParallel")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var runID atomic.Value
		var results group.Results
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "my-fn",
				Name:    "my-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)

				results = group.Parallel(
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
				return nil, results.AnyError()
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		run := waitForRun(t, &runID, enums.RunStatusFailed.String())
		output, ok := run.Output.(map[string]any)
		r.True(ok)

		r.Contains(output["message"], "function panicked: oops")
	})

	t.Run("sequential steps in group", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("app"),
		})
		r.NoError(err)

		var runID atomic.Value
		var logs SafeSlice[string]
		var step1ACounter int
		var step1BCounter int
		var step2Counter int
		var stepAfterCounter int
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID: "fn",
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)
				results := group.Parallel(
					ctx,
					func(ctx context.Context) (any, error) {
						_, err := step.Run(ctx, "1a", func(ctx context.Context) (any, error) {
							logs.Append("1a")
							step1ACounter++
							return nil, nil
						})
						if err != nil {
							return nil, err
						}

						return step.Run(ctx, "1b", func(ctx context.Context) (any, error) {
							logs.Append("1b")
							step1BCounter++
							return nil, nil
						})
					},
					func(ctx context.Context) (any, error) {
						return step.Run(ctx, "2", func(ctx context.Context) (any, error) {
							// Sleep to better demonstrate how step 1b runs after step 2.
							time.Sleep(time.Second)

							logs.Append("2")
							step2Counter++
							return nil, nil
						})
					},
				)
				err := results.AnyError()
				if err != nil {
					return nil, err
				}

				return step.Run(ctx, "after", func(ctx context.Context) (any, error) {
					logs.Append("after")
					stepAfterCounter++
					return nil, nil
				})
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)
		waitForRun(t, &runID, enums.RunStatusCompleted.String())

		r.Equal(1, step1ACounter)
		r.Equal(1, step1BCounter)
		r.Equal(1, step2Counter)
		r.Equal(1, stepAfterCounter)

		// Even though it looks like 1b would run before 2, it actually runs
		// after. This is because of the way parallelism works with step
		// discovery.
		r.Equal([]string{"1a", "2", "1b", "after"}, logs.Load())
	})

	t.Run("struct with specific type", func(t *testing.T) {
		t.Run("inline", func(t *testing.T) {
			// Struct return types are preserved when passing parallel steps
			// inline.

			ctx := context.Background()
			r := require.New(t)

			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID: randomSuffix("app"),
			})
			r.NoError(err)

			type person struct {
				Name string `json:"name"`
			}

			type animal struct {
				Name string `json:"name"`
			}

			var res TypedAtomic[group.Results]
			eventName := randomSuffix("my-event")
			_, err = inngestgo.CreateFunction(
				c,
				inngestgo.FunctionOpts{
					ID: "fn",
				},
				inngestgo.EventTrigger(eventName, nil),
				func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
					results := group.Parallel(
						ctx,
						func(ctx context.Context) (any, error) {
							return step.Run(ctx, "a", func(ctx context.Context) (person, error) {
								return person{Name: "Alice"}, nil
							})
						},
						func(ctx context.Context) (any, error) {
							return step.Run(ctx, "b", func(ctx context.Context) (animal, error) {
								return animal{Name: "Baxter"}, nil
							})
						},
					)
					res.Store(results)
					return nil, results.AnyError()
				},
			)
			r.NoError(err)

			server, sync := serve(t, c)
			defer server.Close()
			r.NoError(sync())

			_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
			r.NoError(err)

			r.EventuallyWithT(func(t *assert.CollectT) {
				a := assert.New(t)
				results, ok := res.Load()
				if !ok {
					return
				}
				if !a.Len(results, 2) {
					return
				}

				v1, ok := results[0].Value.(person)
				a.True(ok)
				a.Equal("Alice", v1.Name)

				v2, ok := results[1].Value.(animal)
				a.True(ok)
				a.Equal("Baxter", v2.Name)
			}, time.Second*10, time.Millisecond*100)
		})

		t.Run("slice", func(t *testing.T) {
			// Struct return types are preserved, even when building a slice
			// with an any return type.

			ctx := context.Background()
			r := require.New(t)

			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID: randomSuffix("app"),
			})
			r.NoError(err)

			type person struct {
				Name string `json:"name"`
			}

			type animal struct {
				Name string `json:"name"`
			}

			var res TypedAtomic[group.Results]
			eventName := randomSuffix("my-event")
			_, err = inngestgo.CreateFunction(
				c,
				inngestgo.FunctionOpts{
					ID: "fn",
				},
				inngestgo.EventTrigger(eventName, nil),
				func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
					var steps []func(ctx context.Context) (any, error)
					steps = append(steps, func(ctx context.Context) (any, error) {
						return step.Run(ctx, "a", func(ctx context.Context) (person, error) {
							return person{Name: "Alice"}, nil
						})
					})
					steps = append(steps, func(ctx context.Context) (any, error) {
						return step.Run(ctx, "b", func(ctx context.Context) (animal, error) {
							return animal{Name: "Baxter"}, nil
						})
					})

					results := group.Parallel(ctx, steps...)
					res.Store(results)
					return nil, results.AnyError()
				},
			)
			r.NoError(err)

			server, sync := serve(t, c)
			defer server.Close()
			r.NoError(sync())

			_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
			r.NoError(err)

			r.EventuallyWithT(func(t *assert.CollectT) {
				a := assert.New(t)
				results, ok := res.Load()
				if !ok {
					return
				}
				if !a.Len(results, 2) {
					return
				}

				v1, ok := results[0].Value.(person)
				a.True(ok)
				a.Equal("Alice", v1.Name)

				v2, ok := results[1].Value.(animal)
				a.True(ok)
				a.Equal("Baxter", v2.Name)
			}, time.Second*10, time.Millisecond*100)
		})
	})
}
