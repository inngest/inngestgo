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
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gowebpki/jcs"
	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngest/pkg/execution/state"
	"github.com/inngest/inngest/pkg/inngest"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/step"
	"github.com/stretchr/testify/require"
)

func init() {
	os.Setenv("INNGEST_SIGNING_KEY", string(testKey))
	os.Setenv("INNGEST_SIGNING_KEY_FALLBACK", string(testKeyFallback))
}

type EventA struct {
	Name string `json:"name"`
	Data struct {
		Foo string `json:"foo"`
		Bar string `json:"bar"`
	} `json:"data"`
}

type EventB struct{}
type EventC struct{}

func TestRegister(t *testing.T) {
	a := CreateFunction(
		FunctionOpts{
			Name: "my func name",
		},
		EventTrigger("test/event.a", nil),
		func(ctx context.Context, input Input[EventA]) (any, error) {
			return nil, nil
		},
	)
	b := CreateFunction(
		FunctionOpts{Name: "another func"},
		EventTrigger("test/event.b", nil),
		func(ctx context.Context, input Input[EventB]) (any, error) {
			return nil, nil
		},
	)
	c := CreateFunction(
		FunctionOpts{Name: "batch func", BatchEvents: &inngest.EventBatchConfig{MaxSize: 20, Timeout: "10s"}},
		EventTrigger("test/batch.a", nil),
		func(ctx context.Context, input Input[EventC]) (any, error) {
			return nil, nil
		},
	)

	Register(a, b, c)
}

