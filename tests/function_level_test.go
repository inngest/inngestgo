package tests

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/stretchr/testify/require"
)

func TestFunctionLevel(t *testing.T) {
	devEnv(t)

	t.Run("panic", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("my-app"),
		})
		r.NoError(err)

		var runID atomic.Value
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
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
