package tests

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/experimental"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientMiddleware(t *testing.T) {
	devEnv(t)

	t.Run("panic capturing for error handling", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		var (
			recovery any
			stack    string
			hit      int32
		)

		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				onPanic: func(ctx context.Context, call experimental.CallContext, r any, s string) {
					recovery = r
					stack = s

					atomic.AddInt32(&hit, 1)
				},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW},
			Logger:     slog.New(slog.DiscardHandler),
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

				step.Run(ctx, "step", func(ctx context.Context) (string, error) {
					return "ok", nil
				})

				panic("nah")
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
			val := atomic.LoadInt32(&hit)
			a.True(atomic.LoadInt32(&hit) >= 1)
			if val == 0 {
				return
			}
			a.Contains(recovery, "nah")
			a.Contains(stack, "middleware_test.go:")
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("step output, fn output", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		var (
			results SafeSlice[any]
			errors  SafeSlice[error]
		)

		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context, call experimental.CallContext, result any, err error) {
					results.Append(result)
					errors.Append(err)
				},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW},
			Logger:     slog.New(slog.DiscardHandler),
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
				step.Run(ctx, "step", func(ctx context.Context) (string, error) {
					return "ok", nil
				})
				return 1.1, nil
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
			a.Equal([]any{
				// First request.
				"ok",
				1.1,
			}, results.Load())
			a.Equal([]error{
				nil,
				nil,
			}, errors.Load(), "%#v", errors.Load())
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("step error", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		var (
			results SafeSlice[any]
			errors  SafeSlice[error]
		)

		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context, call experimental.CallContext, result any, err error) {
					results.Append(result)
					errors.Append(err)
				},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW},
			Logger:     slog.New(slog.DiscardHandler),
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
				step.Run(ctx, "step", func(ctx context.Context) (string, error) {
					return "ok", fmt.Errorf("this is an error")
				})
				return 1.1, nil
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
			a.Equal([]any{
				// First request.
				"ok",
			}, results.Load())
			a.Equal([]error{
				fmt.Errorf("this is an error"),
			}, errors.Load(), "%#v", errors.Load())
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("fn error", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		var (
			results SafeSlice[any]
			errors  SafeSlice[error]
		)

		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context, call experimental.CallContext, result any, err error) {
					results.Append(result)
					errors.Append(err)
				},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW},
			Logger:     slog.New(slog.DiscardHandler),
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
				return nil, fmt.Errorf("fn error")
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
			a.Equal([]any{
				nil,
			}, results.Load())
			a.Equal([]error{
				fmt.Errorf("fn error"),
			}, errors.Load(), "%#v", errors.Load())
		}, 5*time.Second, 10*time.Millisecond)
	})

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

		var runID atomic.Value
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)
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
	})

	t.Run("2 steps", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		var logs SafeSlice[string]
		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context, call experimental.CallContext, result any, err error) {
					logs.Append("mw: AfterExecution")
				},
				beforeExecutionFn: func(ctx context.Context, call experimental.CallContext) {
					logs.Append("mw: BeforeExecution")
				},
				transformInputFn: func(
					input *experimental.TransformableInput,
					fn inngestgo.ServableFunction,
				) {
					logs.Append("mw: TransformInput")
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
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				logs.Append("fn: top")

				_, err := step.Run(ctx, "a", func(ctx context.Context) (any, error) {
					logs.Append("a: running")
					return nil, nil
				})
				if err != nil {
					return nil, err
				}

				logs.Append("fn: between steps")

				_, err = step.Run(ctx, "b", func(ctx context.Context) (any, error) {
					logs.Append("b: running")
					return nil, nil
				})

				logs.Append("fn: bottom")
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
			}, logs.Load())
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("retry", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		var logs SafeSlice[string]
		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context, call experimental.CallContext, result any, err error) {
					logs.Append("mw: AfterExecution")
				},
				beforeExecutionFn: func(ctx context.Context, call experimental.CallContext) {
					logs.Append("mw: BeforeExecution")
				},
				transformInputFn: func(
					input *experimental.TransformableInput,
					fn inngestgo.ServableFunction,
				) {
					logs.Append("mw: TransformInput")
				},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW},
			Logger:     slog.New(slog.DiscardHandler),
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
				logs.Append("fn: top")
				if input.InputCtx.Attempt == 0 {
					return nil, inngestgo.RetryAtError(
						errors.New("oh no"),
						time.Now().Add(time.Second),
					)
				}
				logs.Append("fn: bottom")
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
			}, logs.Load())
		}, 5*time.Second, 10*time.Millisecond)
	})

	t.Run("connect", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		newMW := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn:  func(ctx context.Context, call experimental.CallContext, result any, err error) {},
				beforeExecutionFn: func(ctx context.Context, call experimental.CallContext) {},
			}
		}

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID:      randomSuffix("app"),
			Middleware: []func() experimental.Middleware{newMW},
			Logger:     slog.New(slog.DiscardHandler),
		})
		r.NoError(err)

		var runID atomic.Value
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID.Store(input.InputCtx.RunID)
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
		waitForRun(t, &runID, enums.RunStatusCompleted.String())
	})

	t.Run("multiple", func(t *testing.T) {
		r := require.New(t)
		ctx := context.Background()

		var logs SafeSlice[string]

		newMW1 := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context, call experimental.CallContext, result any, err error) {
					logs.Append("1: AfterExecution")
				},
				beforeExecutionFn: func(ctx context.Context, call experimental.CallContext) {
					logs.Append("1: BeforeExecution")
				},
				transformInputFn: func(
					input *experimental.TransformableInput,
					fn inngestgo.ServableFunction,
				) {
					logs.Append("1: TransformInput")
				},
			}
		}

		newMW2 := func() experimental.Middleware {
			return &inlineMiddleware{
				afterExecutionFn: func(ctx context.Context, call experimental.CallContext, result any, err error) {
					logs.Append("2: AfterExecution")
				},
				beforeExecutionFn: func(ctx context.Context, call experimental.CallContext) {
					logs.Append("2: BeforeExecution")
				},
				transformInputFn: func(
					input *experimental.TransformableInput,
					fn inngestgo.ServableFunction,
				) {
					logs.Append("2: TransformInput")
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
			}, logs.Load())
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
						input *experimental.TransformableInput,
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

			var runID atomic.Value
			var event *inngestgo.GenericEvent[map[string]any]
			var events []inngestgo.GenericEvent[map[string]any]
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
					input inngestgo.Input[map[string]any],
				) (any, error) {
					runID.Store(input.InputCtx.RunID)
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
			waitForRun(t, &runID, enums.RunStatusCompleted.String())

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
						input *experimental.TransformableInput,
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

			var runID atomic.Value
			var event *inngestgo.GenericEvent[any]
			var events []inngestgo.GenericEvent[any]
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
					runID.Store(input.InputCtx.RunID)
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
			waitForRun(t, &runID, enums.RunStatusCompleted.String())

			// Assert event.
			r.Equal(eventName, event.Name)
			r.Equal(map[string]any{
				"msg":         "hi",
				"transformed": true,
			}, event.Data)

			// Assert events.
			r.Len(events, 1)
			r.Equal(eventName, events[0].Name)
			r.Equal(map[string]any{
				"msg":         "hi",
				"transformed": true,
			}, events[0].Data)
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
						input *experimental.TransformableInput,
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

			var runID atomic.Value
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
					runID.Store(input.InputCtx.RunID)
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
			waitForRun(t, &runID, enums.RunStatusCompleted.String())
			r.Equal("hello", value)
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

	var logs SafeSlice[string]
	logger := slog.New(mockLogHandler{
		handle: func(ctx context.Context, record slog.Record) error {
			if int(record.Level) != fakeLevel {
				// Not a log from the Inngest function.
				return nil
			}
			logs.Append(record.Message)
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
		}, logs.Load())
	}, 5*time.Second, 10*time.Millisecond)
}

