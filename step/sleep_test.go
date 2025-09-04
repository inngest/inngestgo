package step

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/require"
	"github.com/xhit/go-str2duration/v2"
)

func TestSleepUntil(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	mw := middleware.New()
	mgr := sdkrequest.NewManager(sdkrequest.Opts{
		Middleware: mw,
		Cancel:     cancel,
		Request: &sdkrequest.Request{
			Steps: map[string]json.RawMessage{},
		},
		Mode: sdkrequest.StepModeYield,
	})
	ctx = sdkrequest.SetManager(ctx, mgr)
	ctx = internal.ContextWithMiddleware(ctx, mw)

	assertions := func(until time.Time) {
		ops := mgr.Ops()
		require.EqualValues(t, 1, len(ops))
		require.EqualValues(t, enums.OpcodeSleep, ops[0].Op)

		// duration should be present
		opts := ops[0].Opts.(map[string]any)
		require.NotEmpty(t, opts["duration"].(string))

		// Parsing this duration should be within ~1ms of now
		dur, err := str2duration.ParseDuration(opts["duration"].(string))
		require.NoError(t, err)
		require.WithinDuration(t, until, time.Now().Add(dur), 2*time.Millisecond)
	}

	t.Run("time.Time", func(t *testing.T) {
		parsed, err := time.Parse(time.RFC3339, "2040-04-01T00:00:00+07:00")
		require.NoError(t, err)

		func() {
			defer func() {
				rcv := recover()
				require.Equal(t, sdkrequest.ControlHijack{}, rcv)
			}()

			require.False(t, IsWithinStep(ctx))
			SleepUntil(ctx, "time.Time", parsed)
			assertions(parsed)
		}()
	})
}
