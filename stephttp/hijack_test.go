package stephttp

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockHijacker implements http.Hijacker for testing
type mockHijacker struct {
	*httptest.ResponseRecorder
	hijackCalled bool
}

func (m *mockHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijackCalled = true
	return nil, nil, nil // Return nil values for test simplicity
}

// mockFlusher implements http.Flusher for testing
type mockFlusher struct {
	*httptest.ResponseRecorder
	flushCalled bool
}

func (m *mockFlusher) Flush() {
	m.flushCalled = true
}

// mockPusher implements http.Pusher for testing
type mockPusher struct {
	*httptest.ResponseRecorder
	pushCalled bool
	pushTarget string
	pushOpts   *http.PushOptions
}

func (m *mockPusher) Push(target string, opts *http.PushOptions) error {
	m.pushCalled = true
	m.pushTarget = target
	m.pushOpts = opts
	return nil
}

// mockFullFeatured implements all HTTP interfaces for testing
type mockFullFeatured struct {
	*httptest.ResponseRecorder
	hijackCalled bool
	flushCalled  bool
	pushCalled   bool
	pushTarget   string
	pushOpts     *http.PushOptions
}

func (m *mockFullFeatured) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	m.hijackCalled = true
	return nil, nil, nil
}

func (m *mockFullFeatured) Flush() {
	m.flushCalled = true
}

func (m *mockFullFeatured) Push(target string, opts *http.PushOptions) error {
	m.pushCalled = true
	m.pushTarget = target
	m.pushOpts = opts
	return nil
}

// mockNonHijacker doesn't implement http.Hijacker
type mockNonHijacker struct {
	*httptest.ResponseRecorder
}

func TestResponseWriter_HijackSupported(t *testing.T) {
	mockWriter := &mockHijacker{
		ResponseRecorder: httptest.NewRecorder(),
	}
	
	rw := newResponseWriter(mockWriter)
	
	// Test that Hijack works when underlying writer supports it
	_, _, err := rw.Hijack()
	if err != nil {
		t.Fatalf("Expected hijack to succeed, got error: %v", err)
	}
	
	if !mockWriter.hijackCalled {
		t.Error("Expected Hijack to be called on underlying writer")
	}
	
	if !rw.hijacked {
		t.Error("Expected responseWriter to be marked as hijacked")
	}
}

func TestResponseWriter_HijackNotSupported(t *testing.T) {
	mockWriter := &mockNonHijacker{
		ResponseRecorder: httptest.NewRecorder(),
	}
	
	rw := newResponseWriter(mockWriter)
	
	// Test that Hijack fails when underlying writer doesn't support it
	_, _, err := rw.Hijack()
	if err != http.ErrNotSupported {
		t.Fatalf("Expected ErrNotSupported, got: %v", err)
	}
	
	if rw.hijacked {
		t.Error("Expected responseWriter to not be marked as hijacked when hijack fails")
	}
}

func TestResponseWriter_WriteAfterHijack(t *testing.T) {
	mockWriter := &mockHijacker{
		ResponseRecorder: httptest.NewRecorder(),
	}
	
	rw := newResponseWriter(mockWriter)
	
	// Write some data before hijacking
	testData1 := []byte("before hijack")
	_, err := rw.Write(testData1)
	if err != nil {
		t.Fatalf("Unexpected error writing data: %v", err)
	}
	
	if rw.body.String() != string(testData1) {
		t.Errorf("Expected body to contain '%s', got '%s'", testData1, rw.body.String())
	}
	
	// Hijack the connection
	_, _, err = rw.Hijack()
	if err != nil {
		t.Fatalf("Unexpected error hijacking connection: %v", err)
	}
	
	// Write some data after hijacking - should not be captured
	testData2 := []byte(" after hijack")
	_, err = rw.Write(testData2)
	if err != nil {
		t.Fatalf("Unexpected error writing data after hijack: %v", err)
	}
	
	// Body should still only contain data from before hijack
	if rw.body.String() != string(testData1) {
		t.Errorf("Expected body to still contain only '%s', got '%s'", testData1, rw.body.String())
	}
}

