package step

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/execution/state"
)

type RunOpts struct {
	// ID represents the optional step name.
	ID string
	// Name represents the optional step name.
	Name string
}

// StepRun runs any code reliably, with retries, returning the resulting data.  If this
// fails the function stops.
//
// TODO: Allow users to catch single step errors.
func Run[T any](
	ctx context.Context,
	id string,
	f func(ctx context.Context) (T, error),
) T {
	mgr := preflight(ctx)
	op := mgr.NewOp(enums.OpcodeStep, id, nil)

	if val, ok := mgr.Step(op); ok {
		// This step has already ran as we have state for it.
		// Unmarshal the JSON into type T
		ft := reflect.TypeOf(f)
		v := reflect.New(ft.Out(0)).Interface()
		if err := json.Unmarshal(val, v); err != nil {
			mgr.SetErr(fmt.Errorf("error unmarshalling state for step '%s': %w", id, err))
			panic(ControlHijack{})
		}
		val, _ := reflect.ValueOf(v).Elem().Interface().(T)
		return val
	}

	// We're calling a function, so always cancel the context afterwards so that no
	// other tools run.
	defer mgr.Cancel()

	result, err := f(ctx)
	if err != nil {
		mgr.SetErr(err)
		panic(ControlHijack{})
	}

	byt, err := json.Marshal(result)
	if err != nil {
		mgr.SetErr(fmt.Errorf("unable to marshal run respone for '%s': %w", id, err))
	}

	mgr.AppendOp(state.GeneratorOpcode{
		ID:   op.MustHash(),
		Op:   enums.OpcodeStep,
		Name: id,
		Data: byt,
	})
	panic(ControlHijack{})
}
