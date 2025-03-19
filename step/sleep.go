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

func SleepUntil[T SleepUntilParam](ctx context.Context, id string, until T) {
	var duration time.Duration

	switch v := any(until).(type) {
	case string:
		// Parse the time using built-in standards.
		t, _ := dateutil.Parse(until)
		// XXX: Handle error.
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
