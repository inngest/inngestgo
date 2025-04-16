package tests

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
	"time"

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
		id, err = c.Send(ctx, inngestgo.GenericEvent[map[string]any]{
			Name: "test",
		})
		r.NoError(err)
		r.NotEmpty(id)
	})

	t.Run("struct pointer", func(t *testing.T) {
		ctx := context.Background()
		r := require.New(t)

		type MyEventData = struct{}
		id, err := c.Send(ctx, inngestgo.GenericEvent[*MyEventData]{
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
			_, err := c.Send(ctx, inngestgo.GenericEvent[*MyEventData]{
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})

		t.Run("slice", func(t *testing.T) {
			ctx := context.Background()
			r := require.New(t)

			_, err = c.Send(ctx, inngestgo.GenericEvent[[]string]{
				Data: []string{"foo", "bar"},
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})

		t.Run("bool", func(t *testing.T) {
			ctx := context.Background()
			r := require.New(t)

			_, err = c.Send(ctx, inngestgo.GenericEvent[bool]{
				Data: true,
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})

		t.Run("int", func(t *testing.T) {
			ctx := context.Background()
			r := require.New(t)

			_, err = c.Send(ctx, inngestgo.GenericEvent[int]{
				Data: 1,
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})

		t.Run("string", func(t *testing.T) {
			ctx := context.Background()
			r := require.New(t)

			_, err = c.Send(ctx, inngestgo.GenericEvent[string]{
				Data: "foo",
				Name: "test",
			})
			r.Error(err)
			r.Contains(err.Error(), "data must be a map or struct")
		})
	})
}

func TestClientSendDevOption(t *testing.T) {
	// Client can send events to Dev Server when using the Dev option instead of
	// the INNGEST_DEV env var

	r := require.New(t)
	ctx := context.Background()

	c, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID: randomSuffix("app"),
		Dev:   inngestgo.Ptr(true),
	})
	r.NoError(err)

	ids, err := c.Send(ctx, inngestgo.Event{Name: "test"})
	r.NoError(err)
	r.NotEmpty(ids)
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

func TestClientSendRetry(t *testing.T) {
	// Resending events with the same idempotency key header results in skipped
	// function runs.

	t.Parallel()
	r := require.New(t)
	ctx := context.Background()

	var proxyCounter int32

	// Create a proxy that mimics a request reaching the Dev Server but the
	// client receives a 500 on the first attempt. This ensures that the Dev
	// Server's event processing logic properly handles the idempotency key
	// header.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&proxyCounter, 1)

		byt, _ := io.ReadAll(r.Body)
		r.Body.Close()

		// Always forward requests.
		req, _ := http.NewRequest(
			r.Method,
			fmt.Sprintf("http://0.0.0.0:8288%s", r.URL.Path),
			// r.Body,
			bytes.NewReader(byt),
		)
		req.Header = r.Header
		resp, _ := http.DefaultClient.Do(req)

		if proxyCounter == 1 {
			// Return a 500 on the first attempt.
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			// Forward the response from the Dev Server.
			for k, v := range resp.Header {
				w.Header()[k] = v
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			resp.Body.Close()
		}
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	r.NoError(err)

	c, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID:    randomSuffix("app"),
		Dev:      inngestgo.Ptr(true),
		EventURL: inngestgo.Ptr(proxyURL.String()),
	})
	r.NoError(err)

	eventName := randomSuffix("event")
	var fnCounter int32
	_, err = inngestgo.CreateFunction(
		c,
		inngestgo.FunctionOpts{ID: "fn"},
		inngestgo.EventTrigger(eventName, nil),
		func(ctx context.Context, input inngestgo.Input[any]) (any, error) {
			atomic.AddInt32(&fnCounter, 1)
			return nil, nil
		},
	)
	r.NoError(err)
	server, sync := serve(t, c)
	defer server.Close()
	r.NoError(sync())

	// Send two events with the same idempotency key header. Both events trigger
	// the function. The returned IDs are unique.
	sendIDs, err := c.SendMany(
		ctx,
		[]any{
			inngestgo.Event{Name: eventName},
			inngestgo.Event{Name: eventName},
		},
	)
	r.NoError(err)
	r.Len(sendIDs, 2)
	r.NotEqual(sendIDs[0], sendIDs[1])

	// Sleep long enough for the Dev Server to process the events.
	time.Sleep(5 * time.Second)
	r.Equal(int32(2), atomic.LoadInt32(&proxyCounter))
	r.Equal(int32(2), atomic.LoadInt32(&fnCounter))

	events, err := getEvents(ctx, eventName)
	r.NoError(err)

	// 4 events were stored: 2 from the first attempt and 2 from the second
	// attempt. This isn't ideal but it's the best we can do until we add
	// first-class event idempotency (it's currently enforced when scheduling
	// runs).
	r.Len(events, 4)

	uniqueIDs := map[string]struct{}{}
	for _, event := range events {
		r.NotNil(event.ID)
		uniqueIDs[*event.ID] = struct{}{}
	}

	// Only 2 unique IDs (despite 4 events) because their internal IDs are
	// deterministically generated from the same seed.
	r.Len(uniqueIDs, 2)

	// The send IDs match the IDs returned by the REST API.
	for _, id := range sendIDs {
		_, ok := uniqueIDs[id]
		r.True(ok)
	}
}

// getEvents returns all events with the given name.
func getEvents(ctx context.Context, name string) ([]inngestgo.Event, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("http://0.0.0.0:8288/v1/events?name=%s", name),
		nil,
	)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}

	var body struct {
		Data []inngestgo.Event `json:"data"`
	}

	err = json.NewDecoder(resp.Body).Decode(&body)
	if err != nil {
		return nil, err
	}
	return body.Data, nil
}
