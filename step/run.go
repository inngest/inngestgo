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

type response struct {
	Data  json.RawMessage `json:"data"`
	Error json.RawMessage `json:"error"`
}

// StepRun runs any code reliably, with retries, returning the resulting data.  If this
// fails the function stops.
//
// TODO: Allow users to catch single step errors.
func Run[T any](
	ctx context.Context,
	id string,
	f func(ctx context.Context) (T, error),
) (T, error) {
	mgr := preflight(ctx)
	op := mgr.NewOp(enums.OpcodeStep, id, nil)

	if val, ok := mgr.Step(op); ok {
		// Create a new empty type T in v
		ft := reflect.TypeOf(f)
		v := reflect.New(ft.Out(0)).Interface()

		// This step has already ran as we have state for it. Unmarshal the JSON into type T
		unwrapped := response{}
		if err := json.Unmarshal(val, &unwrapped); err == nil {
			// Check for step errors first.
			if len(unwrapped.Error) > 0 {
				val, _ := reflect.ValueOf(v).Elem().Interface().(T)
				// xxx: improve this error
				return val, fmt.Errorf("step error: %s", string(unwrapped.Error))
			}

			// If there's an error, assume that val is already of type T without wrapping
			// in the 'data' object as per the SDK spec.  Here, if this succeeds we can be
			// sure that we're wrapping the data in a compliant way.
			if len(unwrapped.Data) > 0 {
				val = unwrapped.Data
			}
		}

		// Grab the data as the step type.
		if err := json.Unmarshal(val, v); err != nil {
			mgr.SetErr(fmt.Errorf("error unmarshalling state for step '%s': %w", id, err))
			panic(ControlHijack{})
		}
		val, _ := reflect.ValueOf(v).Elem().Interface().(T)
		return val, nil
	}

	// We're calling a function, so always cancel the context afterwards so that no
	// other tools run.
	defer mgr.Cancel()

	result, err := f(ctx)
	if err != nil {
		// TODO: Implement step errors.
		mgr.SetErr(err)
		panic(ControlHijack{})
	}

	// Spec RFC:  always wrap the response in a data object.
	byt, err := json.Marshal(map[string]any{
		"data": result,
	})
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