// inlineMiddleware is allows for anonymous middleware to be created within
// functions.
type inlineMiddleware struct {
	// automatically implement any new methods.
	experimental.BaseMiddleware

	beforeExecutionFn func(ctx context.Context, call experimental.CallContext)
	afterExecutionFn  func(ctx context.Context, call experimental.CallContext, result any, err error)
	transformInputFn  func(input *experimental.TransformableInput, fn inngestgo.ServableFunction)
	onPanic           func(ctx context.Context, call experimental.CallContext, recovery any, stack string)
}

func (m *inlineMiddleware) BeforeExecution(ctx context.Context, call experimental.CallContext) {
	if m.beforeExecutionFn == nil {
		return
	}
	m.beforeExecutionFn(ctx, call)
}

func (m *inlineMiddleware) AfterExecution(ctx context.Context, call experimental.CallContext, result any, err error) {
	if m.afterExecutionFn == nil {
		return
	}
	m.afterExecutionFn(ctx, call, result, err)
}

func (m *inlineMiddleware) OnPanic(ctx context.Context, call experimental.CallContext, recovery any, stack string) {
	if m.onPanic == nil {
		return
	}
	m.onPanic(ctx, call, recovery, stack)
}

func (m *inlineMiddleware) TransformInput(
	input *experimental.TransformableInput,
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