func TestResponseWriter_InterfaceAssertion(t *testing.T) {
	// Test with hijacker support
	mockWriter := &mockHijacker{
		ResponseRecorder: httptest.NewRecorder(),
	}
	rw := newResponseWriter(mockWriter)
	
	if _, ok := any(rw).(http.Hijacker); !ok {
		t.Error("Expected responseWriter to implement http.Hijacker when underlying writer does")
	}
	
	// Test without hijacker support - this should still work since our
	// responseWriter always implements the interface, just returns an error
	mockNonHijacker := &mockNonHijacker{
		ResponseRecorder: httptest.NewRecorder(),
	}
	rw2 := newResponseWriter(mockNonHijacker)
	
	if _, ok := any(rw2).(http.Hijacker); !ok {
		t.Error("Expected responseWriter to always implement http.Hijacker interface")
	}
}

func TestResponseWriter_FlushSupported(t *testing.T) {
	mockWriter := &mockFlusher{
		ResponseRecorder: httptest.NewRecorder(),
	}
	
	rw := newResponseWriter(mockWriter)
	
	// Test that Flush works when underlying writer supports it
	rw.Flush()
	
	if !mockWriter.flushCalled {
		t.Error("Expected Flush to be called on underlying writer")
	}
}

func TestResponseWriter_FlushNotSupported(t *testing.T) {
	mockWriter := &mockNonHijacker{
		ResponseRecorder: httptest.NewRecorder(),
	}
	
	rw := newResponseWriter(mockWriter)
	
	// Test that Flush doesn't panic when underlying writer doesn't support it
	// This should just be a no-op
	rw.Flush()
	
	// No assertions needed - just ensure it doesn't panic
}

func TestResponseWriter_PushSupported(t *testing.T) {
	mockWriter := &mockPusher{
		ResponseRecorder: httptest.NewRecorder(),
	}
	
	rw := newResponseWriter(mockWriter)
	
	// Test that Push works when underlying writer supports it
	target := "/api/data"
	opts := &http.PushOptions{Method: "GET"}
	
	err := rw.Push(target, opts)
	if err != nil {
		t.Fatalf("Expected push to succeed, got error: %v", err)
	}
	
	if !mockWriter.pushCalled {
		t.Error("Expected Push to be called on underlying writer")
	}
	
	if mockWriter.pushTarget != target {
		t.Errorf("Expected target to be '%s', got '%s'", target, mockWriter.pushTarget)
	}
	
	if mockWriter.pushOpts != opts {
		t.Error("Expected push options to be passed through")
	}
}

func TestResponseWriter_PushNotSupported(t *testing.T) {
	mockWriter := &mockNonHijacker{
		ResponseRecorder: httptest.NewRecorder(),
	}
	
	rw := newResponseWriter(mockWriter)
	
	// Test that Push fails when underlying writer doesn't support it
	err := rw.Push("/api/data", nil)
	if err != http.ErrNotSupported {
		t.Fatalf("Expected ErrNotSupported, got: %v", err)
	}
}

func TestResponseWriter_AllInterfaces(t *testing.T) {
	mockWriter := &mockFullFeatured{
		ResponseRecorder: httptest.NewRecorder(),
	}
	
	rw := newResponseWriter(mockWriter)
	
	// Test that all interfaces work together
	rw.Flush()
	if !mockWriter.flushCalled {
		t.Error("Expected Flush to be called")
	}
	
	err := rw.Push("/test", nil)
	if err != nil {
		t.Fatalf("Expected Push to succeed, got: %v", err)
	}
	if !mockWriter.pushCalled {
		t.Error("Expected Push to be called")
	}
	
	_, _, err = rw.Hijack()
	if err != nil {
		t.Fatalf("Expected Hijack to succeed, got: %v", err)
	}
	if !mockWriter.hijackCalled {
		t.Error("Expected Hijack to be called")
	}
}

func TestResponseWriter_InterfaceImplementations(t *testing.T) {
	// Test that responseWriter always implements all the interfaces,
	// regardless of underlying writer capabilities
	mockWriter := &mockNonHijacker{
		ResponseRecorder: httptest.NewRecorder(),
	}
	rw := newResponseWriter(mockWriter)
	
	if _, ok := any(rw).(http.Hijacker); !ok {
		t.Error("Expected responseWriter to implement http.Hijacker")
	}
	
	if _, ok := any(rw).(http.Flusher); !ok {
		t.Error("Expected responseWriter to implement http.Flusher")
	}
	
	if _, ok := any(rw).(http.Pusher); !ok {
		t.Error("Expected responseWriter to implement http.Pusher")
	}
}