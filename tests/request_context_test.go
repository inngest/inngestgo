package tests

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestContext(t *testing.T) {
	// Request context values are accessible within Inngest functions.

	devEnv(t)
	type contextKeyType struct{}
	contextKey := contextKeyType{}

	// Middleware that adds a value to the request context.
	withValue := func(value interface{}) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(
					w,
					r.WithContext(context.WithValue(r.Context(), contextKey, value)),
				)
			})
		}
	}

	ctx := context.Background()
	r := require.New(t)

	appName := randomSuffix("app")
	c, err := inngestgo.NewClient(inngestgo.ClientOpts{AppID: appName})
	r.NoError(err)

	var runID string
	var ctxValue string
	eventName := randomSuffix("event")
	_, err = inngestgo.CreateFunction(
		c,
		inngestgo.FunctionOpts{
			ID:      "parent-fn",
			Retries: inngestgo.IntPtr(0),
		},
		inngestgo.EventTrigger(eventName, nil),
		func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
			runID = input.InputCtx.RunID
			ctxValue, _ = ctx.Value(contextKey).(string)
			return nil, nil
		},
	)
	r.NoError(err)

	server, sync := serve(t, c, serveOpts{
		Middleware: withValue("hello"),
	})
	defer server.Close()
	r.NoError(sync())

	_, err = c.Send(ctx, inngestgo.Event{Name: eventName})
	r.NoError(err)

	var run *Run
	r.EventuallyWithT(func(ct *assert.CollectT) {
		a := assert.New(ct)
		run, err = getRun(runID)
		if !a.NoError(err) {
			return
		}
		a.Equal(enums.RunStatusCompleted.String(), run.Status)
		a.Equal("hello", ctxValue)
	}, 5*time.Second, time.Second)
}
