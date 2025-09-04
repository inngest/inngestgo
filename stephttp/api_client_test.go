package stephttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPIClient_RetryLogic(t *testing.T) {
	var callCount atomic.Int32
	var callTimes []time.Time

	// Create a test server that fails 4 times, then succeeds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callTimes = append(callTimes, time.Now())
		count := callCount.Add(1)
		
		if count <= 4 {
			// Fail the first 4 attempts
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "server error"}`))
			return
		}
		
		// Succeed on the 5th attempt
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data": {"success": true}}`))
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-key", "")
	
	start := time.Now()
	_, err := client.do(context.Background(), "GET", "/test", nil)
	end := time.Now()
	
	// Should succeed after retries
	if err != nil {
		t.Fatalf("Expected success after retries, got error: %v", err)
	}
	
	// Should have made exactly 5 calls
	if callCount.Load() != 5 {
		t.Errorf("Expected 5 calls, got %d", callCount.Load())
	}
	
	// Verify backoff timing (should be approximately 50ms + 100ms + 200ms + 400ms = 750ms)
	totalDuration := end.Sub(start)
	expectedMin := 750 * time.Millisecond
	expectedMax := 850 * time.Millisecond // Allow some tolerance
	
	if totalDuration < expectedMin {
		t.Errorf("Total duration too short: got %v, expected at least %v", totalDuration, expectedMin)
	}
	if totalDuration > expectedMax {
		t.Errorf("Total duration too long: got %v, expected at most %v", totalDuration, expectedMax)
	}
	
	// Verify backoff intervals between calls
	if len(callTimes) != 5 {
		t.Fatalf("Expected 5 call times, got %d", len(callTimes))
	}
	
	expectedBackoffs := []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	for i := 1; i < len(callTimes); i++ {
		actualBackoff := callTimes[i].Sub(callTimes[i-1])
		expectedBackoff := expectedBackoffs[i-1]
		
		// Allow 10ms tolerance for timing variations
		tolerance := 10 * time.Millisecond
		if actualBackoff < expectedBackoff-tolerance || actualBackoff > expectedBackoff+tolerance {
			t.Errorf("Backoff %d: expected ~%v, got %v", i, expectedBackoff, actualBackoff)
		}
	}
}

func TestAPIClient_RetryFailure(t *testing.T) {
	var callCount atomic.Int32

	// Create a test server that always fails
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "server error"}`))
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "test-key", "")
	
	_, err := client.do(context.Background(), "GET", "/test", nil)
	
	// Should fail after all retries
	if err == nil {
		t.Fatal("Expected error after all retries failed")
	}
	
	// Should have made exactly 5 calls
	if callCount.Load() != 5 {
		t.Errorf("Expected 5 calls, got %d", callCount.Load())
	}
}

func TestAPIClient_SigningKeyRotation(t *testing.T) {
	var callCount atomic.Int32
	var usedKeys []string

	// Create a test server that fails with 401 for primary key, succeeds for fallback
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		authHeader := r.Header.Get("Authorization")
		usedKeys = append(usedKeys, authHeader)
		
		if authHeader == "Bearer primary-key" {
			// Fail with 401 for primary key
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		
		if authHeader == "Bearer fallback-key" {
			// Succeed for fallback key
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"data": {"success": true}}`))
			return
		}
		
		// Unexpected key
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "invalid key"}`))
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "primary-key", "fallback-key")
	
	_, err := client.do(context.Background(), "GET", "/test", nil)
	
	// Should succeed after key rotation
	if err != nil {
		t.Fatalf("Expected success after key rotation, got error: %v", err)
	}
	
	// Should have made exactly 2 calls (primary fails, fallback succeeds)
	if callCount.Load() != 2 {
		t.Errorf("Expected 2 calls, got %d", callCount.Load())
	}
	
	// Verify key rotation occurred
	if len(usedKeys) != 2 {
		t.Fatalf("Expected 2 authorization headers, got %d", len(usedKeys))
	}
	
	if usedKeys[0] != "Bearer primary-key" {
		t.Errorf("Expected first call to use primary key, got %s", usedKeys[0])
	}
	
	if usedKeys[1] != "Bearer fallback-key" {
		t.Errorf("Expected second call to use fallback key, got %s", usedKeys[1])
	}
	
	// Verify that subsequent calls use fallback key
	callCount.Store(0)
	usedKeys = nil
	
	_, err = client.do(context.Background(), "GET", "/test2", nil)
	if err != nil {
		t.Fatalf("Expected success on subsequent call, got error: %v", err)
	}
	
	if callCount.Load() != 1 {
		t.Errorf("Expected 1 call on subsequent request, got %d", callCount.Load())
	}
	
	if len(usedKeys) != 1 || usedKeys[0] != "Bearer fallback-key" {
		t.Errorf("Expected subsequent call to use fallback key, got %v", usedKeys)
	}
}

func TestAPIClient_NoFallbackKey(t *testing.T) {
	var callCount atomic.Int32

	// Create a test server that always returns 401
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized"}`))
	}))
	defer server.Close()

	client := NewAPIClient(server.URL, "primary-key", "") // No fallback key
	
	_, err := client.do(context.Background(), "GET", "/test", nil)
	
	// Should fail after all retries
	if err == nil {
		t.Fatal("Expected error after all retries failed")
	}
	
	// Should have made exactly 5 calls (no key rotation, just retries)
	if callCount.Load() != 5 {
		t.Errorf("Expected 5 calls, got %d", callCount.Load())
	}
}

func TestValidateResumeRequestSignature_DevMode(t *testing.T) {
	// Set dev mode
	originalDev := os.Getenv("INNGEST_DEV")
	os.Setenv("INNGEST_DEV", "1")
	defer func() {
		if originalDev == "" {
			os.Unsetenv("INNGEST_DEV")
		} else {
			os.Setenv("INNGEST_DEV", originalDev)
		}
	}()

	// Create test request
	req := httptest.NewRequest("POST", "/test", nil)
	
	// Should return true in dev mode regardless of signature or headers
	result := validateResumeRequestSignature(context.Background(), req, "key", "fallback")
	if !result {
		t.Error("Expected validation to pass in dev mode")
	}
}

func TestValidateResumeRequestSignature_MissingSignature(t *testing.T) {
	// Ensure not in dev mode
	originalDev := os.Getenv("INNGEST_DEV")
	os.Unsetenv("INNGEST_DEV")
	defer func() {
		if originalDev != "" {
			os.Setenv("INNGEST_DEV", originalDev)
		}
	}()

	// Create test request with run ID header but no signature
	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("x-run-id", "01HW5N8XQZJ9V2M3K4L5P6Q7R8")
	
	// Should return false when signature is missing
	result := validateResumeRequestSignature(context.Background(), req, "key", "fallback")
	if result {
		t.Error("Expected validation to fail with missing signature")
	}
}

func TestValidateResumeRequestSignature_MissingRunID(t *testing.T) {
	// Ensure not in dev mode
	originalDev := os.Getenv("INNGEST_DEV")
	os.Unsetenv("INNGEST_DEV")
	defer func() {
		if originalDev != "" {
			os.Setenv("INNGEST_DEV", originalDev)
		}
	}()

	// Create test request with signature but no run ID header
	req := httptest.NewRequest("POST", "/test", nil)
	req.Header.Set("x-inngest-signature", "t=123&s=abc")
	
	// Should return false when run ID header is missing
	result := validateResumeRequestSignature(context.Background(), req, "key", "fallback")
	if result {
		t.Error("Expected validation to fail with missing run ID header")
	}
}