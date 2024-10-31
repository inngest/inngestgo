package main

import (
	"context"
	"fmt"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/step"
	"os"
	"time"
)

func main() {
	testKey := "signkey-test-12345678"
	testKeyFallback := "signkey-test-00000000"

	os.Setenv("INNGEST_DEV", "1")
	os.Setenv("INNGEST_EVENT_KEY", "abc123")
	os.Setenv("INNGEST_SIGNING_KEY", string(testKey))
	os.Setenv("INNGEST_SIGNING_KEY_FALLBACK", string(testKeyFallback))

	type AccountCreatedEventData struct {
		AccountID string
	}
	type AccountCreatedEvent inngestgo.GenericEvent[AccountCreatedEventData, any]

	type FunctionCreatedEventData struct {
		FunctionID string
	}
	type FunctionCreatedEvent inngestgo.GenericEvent[FunctionCreatedEventData, any]

	// AccountCreated is a durable function which runs any time the "api/account.created"
	// event is received by Inngest.
	//
	// It is invoked by Inngest, with each step being backed by Inngest's orchestrator.
	// Function state is automatically managed, and persists across server restarts,
	// cloud migrations, and language changes.
	testFn := func(ctx context.Context, input inngestgo.Input[AccountCreatedEvent]) (any, error) {
		// Sleep for a second, minute, hour, week across server restarts.
		step.Sleep(ctx, "initial-delay", time.Second)

		// Run a step which emails the user.  This automatically retries on error.
		// This returns the fully typed result of the lambda.
		result, err := step.Run(ctx, "on-user-created", func(ctx context.Context) (any, error) {
			fmt.Println("example step")
			return nil, nil
		})
		if err != nil {
			// This step retried 5 times by default and permanently failed.
			return nil, err
		}
		// `result` is  fully typed from the lambda
		_ = result

		return nil, nil
	}

	h := inngestgo.NewHandler("core", inngestgo.HandlerOpts{
		Dev: inngestgo.BoolPtr(true),
	})
	f := inngestgo.CreateFunction(
		inngestgo.FunctionOpts{
			ID:   "account-created",
			Name: "Account creation flow",
		},
		// Run on every api/account.created event.
		inngestgo.EventTrigger("api/account.created", nil),
		testFn,
	)
	h.Register(f)

	err := h.Connect(context.Background())
	if err != nil {
		fmt.Println("Error connecting to Inngest", err)
		return
	}
}
