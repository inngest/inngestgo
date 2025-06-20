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

func TestStepFetch(t *testing.T) {
	devEnv(t)

	t.Run("panic", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		eventName := randomSuffix("my-event")
		var runID atomic.Value
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				res, err := step.Fetch[string](ctx, "test-id", step.FetchOpts{
					URL:    "https://example.com",
					Method: "GET",
				})
				r.NoError(err)
				r.EqualValues("https://example.com", res.URL)
				r.EqualValues(200, res.StatusCode)
				r.Contains(res.Body, "Example Domain")

				runID.Store(input.InputCtx.RunID)
				return res.Body, nil
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
		r.NoError(err)

		run := waitForRun(t, &runID, enums.RunStatusCompleted.String())
		_ = run // dont actually need it.
	})
}
