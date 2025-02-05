package tests

import (
	"context"
	"fmt"
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
		c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
		r.NoError(err)
		h := inngestgo.NewHandler(c, inngestgo.HandlerOpts{})

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
				input inngestgo.Input[inngestgo.GenericEvent[ChildEventData, any]],
			) (any, error) {
				return input.Event.Data.Message, nil
			},
		)
		r.NoError(err)

		var runID string
		var invokeResult any
		var invokeErr error
		eventName := randomSuffix("my-event")
		parentFn, err := inngestgo.CreateFunction(
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
						FunctionId: fmt.Sprintf("%s-%s", appName, childFnName),
					},
				)
				return invokeResult, invokeErr
			},
		)
		r.NoError(err)

		h.Register(childFn, parentFn)

		server, sync := serve(t, h)
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
		h := inngestgo.NewHandler(c, inngestgo.HandlerOpts{})

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
		parentFn, err := inngestgo.CreateFunction(
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
						FunctionId: fmt.Sprintf("%s-%s", appName, childFnName),
					},
				)
				return invokeResult, invokeErr
			},
		)
		r.NoError(err)
		h.Register(childFn, parentFn)

		server, sync := serve(t, h)
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
		h := inngestgo.NewHandler(c, inngestgo.HandlerOpts{})

		var runID string
		var invokeResult any
		var invokeErr error
		eventName := randomSuffix("my-event")
		fn, err := inngestgo.CreateFunction(
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

		h.Register(fn)

		server, sync := serve(t, h)
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
}
