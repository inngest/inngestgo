package tests

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/connect/serverless"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerlessRun serves an app through connect/serverless against the dev
// server.  the worker syncs the app over connect, serves runs that the dev
// server sends over the gateway, and returns once it is idle.
func TestServerlessRun(t *testing.T) {
	r := require.New(t)
	devEnv(t)
	// a Go app answers an in-band sync only when it has a signing key, even
	// in dev mode.
	signingKey := "signkey-test-12345678"
	t.Setenv("INNGEST_SIGNING_KEY", signingKey)

	appID := randomSuffix("serverless-")
	c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appID})
	r.NoError(err)

	var echoRunID, stepsRunID TypedAtomic[string]
	_, err = inngestgo.CreateFunction(
		c,
		inngestgo.FunctionOpts{ID: "echo"},
		inngestgo.EventTrigger(appID+"/echo", nil),
		func(ctx context.Context, input inngestgo.Input[map[string]any]) (any, error) {
			echoRunID.Store(input.InputCtx.RunID)
			return input.Event.Data, nil
		},
	)
	r.NoError(err)
	_, err = inngestgo.CreateFunction(
		c,
		inngestgo.FunctionOpts{ID: "steps"},
		inngestgo.EventTrigger(appID+"/steps", nil),
		func(ctx context.Context, input inngestgo.Input[map[string]any]) (any, error) {
			stepsRunID.Store(input.InputCtx.RunID)
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
	r.NoError(err)

	server, _ := serve(t, c)
	defer server.Close()

	idleTimeout := 10 * time.Second
	var runErr error
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		runErr = serverless.Run(context.Background(), serverless.Config{
			AppURL:      server.URL + "/api/inngest",
			SigningKey:  signingKey,
			APIBaseURL:  "http://127.0.0.1:8288",
			InstanceID:  appID,
			Dev:         true,
			Multiplex:   2,
			IdleTimeout: idleTimeout,
			Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		})
	}()

	tests := []struct {
		fn         string
		data       map[string]any
		runID      *TypedAtomic[string]
		wantOutput any
	}{
		{fn: "echo", data: map[string]any{"hello": "world"}, runID: &echoRunID, wantOutput: map[string]any{"hello": "world"}},
		{fn: "steps", runID: &stepsRunID, wantOutput: "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.fn, func(t *testing.T) {
			r := require.New(t)

			// the dev server only starts a run once the worker has synced the
			// app, so send again until the function records a run.
			r.EventuallyWithT(func(ct *assert.CollectT) {
				select {
				case <-workerDone:
					ct.Errorf("worker returned early: %v", runErr)
					return
				default:
				}
				if _, ok := tt.runID.Load(); ok {
					return
				}
				_, err := c.Send(context.Background(), inngestgo.Event{Name: appID + "/" + tt.fn, Data: tt.data})
				assert.NoError(ct, err)
				_, ok := tt.runID.Load()
				assert.True(ct, ok, "no run yet")
			}, 30*time.Second, 2*time.Second)

			runID, _ := tt.runID.Load()
			var run *Run
			r.EventuallyWithT(func(ct *assert.CollectT) {
				var err error
				run, err = getRun(runID)
				if assert.NoError(ct, err) {
					assert.Equal(ct, "Completed", run.Status)
				}
			}, 30*time.Second, 500*time.Millisecond)
			r.Equal(tt.wantOutput, run.Output)
		})
	}

	select {
	case <-workerDone:
		r.NoError(runErr)
	case <-time.After(idleTimeout + 15*time.Second):
		t.Fatal("worker did not return after the idle timeout")
	}
}
