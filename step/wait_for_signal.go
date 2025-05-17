package step

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/xhit/go-str2duration/v2"
)

// ErrSignalNotReceived is returned when a WaitForSignal call times out.  It indicates that a
// matching signal was not received before the timeout.
var ErrSignalNotReceived = fmt.Errorf("signal not received")

type WaitForSignalOpts struct {
	// Name represents the optional step name.
	Name string
	// Signal is the signal to wait for.  This is a string unique to your environment
	// which will resume this particular function run.  If this signal already exists,
	// the step will error.
	//
	// For resuming multiple runs from a signal, use WaitForEvent.  Generally speaking,
	// WaitForEvent fulfils WaitForSignal with fan out and an improved DX.
	Signal string
	// Timeout is how long to wait.  We must always timebound event lsiteners.
	Timeout time.Duration
}

func WaitForSignal[T any](ctx context.Context, stepID string, opts WaitForSignalOpts) (T, error) {
	mgr := preflight(ctx)

	args := map[string]any{
		"signal":  opts.Signal,
		"timeout": str2duration.String(opts.Timeout),
	}
	if opts.Name == "" {
		opts.Name = stepID
	}

	op := mgr.NewOp(enums.OpcodeWaitForSignal, stepID, args)

	// Check if this exists already.
	if val, ok := mgr.Step(ctx, op); ok {
		var output T
		if val == nil || bytes.Equal(val, []byte{0x6e, 0x75, 0x6c, 0x6c}) {
			return output, ErrSignalNotReceived
		}
		if err := json.Unmarshal(val, &output); err != nil {
			mgr.SetErr(fmt.Errorf("error unmarshalling wait for signal value in '%s': %w", opts.Signal, err))
			panic(ControlHijack{})
		}
		return output, nil
	}

	mgr.AppendOp(sdkrequest.GeneratorOpcode{
		ID:          op.MustHash(),
		Op:          op.Op,
		Name:        opts.Name,
		DisplayName: &opts.Name,
		Opts:        op.Opts,
	})
	panic(ControlHijack{})
}
