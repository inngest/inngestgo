package step

import (
	"context"
	"time"

	"github.com/inngest/inngest/pkg/dateutil"
	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/execution/state"
	str2duration "github.com/xhit/go-str2duration/v2"
)

type SleepOpts struct {
	ID string
	// Name represents the optional step name.
	Name string
}

func Sleep(ctx context.Context, id string, duration time.Duration) {
	mgr := preflight(ctx)
	op := mgr.NewOp(enums.OpcodeSleep, id, nil)
	if _, ok := mgr.Step(ctx, op); ok {
		// We've already slept.
		return
	}
	mgr.AppendOp(state.GeneratorOpcode{
		ID:   op.MustHash(),
		Op:   enums.OpcodeSleep,
		Name: id,
		Opts: map[string]any{
			"duration": str2duration.String(duration),
		},
	})
	panic(ControlHijack{})
}

type SleepUntilParam interface {
	time.Time | string
}

// SleepUntil sleeps until a given time.  This halts function execution entirely,
// and Inngest will resume the function after the given time from this step.
//
// This uses type constraints so that you can pass in a time.Time, or a string
// in one of the common RFC timestamps:
//
//	step.SleepUntil(ctx, "time.Time", time.Now().Add(time.Hour))
//	step.SleepUntil(ctx, "time.Time", "2025-04-01T00:00:00Z07:00")
//	step.SleepUntil(ctx, "time.Time", "2025-04-01")
//
// Supported timestamp formats are as follows:
//
//	time.RFC3339,
//	"2006-01-02T15:04:05",
//	time.RFC1123,
//	time.RFC822,
//	time.RFC822Z,
//	time.RFC850,
//	time.RubyDate,
//	time.UnixDate,
//	time.ANSIC,
//	time.Stamp,
//	time.StampMilli,
//	"2006-01-02",
//
// Strings or times without time zones will be parsed in the UTC timezone.  If
// a string is unable to be parsed, SleepUntil will resume immediately.
func SleepUntil[T SleepUntilParam](ctx context.Context, id string, until T) {
	var duration time.Duration

	switch v := any(until).(type) {
	case string:
		// Parse the time using built-in standards.
		t, _ := dateutil.Parse(until)
		duration = time.Until(t)
	case time.Time:
		duration = time.Until(v)
	}

	mgr := preflight(ctx)
	op := mgr.NewOp(enums.OpcodeSleep, id, nil)
	if _, ok := mgr.Step(ctx, op); ok {
		// We've already slept.
		return
	}
	mgr.AppendOp(state.GeneratorOpcode{
		ID:   op.MustHash(),
		Op:   enums.OpcodeSleep,
		Name: id,
		Opts: map[string]any{
			"duration": str2duration.String(duration),
		},
	})
	panic(ControlHijack{})
}
