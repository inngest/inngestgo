package tests

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/step"
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

		var runID atomic.Value
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
				runID.Store(input.InputCtx.RunID)
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
		waitForRun(t, &runID, enums.RunStatusCompleted.String())
		r.NotNil(receivedEvent)
		r.Equal(sentEventID, *receivedEvent.ID)
		r.Equal(map[string]any{"msg": "hi"}, receivedEvent.Data)
		r.NoError(sendErr)
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

		var runID atomic.Value
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
				runID.Store(input.InputCtx.RunID)
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
		waitForRun(t, &runID, enums.RunStatusCompleted.String())
		r.NotNil(receivedEvent)
		r.Equal(sentEventID, *receivedEvent.ID)
		r.Equal(MyEventData{Msg: "hi"}, receivedEvent.Data)
		r.NoError(sendErr)
	})

	t.Run("error due to internal event name", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var runID atomic.Value
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
				runID.Store(input.InputCtx.RunID)
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
		waitForRun(t, &runID, enums.RunStatusFailed.String())
		r.Error(sendErr)
		r.Equal(
			"bad request: event name is reserved for internal use: inngest/foo",
			sendErr.Error(),
		)
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

		var runID atomic.Value
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
				runID.Store(input.InputCtx.RunID)
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
		waitForRun(t, &runID, enums.RunStatusCompleted.String())
		r.Len(sentEventIDs, 2)
		r.NoError(sendErr)
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

		var runID atomic.Value
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
				runID.Store(input.InputCtx.RunID)
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
		waitForRun(t, &runID, enums.RunStatusCompleted.String())
		r.Len(sentEventIDs, 2)
		r.NoError(sendErr)
	})

	t.Run("error due to internal event name", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var runID atomic.Value
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
				runID.Store(input.InputCtx.RunID)
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
		waitForRun(t, &runID, enums.RunStatusFailed.String())
		r.Error(sendErr)
		r.Equal(
			"bad request: event name is reserved for internal use: inngest/foo",
			sendErr.Error(),
		)
	})
}
