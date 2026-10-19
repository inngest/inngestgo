package serverless

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/pkg/checkpoint"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/require"
)

const (
	testSigningKey = "signkey-test-12345678"
	testAppID      = "test-app"
	testFnSlug     = testAppID + "-echo"
	testStepsSlug  = testAppID + "-steps"
)

// newGoApp is a serve() app with two functions.  test-app-echo returns the
// event data.  test-app-steps runs step a, sleeps, runs step b, and returns
// "ab".  it does not checkpoint, so each step ends the request with a 206 and
// the test drives the next request as the executor does.
func newGoApp() (http.Handler, error) {
	return newGoAppWith(false)
}

// newGoAppWith is newGoApp.  with streaming, the app replies 201 at once and
// puts the real reply in the body.
func newGoAppWith(streaming bool) (http.Handler, error) {
	key := testSigningKey
	c, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID:        testAppID,
		SigningKey:   &key,
		Logger:       slog.New(slog.DiscardHandler),
		UseStreaming: streaming,
	})
	if err != nil {
		return nil, err
	}

	_, err = inngestgo.CreateFunction(
		c,
		inngestgo.FunctionOpts{ID: "echo"},
		inngestgo.EventTrigger("test/echo", nil),
		func(ctx context.Context, input inngestgo.Input[map[string]any]) (any, error) {
			return input.Event.Data, nil
		},
	)
	if err != nil {
		return nil, err
	}

	_, err = inngestgo.CreateFunction(
		c,
		inngestgo.FunctionOpts{ID: "steps", Checkpoint: &checkpoint.Config{Disable: true}},
		inngestgo.EventTrigger("test/steps", nil),
		func(ctx context.Context, input inngestgo.Input[map[string]any]) (any, error) {
			a, err := step.Run(ctx, "a", func(ctx context.Context) (string, error) {
				return "a", nil
			})
			if err != nil {
				return nil, err
			}
			step.Sleep(ctx, "nap", time.Second)
			b, err := step.Run(ctx, "b", func(ctx context.Context) (string, error) {
				return "b", nil
			})
			if err != nil {
				return nil, err
			}
			return a + b, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return c.Serve(), nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fnConfig holds the parts of a function config that tests check.
type fnConfig struct {
	ID    string `json:"id"`
	Steps map[string]struct {
		Runtime map[string]any `json:"runtime"`
	} `json:"steps"`
}

// requireConnectStepURLs checks that both test functions have their
// step URL on the connect host.
func requireConnectStepURLs(t *testing.T, fns []fnConfig) {
	t.Helper()
	got := map[string]any{}
	for _, f := range fns {
		got[f.ID] = f.Steps["step"].Runtime["url"]
	}
	require.Equal(t, map[string]any{
		testFnSlug:    "ws://connect?fnId=" + testFnSlug + "&step=step",
		testStepsSlug: "ws://connect?fnId=" + testStepsSlug + "&step=step",
	}, got)
}
