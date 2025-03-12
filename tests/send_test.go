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

func TestSend(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var receivedEventID string
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
				input inngestgo.Input[inngestgo.GenericEvent[any, any]],
			) (any, error) {
				receivedEventID = *input.Event.ID
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
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				sentEventID, sendErr = step.Send(ctx,
					"send",
					inngestgo.Event{Name: childEventName},
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

			a.Equal(enums.RunStatusCompleted.String(), run.Status)
			a.Equal(sentEventID, receivedEventID)
			a.NoError(sendErr)
		}, 5*time.Second, time.Second)
	})
	t.Run("error", func(t *testing.T) {
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

func TestSendMany(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

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
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				sentEventIDs, sendErr = step.SendMany(ctx,
					"send",
					[]inngestgo.Event{
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

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
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
	t.Run("error", func(t *testing.T) {
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
