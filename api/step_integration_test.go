package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/middleware"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/step"
)

func TestStepRun_APIFunctionExecution(t *testing.T) {
	capturedOps := []sdkrequest.GeneratorOpcode{}
	mockAPIManager := &mockAPIManagerWithCapture{
		onCheckpoint: func(runID string, op sdkrequest.GeneratorOpcode) {
			capturedOps = append(capturedOps, op)
		},
	}

	// Create API context with sync manager
	mgr := NewRequestManager("test-run", mockAPIManager, "test-key", middleware.New(), nil)
	ctx := sdkrequest.SetManager(context.Background(), mgr)

	// Execute step.Run in API context
	result, err := step.Run(ctx, "test-step", func(ctx context.Context) (string, error) {
		return "step-result", nil
	})

	// Verify step executed and returned result
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result != "step-result" {
		t.Errorf("Expected 'step-result', got %q", result)
	}

	// Verify step was checkpointed
	time.Sleep(10 * time.Millisecond) // Allow goroutine to complete
	if len(capturedOps) != 1 {
		t.Errorf("Expected 1 checkpointed op, got %d", len(capturedOps))
	}

	if capturedOps[0].Op != enums.OpcodeStepRun {
		t.Errorf("Expected StepRun opcode, got %v", capturedOps[0].Op)
	}

	if capturedOps[0].Name != "test-step" {
		t.Errorf("Expected step name 'test-step', got %q", capturedOps[0].Name)
	}
}

func TestStepRun_APIFunctionError(t *testing.T) {
	capturedOps := []sdkrequest.GeneratorOpcode{}
	mockAPIManager := &mockAPIManagerWithCapture{
		onCheckpoint: func(runID string, op sdkrequest.GeneratorOpcode) {
			capturedOps = append(capturedOps, op)
		},
	}

	mgr := NewRequestManager("test-run", mockAPIManager, "test-key", middleware.New(), nil)
	ctx := sdkrequest.SetManager(context.Background(), mgr)

	// Execute step.Run that fails
	result, err := step.Run(ctx, "failing-step", func(ctx context.Context) (string, error) {
		return "partial-result", fmt.Errorf("step failed")
	})

	// Verify error is returned and result is available
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if err.Error() != "step failed" {
		t.Errorf("Expected 'step failed', got %q", err.Error())
	}

	if result != "partial-result" {
		t.Errorf("Expected 'partial-result', got %q", result)
	}

	// Verify error was checkpointed
	time.Sleep(10 * time.Millisecond)
	if len(capturedOps) != 1 {
		t.Errorf("Expected 1 checkpointed op, got %d", len(capturedOps))
	}

	if capturedOps[0].Op != enums.OpcodeStepError {
		t.Errorf("Expected StepError opcode, got %v", capturedOps[0].Op)
	}

	if capturedOps[0].Error == nil {
		t.Error("Expected error data in checkpointed op")
	}
}

func TestStepRun_MultipleStepsInAPIFunction(t *testing.T) {
	capturedOps := []sdkrequest.GeneratorOpcode{}
	mockAPIManager := &mockAPIManagerWithCapture{
		onCheckpoint: func(runID string, op sdkrequest.GeneratorOpcode) {
			capturedOps = append(capturedOps, op)
		},
	}

	mgr := NewRequestManager("test-run", mockAPIManager, "test-key", middleware.New(), nil)
	ctx := sdkrequest.SetManager(context.Background(), mgr)

	// Execute multiple steps
	result1, err1 := step.Run(ctx, "step-1", func(ctx context.Context) (int, error) {
		return 42, nil
	})

	result2, err2 := step.Run(ctx, "step-2", func(ctx context.Context) (string, error) {
		return fmt.Sprintf("processed-%d", result1), nil
	})

	result3, err3 := step.Run(ctx, "step-3", func(ctx context.Context) (bool, error) {
		return len(result2) > 5, nil
	})

	// Verify all steps executed successfully
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("Expected no errors, got %v, %v, %v", err1, err2, err3)
	}

	if result1 != 42 {
		t.Errorf("Expected result1 42, got %v", result1)
	}

	if result2 != "processed-42" {
		t.Errorf("Expected result2 'processed-42', got %q", result2)
	}

	if !result3 {
		t.Errorf("Expected result3 true, got %v", result3)
	}

	// Verify all steps were checkpointed
	time.Sleep(50 * time.Millisecond)
	if len(capturedOps) != 3 {
		t.Errorf("Expected 3 checkpointed ops, got %d", len(capturedOps))
	}

	stepNames := []string{}
	for _, op := range capturedOps {
		stepNames = append(stepNames, op.Name)
	}

	expectedNames := []string{"step-1", "step-2", "step-3"}
	for i, expected := range expectedNames {
		if i >= len(stepNames) || stepNames[i] != expected {
			t.Errorf("Expected step %d to be %q, got %v", i, expected, stepNames)
		}
	}
}

func TestStepRun_HTTPIntegration(t *testing.T) {
	capturedOps := []sdkrequest.GeneratorOpcode{}
	mockAPIManager := &mockAPIManagerWithCapture{
		onCheckpoint: func(runID string, op sdkrequest.GeneratorOpcode) {
			capturedOps = append(capturedOps, op)
		},
	}

	middleware := &Middleware{
		apiManager: mockAPIManager,
		opts:       MiddlewareOpts{Domain: "test.com"},
		mw:         middleware.New(),
	}

	// Handler that uses multiple steps
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Step 1: Authenticate
		auth, err := step.Run(r.Context(), "auth", func(ctx context.Context) (string, error) {
			return "user123", nil
		})
		if err != nil {
			http.Error(w, "Auth failed", 401)
			return
		}

		// Step 2: Process
		data, err := step.Run(r.Context(), "process", func(ctx context.Context) (map[string]interface{}, error) {
			return map[string]interface{}{
				"user":   auth,
				"result": "processed",
			}, nil
		})
		if err != nil {
			http.Error(w, "Processing failed", 500)
			return
		}

		// Return result
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	}

	// Execute HTTP request
	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrappedHandler := middleware.Handler(handler)
	wrappedHandler(rr, req)

	// Verify HTTP response
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response["user"] != "user123" {
		t.Errorf("Expected user 'user123', got %v", response["user"])
	}

	if response["result"] != "processed" {
		t.Errorf("Expected result 'processed', got %v", response["result"])
	}

	// Verify steps were checkpointed
	time.Sleep(50 * time.Millisecond)
	if len(capturedOps) != 2 {
		t.Errorf("Expected 2 checkpointed ops, got %d", len(capturedOps))
	}
}

func TestStepRun_NoInvocationManager(t *testing.T) {
	// Test step.Run without API context (should still work for async functions)
	ctx := context.Background() // No InvocationManager set

	// This should fail gracefully
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic when no InvocationManager in context")
		}
	}()

	step.Run(ctx, "test", func(ctx context.Context) (string, error) {
		return "should not reach", nil
	})
}