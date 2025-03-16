package tests

import (
	"context"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStepSend(t *testing.T) {
	devEnv(t)

	t.Run("Event type", func(t *testing.T) {
		// Successfully send the builtin event type.

		ctx := context.Background()
		r := require.New(t)

		type MyEvent = inngestgo.Event

		appName := randomSuffix("app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var receivedEvent *MyEvent
		childEventName := randomSuffix("child-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "child-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(childEventName, nil),
			func(
				ctx context.Context,
				input inngestgo.Input[map[string]any],
			) (any, error) {
				receivedEvent = &input.Event
				return nil, nil
			},
		)
		r.NoError(err)

		var runID string
		var sentEventID string
		var sendErr error
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "parent-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(
				ctx context.Context,
				input inngestgo.Input[any],
			) (any, error) {
				runID = input.InputCtx.RunID
				sentEventID, sendErr = step.Send(ctx,
					"send",
					MyEvent{
						Data: map[string]any{"msg": "hi"},
						Name: childEventName,
					},
				)
				return nil, sendErr
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, MyEvent{Name: eventName})
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)

			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}

			a.Equal(enums.RunStatusCompleted.String(), run.Status)
			if !a.NotNil(receivedEvent) {
				return
			}
			a.Equal(sentEventID, *receivedEvent.ID)
			a.Equal(map[string]any{"msg": "hi"}, receivedEvent.Data)
			a.NoError(sendErr)
		}, 5*time.Second, time.Second)
	})

	t.Run("GenericEvent type", func(t *testing.T) {
		// Successfully send a custom event type.

		ctx := context.Background()
		r := require.New(t)

		type MyEventData = struct {
			Msg string
		}
		type MyEvent = inngestgo.GenericEvent[MyEventData]

		appName := randomSuffix("app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var receivedEvent *MyEvent
		childEventName := randomSuffix("child-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "child-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(childEventName, nil),
			func(
				ctx context.Context,
				input inngestgo.Input[MyEventData],
			) (any, error) {
				receivedEvent = &input.Event
				return nil, nil
			},
		)
		r.NoError(err)

		var runID string
		var sentEventID string
		var sendErr error
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "parent-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(
				ctx context.Context,
				input inngestgo.Input[MyEventData],
			) (any, error) {
				runID = input.InputCtx.RunID
				sentEventID, sendErr = step.Send(ctx,
					"send",
					MyEvent{
						Data: MyEventData{Msg: "hi"},
						Name: childEventName,
					},
				)
				return nil, sendErr
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, MyEvent{Name: eventName})
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)

			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}

			a.Equal(enums.RunStatusCompleted.String(), run.Status)
			if !a.NotNil(receivedEvent) {
				return
			}
			a.Equal(sentEventID, *receivedEvent.ID)
			a.Equal(MyEventData{Msg: "hi"}, receivedEvent.Data)
			a.NoError(sendErr)
		}, 5*time.Second, time.Second)
	})

	t.Run("error due to internal event name", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var runID string
		var sendErr error
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "parent-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				_, sendErr = step.Send(ctx,
					"send",
					inngestgo.Event{Name: "inngest/foo"},
				)
				return nil, sendErr
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)

			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}

			a.Equal(enums.RunStatusFailed.String(), run.Status)
			a.Error(sendErr)
			a.Equal(
				"bad request: event name is reserved for internal use: inngest/foo",
				sendErr.Error(),
			)
		}, 5*time.Second, time.Second)
	})
}

func TestStepSendMany(t *testing.T) {
	devEnv(t)

	t.Run("Event type", func(t *testing.T) {
		// Successfully send the builtin event type.

		ctx := context.Background()
		r := require.New(t)

		type MyEvent = inngestgo.Event

		appName := randomSuffix("app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var runID string
		var sentEventIDs []string
		var sendErr error
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "parent-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(
				ctx context.Context,
				input inngestgo.Input[any],
			) (any, error) {
				runID = input.InputCtx.RunID
				sentEventIDs, sendErr = step.SendMany(ctx,
					"send",
					[]MyEvent{
						{Name: randomSuffix("child-event")},
						{Name: randomSuffix("child-event")},
					},
				)
				return nil, sendErr
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, MyEvent{Name: eventName})
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)

			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}

			a.Equal(enums.RunStatusCompleted.String(), run.Status)
			a.Len(sentEventIDs, 2)
			a.NoError(sendErr)
		}, 5*time.Second, time.Second)
	})

	t.Run("GenericEvent type", func(t *testing.T) {
		// Successfully send a custom event type.

		ctx := context.Background()
		r := require.New(t)

		type MyEventData = struct {
			Msg string
		}
		type MyEvent = inngestgo.GenericEvent[MyEventData]

		appName := randomSuffix("app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var runID string
		var sentEventIDs []string
		var sendErr error
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "parent-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(
				ctx context.Context,
				input inngestgo.Input[MyEventData],
			) (any, error) {
				runID = input.InputCtx.RunID
				sentEventIDs, sendErr = step.SendMany(ctx,
					"send",
					[]MyEvent{
						{Name: randomSuffix("child-event")},
						{Name: randomSuffix("child-event")},
					},
				)
				return nil, sendErr
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, MyEvent{Name: eventName})
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)

			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}

			a.Equal(enums.RunStatusCompleted.String(), run.Status)
			a.Len(sentEventIDs, 2)
			a.NoError(sendErr)
		}, 5*time.Second, time.Second)
	})

	t.Run("error due to internal event name", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var runID string
		var sendErr error
		eventName := randomSuffix("event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "parent-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				_, sendErr = step.SendMany(ctx,
					"send",
					[]inngestgo.Event{
						{Name: "inngest/foo"},
						{Name: "inngest/foo"},
					},
				)
				return nil, sendErr
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)

			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}

			a.Equal(enums.RunStatusFailed.String(), run.Status)
			a.Error(sendErr)
			a.Equal(
				"bad request: event name is reserved for internal use: inngest/foo",
				sendErr.Error(),
			)
		}, 5*time.Second, time.Second)
	})
}
