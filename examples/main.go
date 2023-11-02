package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/step"
)

func main() {
	h := inngestgo.NewHandler("core", inngestgo.HandlerOpts{})
	f := inngestgo.CreateFunction(
		inngestgo.FunctionOpts{
			ID: "account-created",
		},
		// Run on every api/account.created event.
		inngestgo.EventTrigger("api/account.created", nil),
		UserCreated,
	)
	h.Register(f)
	http.ListenAndServe(":8080", h)
}

func UserCreated(ctx context.Context, input inngestgo.Input[any]) (any, error) {
	// Sleep for a second
	step.Sleep(ctx, "initial-delay", time.Second)

	// Run a step which emails the user.
	step.Run(ctx, "on-user-created", func(ctx context.Context) (string, error) {
		return "", nil
	})

	fn, err := step.WaitForEvent[FunctionCreatedEvent](ctx, "wait-for-activity", step.WaitForEventOpts{
		Name:    "Wait for a function to be created",
		Event:   "api/function.created",
		If:      inngestgo.StrPtr("async.data.user_id == event.data.user_id"),
		Timeout: time.Hour * 72,
	})
	if err == step.ErrEventNotReceived {
		// A function wasn't created within 3 days.
		return nil, nil
	}

	// The function event is fully typed :)
	fmt.Println(fn.Data.FunctionID)

	return nil, nil
}

// AccountCreatedEvent represents the fully defined event received when an account is created.
//
// This is shorthand for defining a new Inngest-conforming struct:
//
//	type AccountCreatedEvent struct {
//		Name      string                  `json:"name"`
//		Data      AccountCreatedEventData `json:"data"`
//		User      any                     `json:"user"`
//		Timestamp int64                   `json:"ts,omitempty"`
//		Version   string                  `json:"v,omitempty"`
//	}
type AccountCreatedEvent inngestgo.GenericEvent[AccountCreatedEventData, any]
type AccountCreatedEventData struct {
	AccountID string
}

type FunctionCreatedEvent inngestgo.GenericEvent[FunctionCreatedEventData, any]
type FunctionCreatedEventData struct {
	FunctionID string
}
