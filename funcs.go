package inngestgo

import (
	"context"
	"reflect"

	"github.com/gosimple/slug"
	"github.com/inngest/inngest/pkg/inngest"
)

type FunctionOpts struct {
	Name string
	// ID is an optional function ID.  If not specified, the ID
	// will be auto-generated by lowercasing and slugging the name.
	ID          *string
	Concurrency int
	Idempotency *string
	Retries     int
}

// CreateFunction creates a new function which can be registered within a handler.
//
// This function uses generics, allowing you to supply the event that triggers the function.
// For example, if you have a signup event defined as a struct you can use this to strongly
// type your input:
//
// 	type SignupEvent struct {
// 		Name string
// 		Data struct {
// 			Email     string
// 			AccountID string
// 		}
// 	}
//
// 	f := CreateFunction(
// 		"Post-signup flow",
// 		"user/signed.up",
// 		func(ctx context.Context, input gosdk.Input[SignupEvent], tools gosdk.Tools) (any, error) {
// 			// .. Your logic here.  input.Event will be strongly typed as a SignupEvent.
// 		},
// 	)
func CreateFunction[T any](
	fc FunctionOpts,
	trigger inngest.Trigger,
	f SDKFunction[T],
) ServableFunction {
	return servableFunc{
		fc:      fc,
		trigger: trigger,
		f:       f,
	}
}

func EventTrigger(name string) inngest.Trigger {
	return inngest.Trigger{
		EventTrigger: &inngest.EventTrigger{
			Event: name,
		},
	}
}

// SDKFunction represents a user-defined function to be called based off of events or
// on a schedule.
//
// The function is registered with the SDK by calling `CreateFunction` with the function
// name, the trigger, the event type for marshalling, and any options.
//
// This uses generics to strongly type input events:
//
// 	func(ctx context.Context, input gosdk.Input[SignupEvent]) (any, error) {
// 		// .. Your logic here.  input.Event will be strongly typed as a SignupEvent.
// 	}
type SDKFunction[T any] func(ctx context.Context, input Input[T]) (any, error)

// ServableFunction defines a function which can be called by a handler's Serve method.
//
// This is created via CreateFunction in this package.
type ServableFunction interface {
	// Slug returns the function's human-readable ID, such as "sign-up-flow".
	Slug() string

	// Name returns the function name.
	Name() string

	Config() FunctionOpts

	// Trigger returns the event name or schedule that triggers the function.
	Trigger() inngest.Trigger

	// ZeroEvent returns the zero event type to marshal the event into, given an
	// event name.
	ZeroEvent() any

	// Func returns the SDK function to call.  This must alawys be of type SDKFunction,
	// but has an any type as we register many functions of different types into a
	// type-agnostic handler; this is a generic implementation detail, unfortunately.
	Func() any
}

// Input is the input data passed to your function.  It is comprised of the triggering event
// and call context.
type Input[T any] struct {
	Event    T        `json:"event"`
	InputCtx InputCtx `json:"ctx"`
}

type InputCtx struct {
	FunctionID string `json:"fn_id"`
	RunID      string `json:"run_id"`
	StepID     string `json:"step_id"`
}

type servableFunc struct {
	fc      FunctionOpts
	trigger inngest.Trigger
	f       any
}

func (s servableFunc) Config() FunctionOpts {
	return s.fc
}

func (s servableFunc) Slug() string {
	if s.fc.ID == nil {
		return slug.Make(s.fc.Name)
	}
	return *s.fc.ID
}

func (s servableFunc) Name() string {
	return s.fc.Name
}

func (s servableFunc) Trigger() inngest.Trigger {
	return s.trigger
}

func (s servableFunc) ZeroEvent() any {
	// Grab the concrete type from the generic Input[T] type.  This lets us easily
	// initialize new values of this type at runtime.
	fVal := reflect.ValueOf(s.f)
	inputVal := reflect.New(fVal.Type().In(1)).Elem()
	return reflect.New(inputVal.FieldByName("Event").Type()).Elem().Interface()
}

func (s servableFunc) Func() any {
	return s.f
}