// TestInvoke asserts that invoking a function with both the correct and incorrect type
// works as expected.
func TestInvoke(t *testing.T) {

	t.Run("With a struct value event type", func(t *testing.T) {
		ctx := context.Background()
		input := EventA{
			Name: "test/event.a",
			Data: struct {
				Foo string `json:"foo"`
				Bar string `json:"bar"`
			}{
				Foo: "potato",
				Bar: "squished",
			},
		}
		resp := map[string]any{
			"test": true,
		}
		a := CreateFunction(
			FunctionOpts{Name: "my func name"},
			EventTrigger("test/event.a", nil),
			func(ctx context.Context, event Input[EventA]) (any, error) {
				require.EqualValues(t, event.Event, input)
				return resp, nil
			},
		)
		Register(a)

		t.Run("it invokes the function with correct types", func(t *testing.T) {
			actual, op, err := invoke(ctx, a, createRequest(t, input))
			require.NoError(t, err)
			require.Nil(t, op)
			require.Equal(t, resp, actual)
		})
	})

	t.Run("With a struct value event type batch", func(t *testing.T) {
		ctx := context.Background()
		input := EventA{
			Name: "test/event.a",
			Data: struct {
				Foo string `json:"foo"`
				Bar string `json:"bar"`
			}{
				Foo: "potato",
				Bar: "squished",
			},
		}
		resp := map[string]any{
			"test": true,
		}
		a := CreateFunction(
			FunctionOpts{Name: "my func name", BatchEvents: &inngest.EventBatchConfig{MaxSize: 5, Timeout: "10s"}},
			EventTrigger("test/event.a", nil),
			func(ctx context.Context, event Input[EventA]) (any, error) {
				require.EqualValues(t, event.Event, input)
				require.EqualValues(t, len(event.Events), 5)
				return resp, nil
			},
		)
		Register(a)

		t.Run("it invokes the function with correct types", func(t *testing.T) {
			actual, op, err := invoke(ctx, a, createBatchRequest(t, input, 5))
			require.NoError(t, err)
			require.Nil(t, op)
			require.Equal(t, resp, actual)
		})
	})

	t.Run("With a struct ptr event type", func(t *testing.T) {
		input := EventA{
			Name: "test/event.a",
			Data: struct {
				Foo string `json:"foo"`
				Bar string `json:"bar"`
			}{
				Foo: "potato",
				Bar: "squished",
			},
		}
		resp := map[string]any{
			"test": true,
		}
		a := CreateFunction(
			FunctionOpts{Name: "my func name"},
			EventTrigger("test/event.a", nil),
			func(ctx context.Context, event Input[*EventA]) (any, error) {
				require.NotNil(t, event.Event)
				require.EqualValues(t, *event.Event, input)
				return resp, nil
			},
		)
		Register(a)

		ctx := context.Background()

		t.Run("it invokes the function with correct types", func(t *testing.T) {
			actual, op, err := invoke(ctx, a, createRequest(t, input))
			require.NoError(t, err)
			require.Nil(t, op)
			require.Equal(t, resp, actual)
		})
	})

	t.Run("With Input[any] as a function type", func(t *testing.T) {
		resp := map[string]any{"test": true}
		input := EventA{
			Name: "test/event.a",
			Data: struct {
				Foo string `json:"foo"`
				Bar string `json:"bar"`
			}{
				Foo: "potato",
				Bar: "squished",
			},
		}
		a := CreateFunction(
			FunctionOpts{Name: "my func name"},
			EventTrigger("test/event.a", nil),
			func(ctx context.Context, event Input[any]) (any, error) {
				require.NotNil(t, event.Event)
				val, ok := event.Event.(map[string]any)
				require.True(t, ok)
				require.EqualValues(t, input.Name, val["name"])
				val, ok = val["data"].(map[string]any)
				require.True(t, ok)
				require.EqualValues(t, input.Data.Foo, val["foo"])
				require.EqualValues(t, input.Data.Bar, val["bar"])
				return resp, nil
			},
		)
		Register(a)

		ctx := context.Background()
		t.Run("it invokes the function with correct types", func(t *testing.T) {
			actual, op, err := invoke(ctx, a, createRequest(t, input))
			require.NoError(t, err)
			require.Nil(t, op)
			require.Equal(t, resp, actual)
		})
	})

	t.Run("With Input[map[string]any] as a function type", func(t *testing.T) {
		resp := map[string]any{"test": true}
		input := EventA{
			Name: "test/event.a",
			Data: struct {
				Foo string `json:"foo"`
				Bar string `json:"bar"`
			}{
				Foo: "potato",
				Bar: "squished",
			},
		}
		a := CreateFunction(
			FunctionOpts{Name: "my func name"},
			EventTrigger("test/event.a", nil),
			func(ctx context.Context, event Input[map[string]any]) (any, error) {
				require.NotNil(t, event.Event)
				val := event.Event
				require.EqualValues(t, input.Name, val["name"])
				val, ok := val["data"].(map[string]any)
				require.True(t, ok)
				require.EqualValues(t, input.Data.Foo, val["foo"])
				require.EqualValues(t, input.Data.Bar, val["bar"])
				return resp, nil
			},
		)
		Register(a)

		ctx := context.Background()
		t.Run("it invokes the function with correct types", func(t *testing.T) {
			actual, op, err := invoke(ctx, a, createRequest(t, input))
			require.NoError(t, err)
			require.Nil(t, op)
			require.Equal(t, resp, actual)
		})
	})

	// This is silly and no one should ever do this.  The tests are here
	// so that we ensure the code panics on creation.
	t.Run("With an io.Reader as a function type", func(t *testing.T) {
		require.Panics(t, func() {
			// Creating a function with an interface is impossible.  This can
			// never go into production, and you should always be testing this
			// before deploying to Inngest.
			CreateFunction(
				FunctionOpts{Name: "my func name"},
				EventTrigger("test/event.a", nil),
				func(ctx context.Context, event Input[io.Reader]) (any, error) {
					return nil, nil
				},
			)
		})
	})
}

func TestServe(t *testing.T) {
	event := EventA{
		Name: "test/event.a",
		Data: struct {
			Foo string `json:"foo"`
			Bar string `json:"bar"`
		}{
			Foo: "potato",
			Bar: "squished",
		},
	}

	result := map[string]any{"result": true}

	var called int32
	a := CreateFunction(
		FunctionOpts{Name: "My servable function!"},
		EventTrigger("test/event.a", nil),
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

		url := fmt.Sprintf("%s?%s", server.URL, queryParams.Encode())
		resp := handlerPost(t, url, createRequest(t, event))

		defer resp.Body.Close()
		require.Equal(t, int32(1), atomic.LoadInt32(&called), "http function was not called")

		// Assert that the output is correct.
		byt, _ = io.ReadAll(resp.Body)
		actual := map[string]any{}
		err := json.Unmarshal(byt, &actual)
		require.NoError(t, err)
		require.Equal(t, result, actual)
	})

	t.Run("It doesn't call the function with an incorrect function ID", func(t *testing.T) {
		queryParams := url.Values{}
		queryParams.Add("fnId", "lol")

		url := fmt.Sprintf("%s?%s", server.URL, queryParams.Encode())
		resp := handlerPost(t, url, createRequest(t, event))

		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 410, resp.StatusCode)
	})
}

