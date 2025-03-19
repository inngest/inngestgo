package step

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/require"
	"github.com/xhit/go-str2duration/v2"
)

func TestSleepUntil(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	mw := middleware.NewMiddlewareManager()
	mgr := sdkrequest.NewManager(mw, cancel, &sdkrequest.Request{
		Steps: map[string]json.RawMessage{},
	}, "")
	ctx = sdkrequest.SetManager(ctx, mgr)

	reset := func() {
		mgr = sdkrequest.NewManager(mw, cancel, &sdkrequest.Request{
			Steps: map[string]json.RawMessage{},
		}, "")
		ctx = sdkrequest.SetManager(ctx, mgr)
	}

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

		// New steps always panic.
		require.Panics(t, func() {
			SleepUntil(ctx, "time.Time", parsed)
		})

		assertions(parsed)
	})

	t.Run("RFC3339 with TZ", func(t *testing.T) {
		parsed, err := time.Parse(time.RFC3339, "2040-04-01T00:00:00+07:00")
		require.NoError(t, err)

		// New steps always panic.
		require.Panics(t, func() {
			reset()
			SleepUntil(ctx, "time.Time", "2040-04-01T00:00:00+07:00")
		})

		assertions(parsed)
	})

	t.Run("RFC3339 without TZ", func(t *testing.T) {
		parsed, err := time.Parse(time.RFC3339, "2040-04-01T00:00:00Z")
		require.NoError(t, err)

		// New steps always panic.
		require.Panics(t, func() {
			reset()
			SleepUntil(ctx, "time.Time", "2040-04-01T00:00:00Z")
		})

		assertions(parsed)
	})

	t.Run("RFC3339 in UTC without Z (non-compliant people)", func(t *testing.T) {
		parsed, err := time.Parse(time.RFC3339, "2040-04-01T00:00:00Z")
		require.NoError(t, err)

		// New steps always panic.
		require.Panics(t, func() {
			reset()
			// omit the Z, because some people dont conform to specs...
			SleepUntil(ctx, "time.Time", "2040-04-01T00:00:00")
		})

		assertions(parsed)
	})

	t.Run("Dates", func(t *testing.T) {
		parsed, err := time.Parse(time.RFC3339, "2040-04-01T00:00:00Z")
		require.NoError(t, err)

		// New steps always panic.
		require.Panics(t, func() {
			reset()
			SleepUntil(ctx, "time.Time", "2040-04-01")
		})

		assertions(parsed)
	})
}
