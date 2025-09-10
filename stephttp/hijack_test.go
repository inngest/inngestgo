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