func TestSteps(t *testing.T) {
	event := EventA{
		Name: "test/event.a",
		Data: struct {
			Foo string `json:"foo"`
			Bar string `json:"bar"`
		}{
			Foo: "potato",
			Bar: "squished",
		},
	}

	var fnCt, aCt, bCt int32

	a := CreateFunction(
		FunctionOpts{Name: "step function"},
		EventTrigger("test/event.a", nil),
		func(ctx context.Context, input Input[EventA]) (any, error) {
			atomic.AddInt32(&fnCt, 1)
			stepA, _ := step.Run(ctx, "First step", func(ctx context.Context) (map[string]any, error) {
				atomic.AddInt32(&aCt, 1)
				return map[string]any{
					"test": true,
					"foo":  input.Event.Data.Foo,
				}, nil
			})
			stepB, _ := step.Run(ctx, "Second step", func(ctx context.Context) (map[string]any, error) {
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
		resp := handlerPost(t, url, createRequest(t, event))
		defer resp.Body.Close()

		// This should return an opcode indicating that the first step ran as expected.
		byt, _ := io.ReadAll(resp.Body)

		var (
			opcode state.GeneratorOpcode
			stepA  map[string]any
		)

		t.Run("The first step.Run opcodes are correct", func(t *testing.T) {
			opcodes := []state.GeneratorOpcode{}
			err := json.Unmarshal(byt, &opcodes)
			require.NoError(t, err, string(byt))

			require.Len(t, opcodes, 1)
			opcode = opcodes[0]

			require.Equal(t, enums.OpcodeStepRun, opcode.Op, "tools.Run didn't return the correct opcode")
			require.Equal(t, "First step", opcode.Name, "tools.Run didn't return the correct opcode")

			require.EqualValues(t, 1, fnCt)
			require.EqualValues(t, 1, aCt)
			require.EqualValues(t, 0, bCt)

			// Assert the opcode data is as expected
			stepA = map[string]any{}
			err = json.Unmarshal(opcode.Data, &stepA)
			require.NoError(t, err)
			require.EqualValues(t, map[string]any{
				"test": true, "foo": "potato",
			}, stepA)
		})

		t.Run("It invokes the second step if the first step's data is passed in", func(t *testing.T) {
			req := createRequest(t, event)
			req.Steps = map[string]json.RawMessage{
				opcode.ID: opcode.Data,
			}
			resp := handlerPost(t, url, req)
			defer resp.Body.Close()

			// The response should be a new opcode.
			opcodes := []state.GeneratorOpcode{}
			err := json.NewDecoder(resp.Body).Decode(&opcodes)
			require.NoError(t, err)

			require.Len(t, opcodes, 1)
			opcode = opcodes[0]

			require.Equal(t, enums.OpcodeStepRun, opcode.Op, "tools.Run didn't return the correct opcode")
			require.Equal(t, "Second step", opcode.Name, "tools.Run didn't return the correct opcode")

			require.EqualValues(t, 2, fnCt)
			require.EqualValues(t, 1, aCt)
			require.EqualValues(t, 1, bCt)

			// Assert the opcode data is as expected
			stepB := map[string]any{}
			err = json.Unmarshal(opcode.Data, &stepB)
			require.NoError(t, err)
			require.EqualValues(t, map[string]any{
				// data is wrapped in an object to conform to the spec.
				"b": "lol",
				"a": stepA,
			}, stepB)

		})
	})

}

func TestIntrospection(t *testing.T) {
	fn := CreateFunction(
		FunctionOpts{Name: "My servable function!"},
		EventTrigger("test/event.a", nil),
		func(ctx context.Context, input Input[any]) (any, error) {
			return nil, nil
		},
	)
	h := NewHandler("introspection", HandlerOpts{})
	h.Register(fn)
	server := httptest.NewServer(h)
	defer server.Close()

	t.Run("no signature", func(t *testing.T) {
		// When the request has no signature, respond with the insecure
		// introspection body

		r := require.New(t)

		reqBody := []byte("")
		req, err := http.NewRequest(http.MethodGet, server.URL, bytes.NewReader(reqBody))
		r.NoError(err)
		resp, err := http.DefaultClient.Do(req)
		r.Equal(http.StatusOK, resp.StatusCode)
		r.NoError(err)

		var respBody map[string]any
		err = json.NewDecoder(resp.Body).Decode(&respBody)
		r.NoError(err)

		r.Equal(map[string]any{
			"function_count":  float64(1),
			"has_event_key":   false,
			"has_signing_key": true,
			"mode":            "cloud",
		}, respBody)
	})

	t.Run("valid signature", func(t *testing.T) {
		// When the request has a valid signature, respond with the secure
		// introspection body

		r := require.New(t)

		reqBody := []byte("")
		sig := Sign(context.Background(), time.Now(), []byte(testKey), reqBody)
		req, err := http.NewRequest(http.MethodGet, server.URL, bytes.NewReader(reqBody))
		r.NoError(err)
		req.Header.Set("X-Inngest-Signature", sig)
		resp, err := http.DefaultClient.Do(req)
		r.Equal(http.StatusOK, resp.StatusCode)
		r.NoError(err)

		var respBody map[string]any
		err = json.NewDecoder(resp.Body).Decode(&respBody)
		r.NoError(err)

		signingKeyHash, err := hashedSigningKey([]byte(testKey))
		r.NoError(err)
		signingKeyFallbackHash, err := hashedSigningKey([]byte(testKeyFallback))
		r.NoError(err)
		r.Equal(map[string]any{
			"function_count":            float64(1),
			"has_event_key":             false,
			"has_signing_key":           true,
			"mode":                      "cloud",
			"signing_key_fallback_hash": string(signingKeyFallbackHash),
			"signing_key_hash":          string(signingKeyHash),
		}, respBody)
	})

	t.Run("invalid signature", func(t *testing.T) {
		// When the request has an invalid signature, respond with the insecure
		// introspection body

		r := require.New(t)

		reqBody := []byte("")
		invalidKey := "deadbeef"
		sig := Sign(context.Background(), time.Now(), []byte(invalidKey), reqBody)
		req, err := http.NewRequest(http.MethodGet, server.URL, bytes.NewReader(reqBody))
		r.NoError(err)
		req.Header.Set("X-Inngest-Signature", sig)
		resp, err := http.DefaultClient.Do(req)
		r.Equal(http.StatusOK, resp.StatusCode)
		r.NoError(err)

		var respBody map[string]any
		err = json.NewDecoder(resp.Body).Decode(&respBody)
		r.NoError(err)

		r.Equal(map[string]any{
			"function_count":  float64(1),
			"has_event_key":   false,
			"has_signing_key": true,
			"mode":            "cloud",
		}, respBody)
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

func createBatchRequest(t *testing.T, evt any, num int) *sdkrequest.Request {
	t.Helper()

	events := make([]json.RawMessage, num)
	for i := 0; i < num; i++ {
		byt, err := json.Marshal(evt)
		require.NoError(t, err)
		events[i] = byt
	}

	return &sdkrequest.Request{
		Event:  events[0],
		Events: events,
		CallCtx: sdkrequest.CallCtx{
			FunctionID: "fn-id",
			RunID:      "run-id",
		},
	}
}

func handlerPost(t *testing.T, url string, r *sdkrequest.Request) *http.Response {
	t.Helper()

	body := marshalRequest(t, r)
	sig := Sign(context.Background(), time.Now(), []byte(testKey), body)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Inngest-Signature", sig)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func marshalRequest(t *testing.T, r *sdkrequest.Request) []byte {
	t.Helper()

	byt, err := json.Marshal(r)
	require.NoError(t, err)
	byt, err = jcs.Transform(byt)
	require.NoError(t, err)
	return byt
}
