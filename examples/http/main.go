package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/errors"
	"github.com/inngest/inngestgo/pkg/checkpoint"
	"github.com/inngest/inngestgo/step"
)

func main() {
	c, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID: "billing",
	})
	if err != nil {
		panic(err)
	}

	// CreateFunction is a factory method which creates new Inngest functions (step functions,
	// or workflows) with a specific configuration.
	_, err = inngestgo.CreateFunction(
		c,
		inngestgo.FunctionOpts{
			ID:         "account-created",
			Name:       "Account creation flow",
			Checkpoint: checkpoint.ConfigSafe,
			Retries:    inngestgo.IntPtr(5),
		},
		// Run on every api/account.created event.
		inngestgo.EventTrigger("api/account.created", nil),
		// The function to run.
		AccountCreated,
	)
	if err != nil {
		panic(err)
	}

	// And serve the functions from an HTTP handler.
	fmt.Print("listening on :8080")
	_ = http.ListenAndServe(":8080", c.Serve())
}

// AccountCreated is a durable function which runs any time the "api/account.created"
// event is received by Inngest.
//
// It is invoked by Inngest, with each step being backed by Inngest's orchestrator.
// Function state is automatically managed, and persists across server restarts,
// cloud migrations, and language changes.
func AccountCreated(ctx context.Context, input inngestgo.Input[AccountCreatedEventData]) (any, error) {
	// Sleep for a second, minute, hour, week across server restarts.
	step.Sleep(ctx, "initial-delay", time.Second)

	// Run a step which emails the user.  This automatically retries on error.
	// This returns the fully typed result of the lambda.
	//
	// Each step.Run is a code-level transaction that commits its results to the function's
	// state.
	result, err := step.Run(ctx, "fetch todo", func(ctx context.Context) (*TodoItem, error) {
		// Run any code inside a step, eg:
		resp, err := http.Get("https://jsonplaceholder.typicode.com/todos/1")
		if err != nil {
			// This will retry automatically according to the function's Retry count.
			return nil, err
		}
		if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); !retryAfter.IsZero() {
			// Return a RetryAtError to manually control when to retry the step on transient
			// errors such as rate limits.
			return nil, errors.RetryAtError(fmt.Errorf("rate-limited"), retryAfter)
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		item := &TodoItem{}
		err = json.NewDecoder(resp.Body).Decode(item)
		return item, err
	})
	if result == nil || err != nil {
		return nil, err
	}

	// You can access the previous step.Run's return values as expected.
	_, _ = step.Run(ctx, "load todo author", func(ctx context.Context) (any, error) {
		return loadUser(result.UserID)
	})

	// Sample from the event stream for new events.  The function will stop
	// running and automatially resume when a matching event is found, or if
	// the timeout is reached.
	fn, err := step.WaitForEvent[FunctionCreatedEvent](
		ctx,
		"wait-for-activity",
		step.WaitForEventOpts{
			Name:    "Wait for a function to be created",
			Event:   "api/function.created",
			Timeout: time.Second,
			// Match events where the user_id is the same in the async sampled event.
			If: inngestgo.StrPtr("event.data.user_id == async.data.user_id"),
		},
	)
	if err == step.ErrEventNotReceived {
		// A function wasn't created within 3 days.  Send a follow-up email.
		_, _ = step.Run(ctx, "follow-up-email", func(ctx context.Context) (any, error) {
			// ...
			return true, nil
		})
		return nil, nil
	}

	// The event returned from `step.WaitForEvent` is fully typed.
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
type (
	AccountCreatedEvent     inngestgo.GenericEvent[AccountCreatedEventData]
	AccountCreatedEventData struct {
		AccountID string
	}
)

type (
	FunctionCreatedEvent     inngestgo.GenericEvent[FunctionCreatedEventData]
	FunctionCreatedEventData struct {
		FunctionID string
	}
)

//
// Utility helpers
//

type TodoItem struct {
	ID        int    `json:"id"`
	UserID    int    `json:"userId"`
	Title     string `json:"title"`
	Completed bool   `json:"bool"`
}

func parseRetryAfter(input string) time.Time {
	// We must parse this according to RFC9110 / RFC5322.
	fmts := []string{
		time.RFC1123, time.RFC850, time.ANSIC, // In spec
		time.RFC3339, // Not part of the spec
	}
	for _, format := range fmts {
		if t, err := time.Parse(format, input); err == nil {
			return t
		}
	}
	// Attempt to parse this as delay in seconds
	if delay, err := strconv.Atoi(input); err != nil {
		return time.Now().Add(time.Duration(delay) * time.Second)
	}
	// Return zero time
	return time.Time{}
}

func loadUser(id int) (any, error) {
	// eg. fetch from DB
	return nil, nil
}
