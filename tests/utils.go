package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/inngest/inngestgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	StatusFailed = "Failed"
)

type Run struct {
	Output any
	Status string `json:"status"`
}

func getRun(id string) (*Run, error) {
	res, err := http.Get(fmt.Sprintf("http://localhost:8288/v1/runs/%s", id))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = res.Body.Close()
	}()

	byt, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var body struct {
		Data Run `json:"data"`
	}
	err = json.Unmarshal(byt, &body)
	if err != nil {
		return nil, err
	}

	return &body.Data, nil
}

func waitForRun(t *testing.T, id *atomic.Value, status string) *Run {
	r := require.New(t)

	var run *Run
	r.EventuallyWithT(func(ct *assert.CollectT) {
		a := assert.New(ct)

		if !a.NotNil(id, "run ID is nil") {
			return
		}

		idStr, ok := id.Load().(string)
		if !a.True(ok, "run ID is not a string") {
			return
		}

		var err error
		run, err = getRun(idStr)
		if !a.NoError(err) {
			return
		}

		a.Equal(status, run.Status)
	}, 5*time.Second, time.Second)
	return run
}

func randomSuffix(s string) string {
	return s + uuid.NewString()
}

type serveOpts struct {
	Middleware func(http.Handler) http.Handler
}

func serve(
	t *testing.T,
	c inngestgo.Client,
	opts ...serveOpts,
) (*httptest.Server, func() error) {
	var o serveOpts
	if len(opts) > 0 {
		o = opts[0]
	}

	handler := c.Serve()
	if o.Middleware != nil {
		handler = o.Middleware(handler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler.ServeHTTP)
	server := httptest.NewServer(mux)

	sync := func() error {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, server.URL, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		byt, _ := io.ReadAll(resp.Body)
		if resp.StatusCode > 299 {
			return fmt.Errorf("invalid status code: %d (%s)", resp.StatusCode, byt)
		}
		_ = resp.Body.Close()
		return nil
	}

	return server, sync
}

type SafeSlice[T any] struct {
	mu    sync.Mutex
	slice []T
}

func (s *SafeSlice[T]) Append(v T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slice = append(s.slice, v)
}

func (s *SafeSlice[T]) Load() []T {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slice
}

// TypedAtomic provides a type-safe wrapper around sync/atomic.Value.
type TypedAtomic[T any] struct {
	value atomic.Value
}

// Store atomically stores the value.
func (s *TypedAtomic[T]) Store(v T) {
	s.value.Store(v)
}

// Load atomically loads the value. Returns the zero value and false if no value
// has been stored.
func (s *TypedAtomic[T]) Load() (T, bool) {
	v := s.value.Load()
	if v == nil {
		var zero T
		return zero, false
	}
	return v.(T), true
}
