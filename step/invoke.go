package step

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/execution/state"
	sdkerrors "github.com/inngest/inngestgo/errors"
	"github.com/inngest/inngestgo/internal/fn"
	"github.com/xhit/go-str2duration/v2"
)

type InvokeOpts struct {
	// Target function.
	Function fn.ServableFunction

	// Data is the data to pass to the invoked function.
	Data map[string]any

	// User is the user data to pass to the invoked function.
	User any

	// Timeout is an optional duration specifying when the invoked function will be
	// considered timed out
	Timeout time.Duration
}

func Invoke[T any](ctx context.Context, id string, opts InvokeOpts) (T, error) {
	return InvokeByID[T](ctx, id, InvokeByIDOpts{
		AppID:      opts.Function.AppID(),
		FunctionID: opts.Function.ID(),
		Data:       opts.Data,
		User:       opts.User,
		Timeout:    opts.Timeout,
	})
}

type InvokeByIDOpts struct {
	// Target function's app ID. This is the same as the function's client ID.
	AppID string

	// Target function's ID. This does not include the app ID prefix.
	FunctionID string

	// Data is the data to pass to the invoked function.
	Data map[string]any

	// User is the user data to pass to the invoked function.
	User any

	// Timeout is an optional duration specifying when the invoked function will be
	// considered timed out
	Timeout time.Duration
}

func (o InvokeByIDOpts) validate() error {
	var err error
	if o.AppID == "" {
		err = errors.Join(err, fmt.Errorf("appID is required"))
	}
	if o.FunctionID == "" {
		err = errors.Join(err, fmt.Errorf("functionID is required"))
	}
	return err
}

// Invoke another Inngest function using its ID. Returns the value returned from
// that function.
//
// If the invoked function can't be found or otherwise errors, the step will
// fail and the function will stop with a `NoRetryError`.
func InvokeByID[T any](ctx context.Context, id string, opts InvokeByIDOpts) (T, error) {
	mgr := preflight(ctx)
	if err := opts.validate(); err != nil {
		mgr.SetErr(err)
		panic(ControlHijack{})
	}
	fnID := fmt.Sprintf("%s-%s", opts.AppID, opts.FunctionID)

	args := map[string]any{
		"function_id": fnID,
		"payload": map[string]any{
			"data": opts.Data,
			"user": opts.User,
		},
	}
	if opts.Timeout > 0 {
		args["timeout"] = str2duration.String(opts.Timeout)
	}

	op := mgr.NewOp(enums.OpcodeInvokeFunction, id, args)
	if val, ok := mgr.Step(ctx, op); ok {
		var output T
		var valMap map[string]json.RawMessage
		if err := json.Unmarshal(val, &valMap); err != nil {
			mgr.SetErr(fmt.Errorf("error unmarshalling invoke value for '%s': %w", fnID, err))
			panic(ControlHijack{})
		}

		if data, ok := valMap["data"]; ok {
			if err := json.Unmarshal(data, &output); err != nil {
				mgr.SetErr(fmt.Errorf("error unmarshalling invoke data for '%s': %w", fnID, err))
				panic(ControlHijack{})
			}
			return output, nil
		}

		// Handled in this single tool until we want to make broader changes
		// to add per-step errors everywhere.
		if errorVal, ok := valMap["error"]; ok {
			var errObj struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(errorVal, &errObj); err != nil {
				mgr.SetErr(fmt.Errorf("error unmarshalling invoke error for '%s': %w", fnID, err))
				panic(ControlHijack{})
			}

			return output, sdkerrors.NoRetryError(fmt.Errorf("%s", errObj.Message))
		}

		mgr.SetErr(fmt.Errorf("error parsing invoke value for '%s'; unknown shape", fnID))
		panic(ControlHijack{})
	}

	mgr.AppendOp(state.GeneratorOpcode{
		ID:   op.MustHash(),
		Op:   op.Op,
		Name: id,
		Opts: op.Opts,
	})
	panic(ControlHijack{})
}
