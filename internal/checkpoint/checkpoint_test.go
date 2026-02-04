package checkpoint

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/opcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestCheckpointer(t *testing.T, batchSteps int, batchInterval time.Duration, handler http.HandlerFunc) *checkpointer {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	c := New(Opts{
		Config: Config{
			BatchSteps:    batchSteps,
			BatchInterval: batchInterval,
		},
		APIBaseURL: server.URL,
	}).(*checkpointer)
	c.client.httpClient = server.Client()
	return c
}

func TestClose_CancelsPendingTimer(t *testing.T) {
	var checkpointCalls atomic.Int32
	c := newTestCheckpointer(t, 100, 500*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkpointCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	// Add a step — this starts the background timer goroutine.
	c.WithStep(context.Background(), opcode.Step{
		Op: enums.OpcodeStepRun,
		ID: "step-1",
	}, func(_ []opcode.Step, _ error) {})

	// Close before the timer fires.
	c.Close()

	// Wait longer than the batch interval to confirm the goroutine didn't fire.
	time.Sleep(700 * time.Millisecond)

	assert.Equal(t, int32(0), checkpointCalls.Load(), "checkpoint should not have been called after Close")
}

func TestClose_ClearsBuffer(t *testing.T) {
	c := newTestCheckpointer(t, 100, time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	c.WithStep(context.Background(), opcode.Step{
		Op: enums.OpcodeStepRun,
		ID: "step-1",
	}, func(_ []opcode.Step, _ error) {})

	require.Len(t, c.buffer, 1, "buffer should have one step before Close")

	c.Close()

	c.lock.Lock()
	defer c.lock.Unlock()
	assert.Nil(t, c.buffer, "buffer should be nil after Close")
}

func TestClose_Idempotent(t *testing.T) {
	c := newTestCheckpointer(t, 100, time.Second, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Should not panic when called multiple times.
	c.Close()
	c.Close()
	c.Close()
}

func TestWithStep_BatchSteps_StillCheckpoints(t *testing.T) {
	var checkpointCalls atomic.Int32
	c := newTestCheckpointer(t, 1, 0, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkpointCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	var committed []opcode.Step
	c.WithStep(context.Background(), opcode.Step{
		Op: enums.OpcodeStepRun,
		ID: "step-1",
	}, func(done []opcode.Step, err error) {
		committed = done
	})

	assert.Equal(t, int32(1), checkpointCalls.Load(), "checkpoint should fire immediately when batch is full")
	require.Len(t, committed, 1)
	assert.Equal(t, "step-1", committed[0].ID)

	// Close should be safe after a completed checkpoint.
	c.Close()
}

func TestWithStep_BatchInterval_Checkpoints(t *testing.T) {
	var checkpointCalls atomic.Int32
	c := newTestCheckpointer(t, 100, 100*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkpointCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))

	c.WithStep(context.Background(), opcode.Step{
		Op: enums.OpcodeStepRun,
		ID: "step-1",
	}, func(_ []opcode.Step, _ error) {})

	// Wait for the timer to fire.
	time.Sleep(250 * time.Millisecond)

	assert.Equal(t, int32(1), checkpointCalls.Load(), "checkpoint should fire after batch interval")

	c.Close()
}
