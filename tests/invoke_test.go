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
	if testing.Short() {
		t.Skip()
	}

	t.Run("success", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		appName := randomSuffix("my-app")
		h := inngestgo.NewHandler(appName, inngestgo.HandlerOpts{})

		type ChildEventData struct {
			Message string `json:"message"`
		}

		childFnName := "my-child-fn"
		childFn := inngestgo.CreateFunction(
			inngestgo.FunctionOpts{
				ID:      childFnName,
				Name:    childFnName,
				Retries: inngestgo.IntPtr(0),
			},
			inngestgo.EventTrigger("never", nil),
			func(
				ctx context.Context,
				input inngestgo.Input[inngestgo.GenericEvent[ChildEventData, any]],
			) (any, error) {
				return input.Event.Data.Message, nil
			},
		)

		var runID string
		var invokeResult any
		var invokeErr error
		eventName := randomSuffix("my-event")
		parentFn := inngestgo.CreateFunction(
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
						FunctionId: fmt.Sprintf("%s-%s", appName, childFnName),
					},
				)
				return invokeResult, invokeErr
			},
		)

		h.Register(childFn, parentFn)

		server, sync := serve(t, h)
		defer server.Close()
		r.NoError(sync())

		_, err := inngestgo.Send(ctx, inngestgo.Event{
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
		h := inngestgo.NewHandler(appName, inngestgo.HandlerOpts{})

		childFnName := "my-child-fn"
		childFn := inngestgo.CreateFunction(
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

		var runID string
		var invokeResult any
		var invokeErr error
		eventName := randomSuffix("my-event")
		parentFn := inngestgo.CreateFunction(
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
						FunctionId: fmt.Sprintf("%s-%s", appName, childFnName),
					},
				)
				return invokeResult, invokeErr
			},
		)

		h.Register(childFn, parentFn)

		server, sync := serve(t, h)
		defer server.Close()
		r.NoError(sync())

		_, err := inngestgo.Send(ctx, inngestgo.Event{
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
		h := inngestgo.NewHandler(appName, inngestgo.HandlerOpts{})

		var runID string
		var invokeResult any
		var invokeErr error
		eventName := randomSuffix("my-event")
		fn := inngestgo.CreateFunction(
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

		h.Register(fn)

		server, sync := serve(t, h)
		defer server.Close()
		r.NoError(sync())

		_, err := inngestgo.Send(ctx, inngestgo.Event{
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
		h := inngestgo.NewHandler(appName, inngestgo.HandlerOpts{})

		var childCounter int32
		childFnName := "my-child-fn"
		childFn := inngestgo.CreateFunction(
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

		var runID string
		var invokeErr error
		eventName := randomSuffix("my-event")
		parentFn := inngestgo.CreateFunction(
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

				step.Run(ctx, "a", func(ctx context.Context) (any, error) {
					return nil, nil
				})

				return nil, nil
			},
		)

		h.Register(childFn, parentFn)

		server, sync := serve(t, h)
		defer server.Close()
		r.NoError(sync())

		_, err := inngestgo.Send(ctx, inngestgo.Event{
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
}
