package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
)

func TestSyncInvocationManager_StepMode(t *testing.T) {
	mgr := NewRequestManager("run123", nil, "key", middleware.New(), nil)

	if mgr.StepMode() != sdkrequest.StepModeContinue {
		t.Errorf("Expected StepModeContinue, got %v", mgr.StepMode())
	}
}

func TestSyncInvocationManager_Step_AlwaysReturnsFalse(t *testing.T) {
	mgr := NewRequestManager("run123", nil, "key", middleware.New(), nil)

	op := sdkrequest.UnhashedOp{
		ID: "test-step",
		Op: enums.OpcodeStepRun,
	}

	data, exists := mgr.Step(context.Background(), op)

	if exists {
		t.Error("Expected Step() to return false for API functions")
	}
	if data != nil {
		t.Error("Expected Step() to return nil data for API functions")
	}
}

func TestSyncInvocationManager_ReplayedStep_AlwaysReturnsFalse(t *testing.T) {
	mgr := NewRequestManager("run123", nil, "key", middleware.New(), nil)

	if mgr.ReplayedStep("any-id") {
		t.Error("Expected ReplayedStep() to always return false for API functions")
	}
}

func TestSyncInvocationManager_AppendOp(t *testing.T) {
	mockAPIManager := &mockAPIManager{}
	mgr := NewRequestManager("run123", mockAPIManager, "key", middleware.New(), nil)

	op := sdkrequest.GeneratorOpcode{
		ID:   "step123",
		Op:   enums.OpcodeStepRun,
		Name: "test-step",
		Data: json.RawMessage(`{"result": "test"}`),
	}

	mgr.AppendOp(op)

	ops := mgr.Ops()
	if len(ops) != 1 {
		t.Errorf("Expected 1 op, got %d", len(ops))
	}

	if ops[0].ID != "step123" {
		t.Errorf("Expected op ID 'step123', got %s", ops[0].ID)
	}

	// Verify checkpoint was called (with some delay for goroutine)
	if !mockAPIManager.checkpointCalled {
		t.Error("Expected CheckpointStep to be called")
	}
}

func TestSyncInvocationManager_Request(t *testing.T) {
	mgr := NewRequestManager("run123", nil, "key", middleware.New(), nil)

	req := mgr.Request()

	if req.CallCtx.RunID != "run123" {
		t.Errorf("Expected RunID 'run123', got %s", req.CallCtx.RunID)
	}

	if len(req.Steps) != 0 {
		t.Error("Expected empty Steps map for API functions")
	}
}

func TestSyncInvocationManager_ErrorHandling(t *testing.T) {
	mgr := NewRequestManager("run123", nil, "key", middleware.New(), nil)

	testErr := fmt.Errorf("test error")
	mgr.SetErr(testErr)

	if mgr.Err() != testErr {
		t.Errorf("Expected error to be set and retrieved correctly")
	}
}

func TestSyncInvocationManager_NewOp(t *testing.T) {
	mgr := NewRequestManager("run123", nil, "key", middleware.New(), nil)

	op := mgr.NewOp(enums.OpcodeStepRun, "test-step", map[string]any{"key": "value"})

	if op.ID != "test-step" {
		t.Errorf("Expected op ID 'test-step', got %s", op.ID)
	}

	if op.Op != enums.OpcodeStepRun {
		t.Errorf("Expected op type StepRun, got %v", op.Op)
	}

	if op.Pos != 0 {
		t.Errorf("Expected position 0 for API functions, got %d", op.Pos)
	}
}

// Mock APIManager for testing
type mockAPIManager struct {
	checkpointCalled bool
	resultStored     bool
	runCreated       bool
}

func (m *mockAPIManager) CreateAPIRun(ctx context.Context, domain, endpoint, method string, input []byte, metadata map[string]interface{}) (string, error) {
	m.runCreated = true
	return "test-run-id", nil
}

func (m *mockAPIManager) CheckpointStep(ctx context.Context, runID string, step sdkrequest.GeneratorOpcode) error {
	m.checkpointCalled = true
	return nil
}

func (m *mockAPIManager) StoreResult(ctx context.Context, runID string, result APIResult) error {
	m.resultStored = true
	return nil
}

