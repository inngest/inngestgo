package inngestgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/execution/state"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/require"
)

type EventA struct {
	Name string
	Data struct {
		Foo string
		Bar string
	}
}

type EventB struct{}

func TestRegister(t *testing.T) {
	a := CreateFunction(
		FunctionOpts{
			Name: "my func name",
		},
		Event("test/event.a"),
		func(ctx context.Context, input Input[EventA]) (any, error) {
			return nil, nil
		},
	)
	b := CreateFunction(
		FunctionOpts{Name: "another func"},
		Event("test/event.b"),
		func(ctx context.Context, input Input[EventB]) (any, error) {
			return nil, nil
		},
	)
	Register(a, b)
}

// TestInvoke asserts that invoking a function with both the correct and incorrect type
// works as expected.
func TestInvoke(t *testing.T) {
	resp := map[string]any{
		"test": true,
	}
	a := CreateFunction(
		FunctionOpts{Name: "my func name"},
		Event("test/event.a"),
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

func TestServe(t *testing.T) {
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

	result := map[string]any{"result": true}

	var called int32
	a := CreateFunction(
		FunctionOpts{Name: "My servable function!"},
		Event("test/event.a"),
		func(ctx context.Context, input Input[EventA]) (any, error) {
			atomic.AddInt32(&called, 1)
			require.EqualValues(t, event, input.Event)
			return result, nil
		},
	)
	Register(a)
	server := httptest.NewServer(DefaultHandler)
	byt, err := json.Marshal(map[string]any{
		"event": event,
		"ctx": map[string]any{
			"fn_id":  "fn-id",
			"run_id": "run-id",
		},
	})
	require.NoError(t, err)

	t.Run("It calls the correct function with the correct data", func(t *testing.T) {
		queryParams := url.Values{}
		queryParams.Add("fnId", a.Slug())

		resp, err := http.Post(
			fmt.Sprintf("%s?%s", server.URL, queryParams.Encode()),
			"application/json",
			bytes.NewReader(byt),
		)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, int32(1), atomic.LoadInt32(&called), "http function was not called")

		// Assert that the output is correct.
		byt, _ = io.ReadAll(resp.Body)
		actual := map[string]any{}
		err = json.Unmarshal(byt, &actual)
		require.NoError(t, err)
		require.Equal(t, result, actual)
	})

	t.Run("It doesn't call the function with an incorrect function ID", func(t *testing.T) {
		queryParams := url.Values{}
		queryParams.Add("fnId", "lol")

		resp, err := http.Post(
			fmt.Sprintf("%s?%s", server.URL, queryParams.Encode()),
			"application/json",
			bytes.NewReader(byt),
		)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 410, resp.StatusCode)
	})
}

func TestSteps(t *testing.T) {
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

	var fnCt, aCt, bCt int32

	a := CreateFunction(
		FunctionOpts{Name: "step function"},
		Event("test/event.a"),
		func(ctx context.Context, input Input[EventA]) (any, error) {
			atomic.AddInt32(&fnCt, 1)
			stepA := step.Run(ctx, "First step", func(ctx context.Context) (map[string]any, error) {
				atomic.AddInt32(&aCt, 1)
				return map[string]any{
					"test": true,
					"foo":  input.Event.Data.Foo,
				}, nil
			})
			stepB := step.Run(ctx, "Second step", func(ctx context.Context) (map[string]any, error) {
				atomic.AddInt32(&bCt, 1)
				return map[string]any{
					"b": "lol",
					"a": stepA,
				}, nil
			})
			return stepB, nil
		},
	)
	Register(a)
	server := httptest.NewServer(DefaultHandler)
	queryParams := url.Values{}
	queryParams.Add("fnId", a.Slug())
	url := fmt.Sprintf("%s?%s", server.URL, queryParams.Encode())

	t.Run("It invokes the first step and returns an opcode", func(t *testing.T) {
		resp, err := http.Post(url, "application/json", createRequestReader(t, createRequest(t, event)))
		require.NoError(t, err)
		defer resp.Body.Close()

		// This should return an opcode indicating that the first step ran as expected.
		byt, _ := io.ReadAll(resp.Body)

		var (
			opcode state.GeneratorOpcode
			stepA  map[string]any
		)

		t.Run("The first step.Run opcodes are correct", func(t *testing.T) {
			opcodes := []state.GeneratorOpcode{}
			err = json.Unmarshal(byt, &opcodes)
			require.NoError(t, err, string(byt))

			require.Len(t, opcodes, 1)
			opcode = opcodes[0]

			require.Equal(t, enums.OpcodeStep, opcode.Op, "tools.Run didn't return the correct opcode")
			require.Equal(t, "First step", opcode.Name, "tools.Run didn't return the correct opcode")

			require.EqualValues(t, 1, fnCt)
			require.EqualValues(t, 1, aCt)
			require.EqualValues(t, 0, bCt)

			// Assert the opcode data is as expected
			stepA = map[string]any{}
			err = json.Unmarshal(opcode.Data, &stepA)
			require.NoError(t, err)
			require.EqualValues(t, map[string]any{"test": true, "foo": "potato"}, stepA)
		})

		t.Run("It invokes the second step if the first step's data is passed in", func(t *testing.T) {
			req := createRequest(t, event)
			req.Steps = map[string]json.RawMessage{
				opcode.ID: opcode.Data,
			}
			resp, err := http.Post(url, "application/json", createRequestReader(t, req))
			require.NoError(t, err)
			defer resp.Body.Close()

			// The response should be a new opcode.
			opcodes := []state.GeneratorOpcode{}
			err = json.NewDecoder(resp.Body).Decode(&opcodes)
			require.NoError(t, err)

			require.Len(t, opcodes, 1)
			opcode = opcodes[0]

			require.Equal(t, enums.OpcodeStep, opcode.Op, "tools.Run didn't return the correct opcode")
			require.Equal(t, "Second step", opcode.Name, "tools.Run didn't return the correct opcode")

			require.EqualValues(t, 2, fnCt)
			require.EqualValues(t, 1, aCt)
			require.EqualValues(t, 1, bCt)

			// Assert the opcode data is as expected
			stepB := map[string]any{}
			err = json.Unmarshal(opcode.Data, &stepB)
			require.NoError(t, err)
			require.EqualValues(t, map[string]any{
				"b": "lol",
				"a": stepA,
			}, stepB)

		})
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

func createRequestReader(t *testing.T, r *sdkrequest.Request) io.Reader {
	t.Helper()
	byt, err := json.Marshal(r)
	require.NoError(t, err)
	return bytes.NewReader(byt)
}
