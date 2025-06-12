package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/inngest/inngestgo/step"
)

func TestMiddleware_Handler_BasicFlow(t *testing.T) {
	mockAPIManager := &mockAPIManager{}
	middleware := &Middleware{
		apiManager: mockAPIManager,
		opts: MiddlewareOpts{
			Domain:     "test.com",
			SigningKey: "test-key",
		},
		mw: nil, // We'll handle this in the test
	}

	// Test handler that uses step.Run
	handler := func(w http.ResponseWriter, r *http.Request) {
		// This should work with step tooling
		result, err := step.Run(r.Context(), "test-step", func(ctx context.Context) (string, error) {
			return "test-result", nil
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"result": result})
	}

	// Create test request
	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"test": "data"}`))
	req.Header.Set("Content-Type", "application/json")
	
	// Record response
	rr := httptest.NewRecorder()
	
	// Execute
	wrappedHandler := middleware.Handler(handler)
	wrappedHandler(rr, req)

	// Verify response
	if rr.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rr.Code)
	}

	// Verify API manager was called
	if !mockAPIManager.runCreated {
		t.Error("Expected CreateAPIRun to be called")
	}
	
	if !mockAPIManager.resultStored {
		t.Error("Expected StoreResult to be called")
	}
}

func TestMiddleware_Handler_RequestBodyCapture(t *testing.T) {
	capturedBody := ""
	mockAPIManager := &mockAPIManagerWithCapture{
		onCreateRun: func(domain, endpoint, method string, input []byte, metadata map[string]interface{}) {
			capturedBody = string(input)
		},
	}

	middleware := &Middleware{
		apiManager: mockAPIManager,
		opts: MiddlewareOpts{
			Domain: "test.com",
		},
		mw: nil,
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}

	testBody := `{"user": "test", "action": "create"}`
	req := httptest.NewRequest("POST", "/users", strings.NewReader(testBody))
	rr := httptest.NewRecorder()

	wrappedHandler := middleware.Handler(handler)
	wrappedHandler(rr, req)

	if capturedBody != testBody {
		t.Errorf("Expected body %q, got %q", testBody, capturedBody)
	}
}

func TestMiddleware_Handler_ResponseCapture(t *testing.T) {
	var capturedResult APIResult
	mockAPIManager := &mockAPIManagerWithCapture{
		onStoreResult: func(result APIResult) {
			capturedResult = result
		},
	}

	middleware := &Middleware{
		apiManager: mockAPIManager,
		opts:       MiddlewareOpts{Domain: "test.com"},
		mw:         nil,
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "test-value")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id": "123"}`))
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	wrappedHandler := middleware.Handler(handler)
	wrappedHandler(rr, req)

	// Wait a moment for async result storage
	time.Sleep(10 * time.Millisecond)

	if capturedResult.StatusCode != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", capturedResult.StatusCode)
	}

	if capturedResult.Headers["X-Custom"] != "test-value" {
		t.Errorf("Expected custom header, got %v", capturedResult.Headers)
	}

	if string(capturedResult.Body) != `{"id": "123"}` {
		t.Errorf("Expected body %q, got %q", `{"id": "123"}`, string(capturedResult.Body))
	}

	if capturedResult.Duration <= 0 {
		t.Error("Expected positive duration")
	}
}

func TestMiddleware_Handler_ErrorHandling(t *testing.T) {
	mockAPIManager := &mockAPIManager{}
	middleware := &Middleware{
		apiManager: mockAPIManager,
		opts:       MiddlewareOpts{Domain: "test.com"},
		mw:         nil,
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		// Simulate a step that fails
		_, err := step.Run(r.Context(), "failing-step", func(ctx context.Context) (string, error) {
			return "", fmt.Errorf("step failed")
		})
		if err != nil {
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
	}

	req := httptest.NewRequest("POST", "/test", nil)
	rr := httptest.NewRecorder()

	wrappedHandler := middleware.Handler(handler)
	wrappedHandler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", rr.Code)
	}
}

func TestResponseWriter_StatusCodeCapture(t *testing.T) {
	rr := httptest.NewRecorder()
	rw := newResponseWriter(rr)

	rw.WriteHeader(http.StatusNotFound)
	rw.Write([]byte("Not found"))

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", rw.statusCode)
	}

	if rw.body.String() != "Not found" {
		t.Errorf("Expected body 'Not found', got %q", rw.body.String())
	}
}

func TestResponseWriter_DefaultStatusCode(t *testing.T) {
	rr := httptest.NewRecorder()
	rw := newResponseWriter(rr)

	rw.Write([]byte("OK"))

	if rw.statusCode != http.StatusOK {
		t.Errorf("Expected default status 200, got %d", rw.statusCode)
	}
}

func TestFlattenHeaders(t *testing.T) {
	headers := http.Header{
		"Content-Type":   []string{"application/json"},
		"X-Custom":       []string{"value1", "value2"},
		"Authorization":  []string{"Bearer token"},
	}

	flattened := flattenHeaders(headers)

	expected := map[string]string{
		"Content-Type":  "application/json",
		"X-Custom":      "value1", // Should take first value
		"Authorization": "Bearer token",
	}

	for key, expectedValue := range expected {
		if flattened[key] != expectedValue {
			t.Errorf("For header %s, expected %q, got %q", key, expectedValue, flattened[key])
		}
	}
}

// Mock with capture capabilities
type mockAPIManagerWithCapture struct {
	onCreateRun   func(domain, endpoint, method string, input []byte, metadata map[string]interface{})
	onCheckpoint  func(runID string, step sdkrequest.GeneratorOpcode)
	onStoreResult func(result APIResult)
}

func (m *mockAPIManagerWithCapture) CreateAPIRun(ctx context.Context, domain, endpoint, method string, input []byte, metadata map[string]interface{}) (string, error) {
	if m.onCreateRun != nil {
		m.onCreateRun(domain, endpoint, method, input, metadata)
	}
	return "test-run-id", nil
}

func (m *mockAPIManagerWithCapture) CheckpointStep(ctx context.Context, runID string, step sdkrequest.GeneratorOpcode) error {
	if m.onCheckpoint != nil {
		m.onCheckpoint(runID, step)
	}
	return nil
}

func (m *mockAPIManagerWithCapture) StoreResult(ctx context.Context, runID string, result APIResult) error {
	if m.onStoreResult != nil {
		m.onStoreResult(result)
	}
	return nil
}