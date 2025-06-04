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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStepRun(t *testing.T) {
	devEnv(t)

	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID atomic.Value
		var stepError error
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
		r.Error(stepError)
		r.Equal("oh no", stepError.Error())
	})

	t.Run("panic", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID atomic.Value
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
				return step.Run(ctx, "a", func(ctx context.Context) (any, error) {
					panic("oops")
				})
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

	t.Run("struct with any type", func(t *testing.T) {
		// Returning a struct with in an any-typed step.Run results in a
		// map[string]any.

		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		type person struct {
			Name string `json:"name"`
		}

		var stepOutput any
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				stepOutput, err = step.Run(ctx,
					"a",
					func(ctx context.Context) (any, error) {
						return person{Name: "Alice"}, nil
					},
				)
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

		r.EventuallyWithT(func(t *assert.CollectT) {
			a := assert.New(t)
			v, ok := stepOutput.(map[string]any)
			if !a.True(ok) {
				return
			}

			name, ok := v["name"].(string)
			if !a.True(ok) {
				return
			}
			a.Equal("Alice", name)
		}, time.Second*10, time.Millisecond*100)
	})

	t.Run("struct with specific type", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		type person struct {
			Name string `json:"name"`
		}

		var stepOutput person
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				stepOutput, err = step.Run(ctx,
					"a",
					func(ctx context.Context) (person, error) {
						return person{Name: "Alice"}, nil
					},
				)
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

		r.EventuallyWithT(func(t *assert.CollectT) {
			a := assert.New(t)
			a.Equal("Alice", stepOutput.Name)
		}, time.Second*10, time.Millisecond*100)
	})
}
