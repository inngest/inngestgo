package inngestgo

import (
	"context"
	"fmt"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func TestConnectEstablish(t *testing.T) {
	os.Setenv("INNGEST_DEV", "1")

	type AccountCreatedEventData struct {
		AccountID string
	}
	type AccountCreatedEvent GenericEvent[AccountCreatedEventData, any]

	// AccountCreated is a durable function which runs any time the "api/account.created"
	// event is received by Inngest.
	//
	// It is invoked by Inngest, with each step being backed by Inngest's orchestrator.
	// Function state is automatically managed, and persists across server restarts,
	// cloud migrations, and language changes.
	testFn := func(ctx context.Context, input Input[AccountCreatedEvent]) (any, error) {
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

	h := NewHandler("core", HandlerOpts{})
	f := CreateFunction(
		FunctionOpts{
			ID:   "account-created",
			Name: "Account creation flow",
		},
		// Run on every api/account.created event.
		EventTrigger("api/account.created", nil),
		testFn,
	)
	h.Register(f)

	err := h.Connect(context.Background())
	require.NoError(t, err)
}
