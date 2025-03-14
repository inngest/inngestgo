package tests

import (
	"context"
	"testing"

	"github.com/inngest/inngestgo"
	"github.com/stretchr/testify/require"
)

func TestClientSend(t *testing.T) {
	devEnv(t)

	r := require.New(t)
	c, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID: randomSuffix("app"),
	})
	r.NoError(err)

	t.Run("empty data", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		// Event type with no data.
		id, err := c.Send(ctx, inngestgo.Event{Name: "test"})
		r.NoError(err)
		r.NotEmpty(id)

		// GenericEvent type with no data.
		id, err = c.Send(ctx, inngestgo.GenericEvent[map[string]any, any]{
			Name: "test",
		})
		r.NoError(err)
		r.NotEmpty(id)
	})

	t.Run("struct pointer", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		type MyEventData = struct{}
		id, err := c.Send(ctx, inngestgo.GenericEvent[*MyEventData, any]{
			Name: "test",
			Data: &MyEventData{},
		})
		r.NoError(err)
		r.NotEmpty(id)
	})

	t.Run("invalid data", func(t *testing.T) {
		t.Run("nil pointer", func(t *testing.T) {
			ctx := context.Background()
			r := require.New(t)

			type MyEventData = struct{}
			_, err := c.Send(ctx, inngestgo.GenericEvent[*MyEventData, any]{
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})

		t.Run("slice", func(t *testing.T) {
			ctx := context.Background()
			r := require.New(t)

			_, err = c.Send(ctx, inngestgo.GenericEvent[[]string, any]{
				Data: []string{"foo", "bar"},
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})

		t.Run("bool", func(t *testing.T) {
			ctx := context.Background()
			r := require.New(t)

			_, err = c.Send(ctx, inngestgo.GenericEvent[bool, any]{
				Data: true,
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})

		t.Run("int", func(t *testing.T) {
			ctx := context.Background()
			r := require.New(t)

			_, err = c.Send(ctx, inngestgo.GenericEvent[int, any]{
				Data: 1,
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})

		t.Run("string", func(t *testing.T) {
			ctx := context.Background()
			r := require.New(t)

			_, err = c.Send(ctx, inngestgo.GenericEvent[string, any]{
				Data: "foo",
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})
	})
}

func TestClientSendMany(t *testing.T) {
	devEnv(t)

	t.Run("empty data", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		c, err := inngestgo.NewClient(inngestgo.ClientOpts{
			AppID: randomSuffix("app"),
		})
		r.NoError(err)

		ids, err := c.SendMany(ctx, []any{
			inngestgo.Event{Name: "test"},
			inngestgo.Event{Name: "test"},
		})
		r.NoError(err)
		r.Len(ids, 2)
		for _, id := range ids {
			r.NotEmpty(id)
		}
	})
}
