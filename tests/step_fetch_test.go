package tests

import (
	"context"
	"fmt"
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
				fmt.Println(res)

				runID.Store(input.InputCtx.RunID)
				panic("oops")
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
}
