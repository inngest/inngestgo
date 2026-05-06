package inngestgo

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStreamingInvoke_ResponseWriterRace exercises the streaming code path
// in handler.go. The race is within a single request: the keepalive
// goroutine and the main handler goroutine both write to
// http.ResponseWriter, which isn't safe for concurrent use.
//
// Goal: overlap the 5-second keepalive tick with the JSON-encoding write
// of the response. The function deliberately sleeps 5.2s so the keepalive
// goroutine is mid-Write when the response Encoder runs.
//
// On the patched code, all writes happen on the handler goroutine; the
// test passes clean under -race.
//
// The test also asserts the function is invoked exactly once. This catches
// regressions where the streaming and non-streaming branches both run, as
// happened in an early iteration of the fix.
//
// This test does not cover the connect/connection.go fix. That needs its
// own test if direct regression coverage is wanted.
func TestStreamingInvoke_ResponseWriterRace(t *testing.T) {
	setEnvVars(t)
	r := require.New(t)

	c, err := NewClient(ClientOpts{
		AppID:        "streaming-race-repro",
		UseStreaming: true,
	})
	r.NoError(err)

	event := EventA{
		Name: "test/event.a",
		Data: EventAData{Foo: "x", Bar: "y"},
	}
	result := map[string]any{"result": true}

	var calls atomic.Int32

	fn, err := CreateFunction(
		c,
		FunctionOpts{ID: "race-repro"},
		EventTrigger("test/event.a", nil),
		func(ctx context.Context, input Input[EventAData]) (any, error) {
			calls.Add(1)
			time.Sleep(5200 * time.Millisecond)
			return result, nil
		},
	)
	r.NoError(err)

	server := httptest.NewServer(c.Serve())
	defer server.Close()

	q := url.Values{}
	q.Add("fnId", fn.FullyQualifiedID())
	urlStr := fmt.Sprintf("%s?%s", server.URL, q.Encode())

	resp := handlerPost(t, urlStr, createRequest(t, event))
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)

	r.Equal(int32(1), calls.Load(), "function should be invoked exactly once")
}
