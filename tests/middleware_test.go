package tests

import (
	"context"
	"errors"
	"log/slog"
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

		newMW := func() experimental.Middleware {
			return &experimental.BaseMiddleware{}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW},
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
		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context) {
					logs = append(logs, "mw: AfterExecution")
				},
				beforeExecutionFn: func(ctx context.Context) {
					logs = append(logs, "mw: BeforeExecution")
				},
				transformInputFn: func(
					input *middleware.TransformableInput,
					fn inngestgo.ServableFunction,
				) {
					logs = append(logs, "mw: TransformInput")
				},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() middleware.Middleware{newMW},
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
		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context) {
					logs = append(logs, "mw: AfterExecution")
				},
				beforeExecutionFn: func(ctx context.Context) {
					logs = append(logs, "mw: BeforeExecution")
				},
				transformInputFn: func(
					input *middleware.TransformableInput,
					fn inngestgo.ServableFunction,
				) {
					logs = append(logs, "mw: TransformInput")
				},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW},
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

		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn:  func(ctx context.Context) {},
				beforeExecutionFn: func(ctx context.Context) {},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW},
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

		newMW1 := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context) {
					logs = append(logs, "1: AfterExecution")
				},
				beforeExecutionFn: func(ctx context.Context) {
					logs = append(logs, "1: BeforeExecution")
				},
				transformInputFn: func(
					input *middleware.TransformableInput,
					fn inngestgo.ServableFunction,
				) {
					logs = append(logs, "1: TransformInput")
				},
			}
		}

		newMW2 := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context) {
					logs = append(logs, "2: AfterExecution")
				},
				beforeExecutionFn: func(ctx context.Context) {
					logs = append(logs, "2: BeforeExecution")
				},
				transformInputFn: func(
					input *middleware.TransformableInput,
					fn inngestgo.ServableFunction,
				) {
					logs = append(logs, "2: TransformInput")
				},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW1, newMW2},
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

			newMW := func() experimental.Middleware {
				return &inlineMiddleware{
					transformInputFn: func(
						input *middleware.TransformableInput,
						fn inngestgo.ServableFunction,
					) {
						input.Event.Data["transformed"] = true

						for _, evt := range input.Events {
							evt.Data["transformed"] = true
						}
					},
				}
			}

			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID:      randomSuffix("app"),
				Middleware: []func() experimental.Middleware{newMW},
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

			newMW := func() experimental.Middleware {
				return &inlineMiddleware{
					transformInputFn: func(
						input *middleware.TransformableInput,
						fn inngestgo.ServableFunction,
					) {
						input.Event.Data["transformed"] = true

						for _, evt := range input.Events {
							evt.Data["transformed"] = true
						}
					},
				}
			}

			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID:      randomSuffix("app"),
				Middleware: []func() experimental.Middleware{newMW},
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

			newMW := func() experimental.Middleware {
				return &inlineMiddleware{
					transformInputFn: func(
						input *middleware.TransformableInput,
						fn inngestgo.ServableFunction,
					) {
						input.WithContext(context.WithValue(
							input.Context(), contextKey, "hello",
						))
					},
				}
			}

			c, err := inngestgo.NewClient(inngestgo.ClientOpts{
				AppID:      randomSuffix("app"),
				Middleware: []func() experimental.Middleware{newMW},
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

func TestLoggerMiddleware(t *testing.T) {
	devEnv(t)
	r := require.New(t)
	ctx := context.Background()

	// We need to use a fake level to let us filter out logs from stuff outside
	// the Inngest function. Since the logger middleware uses the client logger,
	// it'll capture unrelated logs like syncing the app.
	fakeLevel := 999

	logs := []string{}
	logger := slog.New(mockLogHandler{
		handle: func(ctx context.Context, record slog.Record) error {
			if int(record.Level) != fakeLevel {
				// Not a log from the Inngest function.
				return nil
			}
			logs = append(logs, record.Message)
			return nil
		},
	})

	c, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID:  randomSuffix("app"),
		Logger: logger,
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
		func(
			ctx context.Context,
			input inngestgo.Input[any],
		) (any, error) {
			l, err := experimental.LoggerFromContext(ctx)
			if err != nil {
				return nil, err
			}

			l.Log(ctx, slog.Level(fakeLevel), "fn start")

			_, err = step.Run(ctx, "a", func(ctx context.Context) (any, error) {
				l.Log(ctx, slog.Level(fakeLevel), "step a")
				return nil, nil
			})
			if err != nil {
				return nil, err
			}

			_, err = step.Run(ctx, "b", func(ctx context.Context) (any, error) {
				l.Log(ctx, slog.Level(fakeLevel), "step b")
				return nil, nil
			})
			if err != nil {
				return nil, err
			}

			l.Log(ctx, slog.Level(fakeLevel), "fn end")
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
			"fn start",
			"step a",
			"step b",
			"fn end",
		}, logs)
	}, 5*time.Second, 10*time.Millisecond)
}

// inlineMiddleware is allows for anonymous middleware to be created within
// functions.
type inlineMiddleware struct {
	beforeExecutionFn func(ctx context.Context)
	afterExecutionFn  func(ctx context.Context)
	transformInputFn  func(input *middleware.TransformableInput, fn inngestgo.ServableFunction)
}

func (m *inlineMiddleware) AfterExecution(ctx context.Context) {
	if m.afterExecutionFn == nil {
		return
	}
	m.afterExecutionFn(ctx)
}

func (m *inlineMiddleware) BeforeExecution(ctx context.Context) {
	if m.beforeExecutionFn == nil {
		return
	}
	m.beforeExecutionFn(ctx)
}

func (m *inlineMiddleware) TransformInput(
	input *middleware.TransformableInput,
	fn inngestgo.ServableFunction,
) {
	if m.transformInputFn == nil {
		return
	}
	m.transformInputFn(input, fn)
}

// mockLogHandler is a mock slog.Handler that can be used to test the
// LogMiddleware.
type mockLogHandler struct {
	handle func(ctx context.Context, record slog.Record) error
}

func (h mockLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return true
}

func (h mockLogHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.handle(ctx, record)
}

func (h mockLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h mockLogHandler) WithGroup(name string) slog.Handler {
	return h
}
