package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/experimental"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientMiddleware(t *testing.T) {
	devEnv(t)

	t.Run("no hooks", func(t *testing.T) {
		// Nothing errors when 0 hooks are provided.

		r := require.New(t)
		ctx := context.Background()

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("app"),
			Middleware: []*experimental.Middleware{
				{},
			},
		})
		r.NoError(err)

		var runID string
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
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
			run, err := getRun(runID)
			if !a.NoError(err) {
				return
			}
			a.Equal(enums.RunStatusCompleted.String(), run.Status)
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("2 steps", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		logs := []string{}
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("app"),
			Middleware: []*experimental.Middleware{
				{
					AfterExecution: func(ctx context.Context) {
						logs = append(logs, "mw: AfterExecution")
					},
					BeforeExecution: func(ctx context.Context) {
						logs = append(logs, "mw: BeforeExecution")
					},
					TransformInput: func(
						input *middleware.TransformableInput,
						fn inngestgo.ServableFunction,
					) {
						logs = append(logs, "mw: TransformInput")
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
				"mw: TransformInput",
				"mw: BeforeExecution",
				"fn: top",
				"a: running",
				"mw: AfterExecution",

				// Second request.
				"mw: TransformInput",
				"fn: top",
				"mw: BeforeExecution",
				"fn: between steps",
				"b: running",
				"mw: AfterExecution",

				// Third request.
				"mw: TransformInput",
				"fn: top",
				"fn: between steps",
				"mw: BeforeExecution",
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
			Middleware: []*experimental.Middleware{
				{
					AfterExecution: func(ctx context.Context) {
						logs = append(logs, "mw: AfterExecution")
					},
					BeforeExecution: func(ctx context.Context) {
						logs = append(logs, "mw: BeforeExecution")
					},
					TransformInput: func(
						input *middleware.TransformableInput,
						fn inngestgo.ServableFunction,
					) {
						logs = append(logs, "mw: TransformInput")
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
				"mw: TransformInput",
				"mw: BeforeExecution",
				"fn: top",
				"mw: AfterExecution",

				// Second request.
				"mw: TransformInput",
				"mw: BeforeExecution",
				"fn: top",
				"fn: bottom",
				"mw: AfterExecution",
			}, logs)
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("connect", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("app"),
			Middleware: []*experimental.Middleware{
				{
					AfterExecution:  func(ctx context.Context) {},
					BeforeExecution: func(ctx context.Context) {},
				},
			},
		})
		r.NoError(err)

		var runID string
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				return nil, nil
			},
		)
		r.NoError(err)

		conn, err := inngestgo.Connect(ctx, inngestgo.ConnectOpts{
			InstanceID: inngestgo.Ptr(randomSuffix("instance")),
			Apps:       []inngestgo.Client{c},
		})
		r.NoError(err)
		defer conn.Close()

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)
			run, err := getRun(runID)
			if !a.NoError(err) {
				return
			}
			a.Equal(enums.RunStatusCompleted.String(), run.Status)
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("multiple", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		logs := []string{}
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("app"),
			Middleware: []*experimental.Middleware{
				{
					AfterExecution: func(ctx context.Context) {
						logs = append(logs, "1: AfterExecution")
					},
					BeforeExecution: func(ctx context.Context) {
						logs = append(logs, "1: BeforeExecution")
					},
					TransformInput: func(
						input *middleware.TransformableInput,
						fn inngestgo.ServableFunction,
					) {
						logs = append(logs, "1: TransformInput")
					},
				},
				{
					AfterExecution: func(ctx context.Context) {
						logs = append(logs, "2: AfterExecution")
					},
					BeforeExecution: func(ctx context.Context) {
						logs = append(logs, "2: BeforeExecution")
					},
					TransformInput: func(
						input *middleware.TransformableInput,
						fn inngestgo.ServableFunction,
					) {
						logs = append(logs, "2: TransformInput")
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
				"1: TransformInput",
				"2: TransformInput",
				"1: BeforeExecution",
				"2: BeforeExecution",
				"2: AfterExecution",
				"1: AfterExecution",
			}, logs)
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("transform input", func(t *testing.T) {
		t.Run("explicit event data type", func(t *testing.T) {
			// TransformInput works when the event data type is explicitly set.

			r := require.New(t)
			ctx := context.Background()

			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID: randomSuffix("app"),
				Middleware: []*experimental.Middleware{
					{
						TransformInput: func(
							input *middleware.TransformableInput,
							fn inngestgo.ServableFunction,
						) {
							input.Event.Data["transformed"] = true

							for _, evt := range input.Events {
								evt.Data["transformed"] = true
							}
						},
					},
				},
			})
			r.NoError(err)

			var event *inngestgo.GenericEvent[map[string]any, any]
			var events []inngestgo.GenericEvent[map[string]any, any]
			eventName := randomSuffix("event")
			_, err = inngestgo.CreateFunction(
				c,
				inngestgo.FunctionOpts{
					ID:      "fn",
					Retries: inngestgo.IntPtr(0),
				},
				inngestgo.EventTrigger(eventName, nil),
				func(
					ctx context.Context,
					input inngestgo.Input[inngestgo.GenericEvent[map[string]any, any]],
				) (any, error) {
					event = &input.Event
					events = input.Events
					return nil, nil
				},
			)
			r.NoError(err)

			server, sync := serve(t, c)
			defer server.Close()
			r.NoError(sync())

			_, err = c.Send(ctx, inngestgo.Event{
				Data: map[string]any{"msg": "hi"},
				Name: eventName,
			})
			r.NoError(err)

			r.EventuallyWithT(func(ct *assert.CollectT) {
				a := assert.New(ct)
				a.NotNil(event)
				a.NotNil(events)
			}, 5*time.Second, 10*time.Millisecond)

			// Assert event.
			r.Equal(eventName, event.Name)
			r.Equal(map[string]any{
				"msg":         "hi",
				"transformed": true,
			}, event.Data)

			// Assert events.
			r.Equal(1, len(events))
			r.Equal(eventName, events[0].Name)
			r.Equal(map[string]any{
				"msg":         "hi",
				"transformed": true,
			}, events[0].Data)
		})

		t.Run("no event type", func(t *testing.T) {
			// TransformInput works when the event data type is any.

			r := require.New(t)
			ctx := context.Background()

			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID: randomSuffix("app"),
				Middleware: []*experimental.Middleware{
					{
						TransformInput: func(
							input *middleware.TransformableInput,
							fn inngestgo.ServableFunction,
						) {
							input.Event.Data["transformed"] = true

							for _, evt := range input.Events {
								evt.Data["transformed"] = true
							}
						},
					},
				},
			})
			r.NoError(err)

			var event any
			var events []any
			eventName := randomSuffix("event")
			_, err = inngestgo.CreateFunction(
				c,
				inngestgo.FunctionOpts{
					ID:      "fn",
					Retries: inngestgo.IntPtr(0),
				},
				inngestgo.EventTrigger(eventName, nil),
				func(
					ctx context.Context,
					input inngestgo.Input[any],
				) (any, error) {
					event = input.Event
					events = input.Events
					return nil, nil
				},
			)
			r.NoError(err)

			server, sync := serve(t, c)
			defer server.Close()
			r.NoError(sync())

			_, err = c.Send(ctx, inngestgo.Event{
				Data: map[string]any{"msg": "hi"},
				Name: eventName,
			})
			r.NoError(err)

			r.EventuallyWithT(func(ct *assert.CollectT) {
				a := assert.New(ct)
				a.NotNil(event)
			}, 5*time.Second, 10*time.Millisecond)

			// Assert event.
			v, ok := event.(map[string]any)
			r.True(ok)
			r.Equal(eventName, v["name"])
			r.Equal(map[string]any{
				"msg":         "hi",
				"transformed": true,
			}, v["data"])

			// Assert events.
			r.Len(events, 1)
			v, ok = events[0].(map[string]any)
			r.True(ok)
			r.Equal(eventName, v["name"])
			r.Equal(map[string]any{
				"msg":         "hi",
				"transformed": true,
			}, v["data"])
		})

		t.Run("context", func(t *testing.T) {
			// TransformInput can modify the context passed to the Inngest
			// function.

			r := require.New(t)
			ctx := context.Background()
			type contextKeyType struct{}
			contextKey := contextKeyType{}

			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID: randomSuffix("app"),
				Middleware: []*experimental.Middleware{
					{
						TransformInput: func(
							input *middleware.TransformableInput,
							fn inngestgo.ServableFunction,
						) {
							input.WithContext(context.WithValue(
								input.Context(), contextKey, "hello",
							))
						},
					},
				},
			})
			r.NoError(err)

			var value any
			eventName := randomSuffix("event")
			_, err = inngestgo.CreateFunction(
				c,
				inngestgo.FunctionOpts{
					ID:      "fn",
					Retries: inngestgo.IntPtr(0),
				},
				inngestgo.EventTrigger(eventName, nil),
				func(
					ctx context.Context,
					input inngestgo.Input[any],
				) (any, error) {
					value = ctx.Value(contextKey)
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
				a.Equal("hello", value)
			}, 5*time.Second, 10*time.Millisecond)
		})
	})
}
