package inngestgo

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/stretchr/testify/require"
)

type EventA struct {
	Name string
	Data struct {
		Foo string
		Bar string
	}
}

// TestInvoke asserts that invoking a function with both the correct and incorrect type
// works as expected.
func TestInvoke(t *testing.T) {
	resp := map[string]any{
		"test": true,
	}
	a := CreateFunction(
		FunctionOpts{Name: "my func name"},
		EventTrigger("test/event.a"),
		func(ctx context.Context, event Input[EventA]) (any, error) {
			return resp, nil
		},
	)
	Register(a)

	ctx := context.Background()
	event := EventA{
		Name: "test/event.a",
		Data: struct {
			Foo string
			Bar string
		}{
			Foo: "potato",
			Bar: "squished",
		},
	}

	t.Run("it invokes the function with correct types", func(t *testing.T) {
		actual, op, err := invoke(ctx, a, createRequest(t, event))
		require.NoError(t, err)
		require.Nil(t, op)
		require.Equal(t, resp, actual)
	})
}

func createRequest(t *testing.T, evt any) *sdkrequest.Request {
	t.Helper()

	byt, err := json.Marshal(evt)
	require.NoError(t, err)

	return &sdkrequest.Request{
		Event: byt,
		CallCtx: sdkrequest.CallCtx{
			FunctionID: "fn-id",
			RunID:      "run-id",
		},
	}
}
