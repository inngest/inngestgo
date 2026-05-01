package sdkrequest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/checkpoint"
	"github.com/inngest/inngestgo/internal/fn"
	"github.com/inngest/inngestgo/internal/opcode"
	checkpointpkg "github.com/inngest/inngestgo/pkg/checkpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testFunction struct {
	config fn.FunctionOpts
}

func (f testFunction) FullyQualifiedID() string { return "test-fn" }
func (f testFunction) ID() string               { return "test-fn" }
func (f testFunction) Name() string             { return "test fn" }
func (f testFunction) Config() fn.FunctionOpts  { return f.config }
func (f testFunction) Trigger() fn.Triggerable  { return nil }
func (f testFunction) ZeroEvent() any           { return nil }
func (f testFunction) Func() any                { return nil }

func TestCallCtxGenerationIDUnmarshal(t *testing.T) {
	var req Request
	err := json.Unmarshal([]byte(`{"ctx":{"generation_id":789}}`), &req)

	require.NoError(t, err)
	assert.Equal(t, 789, req.CallCtx.GenerationID)
}

func TestManagerThreadsGenerationIDToCheckpoint(t *testing.T) {
	var received checkpoint.AsyncRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewDecoder(r.Body).Decode(&received)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	mgr := NewManager(Opts{
		Mode: StepModeCheckpoint,
		Fn: testFunction{config: fn.FunctionOpts{
			Checkpoint: &checkpointpkg.Config{BatchSteps: 1},
		}},
		Request: &Request{CallCtx: CallCtx{
			FunctionID:   uuid.New(),
			RunID:        "run-123",
			QueueItemRef: "qi-456",
			GenerationID: 789,
		}},
		APIBaseURL: server.URL,
	})

	mgr.AppendOp(context.Background(), opcode.Step{
		Op: enums.OpcodeStepRun,
		ID: "step-1",
	})

	assert.Equal(t, 789, received.GenerationID)
}

func TestManagerStaleDispatchHijacksCheckpointExecution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	var cancelled atomic.Bool
	mgr := NewManager(Opts{
		Mode: StepModeCheckpoint,
		Fn: testFunction{config: fn.FunctionOpts{
			Checkpoint: &checkpointpkg.Config{BatchSteps: 1},
		}},
		Request: &Request{CallCtx: CallCtx{
			FunctionID:   uuid.New(),
			RunID:        "run-123",
			QueueItemRef: "qi-456",
			GenerationID: 789,
		}},
		Cancel: func() {
			cancelled.Store(true)
		},
		APIBaseURL: server.URL,
	})

	require.PanicsWithValue(t, ControlHijack{}, func() {
		mgr.AppendOp(context.Background(), opcode.Step{
			Op: enums.OpcodeStepRun,
			ID: "step-1",
		})
	})
	assert.True(t, cancelled.Load())
	assert.Empty(t, mgr.Ops(),
		"stale dispatch must leave no buffered ops; otherwise the recovered "+
			"handler returns them and the executor chains further dispatches "+
			"off the stale SDK's response (EXE-1552)")
}
