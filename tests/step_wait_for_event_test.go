package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStepWaitForEvent(t *testing.T) {
	devEnv(t)

	t.Run("match", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID string
		var stepResult map[string]any
		var stepError error
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
				stepResult, stepError = step.WaitForEvent[map[string]any](ctx,
					"a",
					step.WaitForEventOpts{
						Name:    fmt.Sprintf("%s-fulfilled", eventName),
						Timeout: 10 * time.Second,
					},
				)
				return stepResult, stepError
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		// Wait for run to start.
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)
			a.NotEmpty(runID)
		}, 5*time.Second, time.Second)

		_, err = c.Send(ctx, inngestgo.Event{
			Name: fmt.Sprintf("%s-fulfilled", eventName),
			Data: map[string]any{"foo": "bar"},
		})
		r.NoError(err)

		waitForRun(t, &runID, enums.RunStatusCompleted.String())
		r.NoError(stepError)
		r.Equal(fmt.Sprintf("%s-fulfilled", eventName), stepResult["name"])
		r.Equal(map[string]any{"foo": "bar"}, stepResult["data"])
	})

	t.Run("timeout", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID string
		var stepResult map[string]any
		var stepError error
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
				stepResult, stepError = step.WaitForEvent[map[string]any](ctx,
					"a",
					step.WaitForEventOpts{
						Name:    fmt.Sprintf("%s-never", eventName),
						Timeout: time.Second,
					},
				)
				return stepResult, stepError
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
		r.ErrorIs(stepError, step.ErrEventNotReceived)
		r.Nil(stepResult)
	})
}
