package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/experimental"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientMiddleware(t *testing.T) {
	t.Run("2 steps", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		logs := []string{}
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("app"),
			Middleware: []experimental.Middleware{
				{
					AfterExecution: func(ctx context.Context) {
						logs = append(logs, "mw: AfterExecution")
					},
				},
			},
		})
		r.NoError(err)

		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				logs = append(logs, "fn: top")

				_, err := step.Run(ctx, "a", func(ctx context.Context) (any, error) {
					logs = append(logs, "a: running")
					return nil, nil
				})
				if err != nil {
					return nil, err
				}

				logs = append(logs, "fn: between steps")

				_, err = step.Run(ctx, "b", func(ctx context.Context) (any, error) {
					logs = append(logs, "b: running")
					return nil, nil
				})

				logs = append(logs, "fn: bottom")
				return nil, err
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)
			a.Equal([]string{
				// First request.
				"fn: top",
				"a: running",
				"mw: AfterExecution",

				// Second request.
				"fn: top",
				"fn: between steps",
				"b: running",
				"mw: AfterExecution",

				// Third request.
				"fn: top",
				"fn: between steps",
				"fn: bottom",
				"mw: AfterExecution",
			}, logs)
		}, 5*time.Second, 10*time.Millisecond)
	})
	t.Run("retry", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		logs := []string{}
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("app"),
			Middleware: []experimental.Middleware{
				{
					AfterExecution: func(ctx context.Context) {
						logs = append(logs, "mw: AfterExecution")
					},
				},
			},
		})
		r.NoError(err)

		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(1),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				logs = append(logs, "fn: top")
				if input.InputCtx.Attempt == 0 {
					return nil, inngestgo.RetryAtError(
						errors.New("oh no"),
						time.Now().Add(time.Second),
					)
				}
				logs = append(logs, "fn: bottom")
				return nil, nil
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)
			a.Equal([]string{
				// First request.
				"fn: top",
				"mw: AfterExecution",

				// Second request.
				"fn: top",
				"fn: bottom",
				"mw: AfterExecution",
			}, logs)
		}, 5*time.Second, 10*time.Millisecond)
	})
}
