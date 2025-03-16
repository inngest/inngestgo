package tests

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvoke(t *testing.T) {
	devEnv(t)

	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("my-app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		type ChildEventData struct {
			Message string `json:"message"`
		}

		childFnName := "my-child-fn"
		childFn, err := inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      childFnName,
				Name:    childFnName,
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger("never", nil),
			func(
				ctx context.Context,
				input inngestgo.Input[ChildEventData],
			) (any, error) {
				return input.Event.Data.Message, nil
			},
		)
		r.NoError(err)

		var runID string
		var invokeResult any
		var invokeErr error
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "my-parent-fn",
				Name:    "my-parent-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				invokeResult, invokeErr = step.Invoke[any](ctx,
					"invoke",
					step.InvokeOpts{
						Data:       map[string]any{"message": "hello"},
						FunctionId: childFn.FullyQualifiedID(),
					},
				)
				return invokeResult, invokeErr
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{
			Name: eventName,
			Data: map[string]any{"foo": "bar"}},
		)
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)

			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}

			a.Equal(enums.RunStatusCompleted.String(), run.Status)
		}, 5*time.Second, time.Second)

		r.Equal("hello", invokeResult)
		r.NoError(invokeErr)
		r.Equal("hello", run.Output)
	})

	t.Run("child returns error", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("my-app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		childFnName := "my-child-fn"
		childFn, err := inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      childFnName,
				Name:    childFnName,
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger("never", nil),
			func(
				ctx context.Context,
				input inngestgo.Input[any],
			) (any, error) {
				return nil, fmt.Errorf("oh no")
			},
		)
		r.NoError(err)
		var runID string
		var invokeResult any
		var invokeErr error
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "my-parent-fn",
				Name:    "my-parent-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				invokeResult, invokeErr = step.Invoke[any](ctx,
					"invoke",
					step.InvokeOpts{
						FunctionId: childFn.FullyQualifiedID(),
					},
				)
				return invokeResult, invokeErr
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{
			Name: eventName,
			Data: map[string]any{"foo": "bar"}},
		)
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)

			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}

			a.Equal(enums.RunStatusFailed.String(), run.Status)
		}, 5*time.Second, time.Second)

		r.Nil(invokeResult)
		r.Equal("oh no", invokeErr.Error())
		r.Equal(
			map[string]any{
				"message": "oh no",
			},
			run.Output,
		)
	})

	t.Run("non-existent function", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("my-app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var runID string
		var invokeResult any
		var invokeErr error
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "my-fn",
				Name:    "my-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				invokeResult, invokeErr = step.Invoke[any](ctx,
					"invoke",
					step.InvokeOpts{FunctionId: "some-non-existent-fn"},
				)
				return invokeResult, invokeErr
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{
			Name: eventName,
			Data: map[string]any{"foo": "bar"}},
		)
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)

			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}

			a.Equal(enums.RunStatusFailed.String(), run.Status)
		}, 5*time.Second, time.Second)

		r.Equal(
			map[string]any{
				"message": "could not find function with ID: some-non-existent-fn",
			},
			run.Output,
		)

		r.Nil(invokeResult)
		r.Equal("could not find function with ID: some-non-existent-fn", invokeErr.Error())
	})

	t.Run("swallowed error does cause invoke retry", func(t *testing.T) {
		// If we swallow the step.Invoke error and continue, we do not retry the
		// invoke.

		ctx := context.Background()
		r := require.New(t)
		appName := randomSuffix("my-app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var childCounter int32
		childFnName := "my-child-fn"
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      childFnName,
				Name:    childFnName,
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger("never", nil),
			func(
				ctx context.Context,
				input inngestgo.Input[any],
			) (any, error) {
				atomic.AddInt32(&childCounter, 1)
				return nil, fmt.Errorf("oh no")
			},
		)
		r.NoError(err)

		var runID string
		var invokeErr error
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      "my-parent-fn",
				Name:    "my-parent-fn",
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				_, invokeErr = step.Invoke[any](ctx,
					"invoke",
					step.InvokeOpts{
						FunctionId: fmt.Sprintf("%s-%s", appName, childFnName),
					},
				)

				_, _ = step.Run(ctx, "a", func(ctx context.Context) (any, error) {
					return nil, nil
				})

				return nil, nil
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{
			Name: eventName,
			Data: map[string]any{"foo": "bar"}},
		)
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)
			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}
			a.Equal(enums.RunStatusCompleted.String(), run.Status)
		}, 5*time.Second, time.Second)

		r.Error(invokeErr)
		r.Equal(int32(1), childCounter)
	})

	t.Run("failed invoke does not cause parent retry", func(t *testing.T) {
		// Returning a step.Invoke error from a parent function does not cause a
		// retry. Invoke retries are the responsibility of the invoked function.

		ctx := context.Background()
		r := require.New(t)
		appName := randomSuffix("my-app")
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)

		var childCounter int32
		childFnName := "my-child-fn"
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:      childFnName,
				Name:    childFnName,
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger("never", nil),
			func(
				ctx context.Context,
				input inngestgo.Input[any],
			) (any, error) {
				atomic.AddInt32(&childCounter, 1)
				return nil, fmt.Errorf("oh no")
			},
		)
		r.NoError(err)
		var runID string
		var attempt int
		eventName := randomSuffix("my-event")
		_, err = inngestgo.CreateFunction(
			c,
			inngestgo.FunctionOpts{
				ID:   "my-parent-fn",
				Name: "my-parent-fn",

				// Allow a retry because we need to assert that a retry does not
				// happen.
				Retries: inngestgo.IntPtr(1),
			},
			inngestgo.EventTrigger(eventName, nil),
			func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
				runID = input.InputCtx.RunID
				attempt = input.InputCtx.Attempt
				return step.Invoke[any](ctx,
					"invoke",
					step.InvokeOpts{
						FunctionId: fmt.Sprintf("%s-%s", appName, childFnName),
					},
				)
			},
		)
		r.NoError(err)

		server, sync := serve(t, c)
		defer server.Close()
		r.NoError(sync())

		_, err = c.Send(ctx, inngestgo.Event{
			Name: eventName,
			Data: map[string]any{"foo": "bar"}},
		)
		r.NoError(err)

		var run *Run
		r.EventuallyWithT(func(ct *assert.CollectT) {
			a := assert.New(ct)
			run, err = getRun(runID)
			if !a.NoError(err) {
				return
			}
			a.Equal(enums.RunStatusFailed.String(), run.Status)
		}, 5*time.Second, time.Second)

		r.Equal(int32(1), childCounter)

		// A retry never happened because we returned the step.Invoke error.
		r.Equal(0, attempt)
	})
}
