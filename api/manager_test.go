package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/sdkrequest"
)

func TestAPIManager_CreateAPIRun(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/api-runs" {
			t.Errorf("Expected path /v1/api-runs, got %s", r.URL.Path)
		}

		if r.Method != "POST" {
			t.Errorf("Expected POST method, got %s", r.Method)
		}

		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Expected Authorization header, got %s", r.Header.Get("Authorization"))
		}

		// Parse request body
		var req CreateAPIRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Failed to decode request: %v", err)
		}

		if req.Domain != "test.com" {
			t.Errorf("Expected domain 'test.com', got %s", req.Domain)
		}

		if req.Endpoint != "/users" {
			t.Errorf("Expected endpoint '/users', got %s", req.Endpoint)
		}

		if req.Method != "POST" {
			t.Errorf("Expected method 'POST', got %s", req.Method)
		}

		// Send response
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(CreateAPIRunResponse{
			RunID: "run_123",
		})
	}))
	defer server.Close()

	manager := NewAPIManager(server.URL, "test-key")

	runID, err := manager.CreateAPIRun(
		context.Background(),
		"test.com",
		"/users",
		"POST",
		[]byte(`{"user": "test"}`),
		map[string]interface{}{"ip": "127.0.0.1"},
	)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if runID != "run_123" {
		t.Errorf("Expected runID 'run_123', got %s", runID)
	}
}

func TestAPIManager_CreateAPIRun_Error(t *testing.T) {
	// Mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal server error"))
	}))
	defer server.Close()

	manager := NewAPIManager(server.URL, "test-key")

	_, err := manager.CreateAPIRun(
		context.Background(),
		"test.com",
		"/users",
		"POST",
		[]byte(`{"user": "test"}`),
		nil,
	)

	if err == nil {
		t.Fatal("Expected error, got nil")
	}
}

func TestAPIManager_CheckpointStep(t *testing.T) {
	checkpointReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/api-runs/checkpoint" {
			checkpointReceived = true

			var req CheckpointStepRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("Failed to decode checkpoint request: %v", err)
				return
			}

			if req.RunID != "run_123" {
				t.Errorf("Expected runID 'run_123', got %s", req.RunID)
			}

			if req.Step.Name != "test-step" {
				t.Errorf("Expected step name 'test-step', got %s", req.Step.Name)
			}

			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	manager := NewAPIManager(server.URL, "test-key")

	step := sdkrequest.GeneratorOpcode{
		ID:   "step123",
		Op:   enums.OpcodeStepRun,
		Name: "test-step",
		Data: json.RawMessage(`{"result": "test"}`),
	}

	err := manager.CheckpointStep(context.Background(), "run_123", step)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Wait for background request to complete
	time.Sleep(100 * time.Millisecond)

	if !checkpointReceived {
		t.Error("Expected checkpoint request to be received")
	}
}

func TestAPIManager_StoreResult(t *testing.T) {
	resultReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/api-runs/result" {
			resultReceived = true

			var req StoreResultRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("Failed to decode result request: %v", err)
				return
			}

			if req.RunID != "run_123" {
				t.Errorf("Expected runID 'run_123', got %s", req.RunID)
			}

			if req.Result.StatusCode != 200 {
				t.Errorf("Expected status code 200, got %d", req.Result.StatusCode)
			}

			if string(req.Result.Body) != "test response" {
				t.Errorf("Expected body 'test response', got %s", string(req.Result.Body))
			}

			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	manager := NewAPIManager(server.URL, "test-key")

	result := APIResult{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "text/plain"},
		Body:       []byte("test response"),
		Duration:   100 * time.Millisecond,
	}

	err := manager.StoreResult(context.Background(), "run_123", result)

	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Wait for background request to complete
	time.Sleep(100 * time.Millisecond)

	if !resultReceived {
		t.Error("Expected result request to be received")
	}
}

func TestAPIManager_Timeout(t *testing.T) {
	// Server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(15 * time.Second) // Longer than client timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	manager := NewAPIManager(server.URL, "test-key")

	// This should timeout
	_, err := manager.CreateAPIRun(
		context.Background(),
		"test.com",
		"/users",
		"POST",
		[]byte(`{}`),
		nil,
	)

	if err == nil {
		t.Fatal("Expected timeout error, got nil")
	}
}

func TestAPIResult_JSONSerialization(t *testing.T) {
	result := APIResult{
		StatusCode: 201,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"X-Custom":     "value",
		},
		Body:     []byte(`{"id": "123"}`),
		Duration: 250 * time.Millisecond,
		Error:    "step failed",
	}

	// Serialize to JSON
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal APIResult: %v", err)
	}

	// Deserialize from JSON
	var restored APIResult
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Failed to unmarshal APIResult: %v", err)
	}

	// Verify fields
	if restored.StatusCode != result.StatusCode {
		t.Errorf("StatusCode mismatch: expected %d, got %d", result.StatusCode, restored.StatusCode)
	}

	if restored.Headers["Content-Type"] != result.Headers["Content-Type"] {
		t.Errorf("Headers mismatch: expected %s, got %s", 
			result.Headers["Content-Type"], restored.Headers["Content-Type"])
	}

	if string(restored.Body) != string(result.Body) {
		t.Errorf("Body mismatch: expected %s, got %s", string(result.Body), string(restored.Body))
	}

	if restored.Duration != result.Duration {
		t.Errorf("Duration mismatch: expected %v, got %v", result.Duration, restored.Duration)
	}

	if restored.Error != result.Error {
		t.Errorf("Error mismatch: expected %s, got %s", result.Error, restored.Error)
	}
}