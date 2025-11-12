package stephttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestWebSocketIntegration tests that our responseWriter works with real WebSocket connections
func TestWebSocketIntegration(t *testing.T) {
	var (
		serverMessages  []string
		clientMessages  []string
		mu              sync.Mutex
		connEstablished bool
	)

	// Create a WebSocket handler that uses our responseWriter wrapper
	wsHandler := func(w http.ResponseWriter, r *http.Request) {
		// Wrap the response writer with our responseWriter
		rw := newResponseWriter(w)

		// Accept WebSocket connection - this will use Hijack()
		conn, err := websocket.Accept(rw, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true, // For testing only
		})
		if err != nil {
			t.Errorf("Failed to accept WebSocket connection: %v", err)
			return
		}
		defer func() {
			_ = conn.Close(websocket.StatusNormalClosure, "test complete")
		}()

		mu.Lock()
		connEstablished = true
		mu.Unlock()

		// Handle WebSocket messages
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Read messages from client
		go func() {
			for {
				_, msg, err := conn.Read(ctx)
				if err != nil {
					return
				}
				mu.Lock()
				serverMessages = append(serverMessages, string(msg))
				mu.Unlock()

				// Echo the message back with a prefix
				response := "server-echo: " + string(msg)
				if err := conn.Write(ctx, websocket.MessageText, []byte(response)); err != nil {
					return
				}
			}
		}()

		// Send a welcome message
		if err := conn.Write(ctx, websocket.MessageText, []byte("welcome")); err != nil {
			t.Errorf("Failed to send welcome message: %v", err)
			return
		}

		// Keep connection alive for testing
		<-ctx.Done()
	}

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(wsHandler))
	defer server.Close()

	// Convert HTTP URL to WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Create WebSocket client
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to WebSocket server: %v", err)
	}
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "client done")
	}()

	// Wait a bit for connection to establish
	time.Sleep(100 * time.Millisecond)

	// Verify connection was established
	mu.Lock()
	if !connEstablished {
		t.Fatal("WebSocket connection was not established")
	}
	mu.Unlock()

	// Read welcome message
	_, welcomeMsg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Failed to read welcome message: %v", err)
	}

	mu.Lock()
	clientMessages = append(clientMessages, string(welcomeMsg))
	mu.Unlock()

	if string(welcomeMsg) != "welcome" {
		t.Errorf("Expected welcome message 'welcome', got '%s'", welcomeMsg)
	}

	// Send test messages
	testMessages := []string{"hello", "websocket", "test"}

	for _, msg := range testMessages {
		// Send message to server
		if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatalf("Failed to send message '%s': %v", msg, err)
		}

		// Read echo response
		_, response, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("Failed to read response for message '%s': %v", msg, err)
		}

		mu.Lock()
		clientMessages = append(clientMessages, string(response))
		mu.Unlock()

		expectedResponse := "server-echo: " + msg
		if string(response) != expectedResponse {
			t.Errorf("Expected response '%s', got '%s'", expectedResponse, response)
		}
	}

	// Wait a bit for all messages to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify all messages were received by server
	mu.Lock()
	defer mu.Unlock()

	if len(serverMessages) != len(testMessages) {
		t.Errorf("Expected server to receive %d messages, got %d", len(testMessages), len(serverMessages))
	}

	for i, expectedMsg := range testMessages {
		if i >= len(serverMessages) {
			t.Errorf("Server missing message %d: '%s'", i, expectedMsg)
			continue
		}
		if serverMessages[i] != expectedMsg {
			t.Errorf("Server message %d: expected '%s', got '%s'", i, expectedMsg, serverMessages[i])
		}
	}

	// Verify all echo messages were received by client
	expectedClientMessages := len(testMessages) + 1 // +1 for welcome message
	if len(clientMessages) != expectedClientMessages {
		t.Errorf("Expected client to receive %d messages, got %d", expectedClientMessages, len(clientMessages))
	}
}

// TestWebSocketWithStepHTTPProvider tests WebSocket with the full stephttp provider
func TestWebSocketWithStepHTTPProvider(t *testing.T) {
	var wsConnected bool
	var mu sync.Mutex

	// Create a provider
	provider := Setup(SetupOpts{
		Domain: "test.example.com",
	})

	// Create a handler that establishes WebSocket connection
	wsHandler := func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Errorf("Failed to accept WebSocket with stephttp provider: %v", err)
			return
		}
		defer func() {
			_ = conn.Close(websocket.StatusNormalClosure, "test done")
		}()

		mu.Lock()
		wsConnected = true
		mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Send a test message
		if err := conn.Write(ctx, websocket.MessageText, []byte("provider-test")); err != nil {
			t.Errorf("Failed to send message: %v", err)
		}

		<-ctx.Done()
	}

	// Wrap with stephttp middleware
	wrappedHandler := provider.ServeHTTP(wsHandler)

	// Create test server
	server := httptest.NewServer(wrappedHandler)
	defer server.Close()

	// Convert to WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Connect as client
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect through stephttp provider: %v", err)
	}
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "client done")
	}()

	// Read the test message
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Failed to read message from provider-wrapped handler: %v", err)
	}

	if string(msg) != "provider-test" {
		t.Errorf("Expected message 'provider-test', got '%s'", msg)
	}

	// Verify connection was established
	mu.Lock()
	if !wsConnected {
		t.Error("WebSocket connection was not established with stephttp provider")
	}
	mu.Unlock()
}

// TestRegularHTTPStillWorks ensures regular HTTP requests still work and record responses
func TestRegularHTTPStillWorks(t *testing.T) {
	var recordedResponse []byte
	var recordedStatus int

	// Create a regular HTTP handler
	httpHandler := func(w http.ResponseWriter, r *http.Request) {
		// This should be recorded since it's not hijacked
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := `{"message": "regular http works"}`
		_, _ = w.Write([]byte(response))

		// Capture what was written for verification
		if rw, ok := w.(*responseWriter); ok {
			recordedResponse = rw.body.Bytes()
			recordedStatus = rw.statusCode
		}
	}

	// Test directly with responseWriter
	recorder := httptest.NewRecorder()
	rw := newResponseWriter(recorder)

	req := httptest.NewRequest("GET", "/test", nil)
	httpHandler(rw, req)

	// Verify response was recorded
	if recordedStatus != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, recordedStatus)
	}

	expectedResponse := `{"message": "regular http works"}`
	if string(recordedResponse) != expectedResponse {
		t.Errorf("Expected recorded response '%s', got '%s'", expectedResponse, recordedResponse)
	}

	// Verify it was also written to the underlying recorder
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected recorder status %d, got %d", http.StatusOK, recorder.Code)
	}

	if recorder.Body.String() != expectedResponse {
		t.Errorf("Expected recorder body '%s', got '%s'", expectedResponse, recorder.Body.String())
	}
}

// TestMixedTraffic tests both WebSocket and regular HTTP requests in the same server
func TestMixedTraffic(t *testing.T) {
	mux := http.NewServeMux()

	// Regular HTTP endpoint
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		rw := newResponseWriter(w)
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`{"status": "ok"}`))
	})

	// WebSocket endpoint
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		rw := newResponseWriter(w)

		conn, err := websocket.Accept(rw, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		})
		if err != nil {
			t.Errorf("WebSocket accept failed: %v", err)
			return
		}
		defer func() {
			_ = conn.Close(websocket.StatusNormalClosure, "done")
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		if err := conn.Write(ctx, websocket.MessageText, []byte("ws-ok")); err != nil {
			t.Errorf("WebSocket write failed: %v", err)
		}

		<-ctx.Done()
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Test regular HTTP request
	resp, err := http.Get(server.URL + "/api/test")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected HTTP status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// Test WebSocket request
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "test done")
	}()

	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("WebSocket read failed: %v", err)
	}

	if string(msg) != "ws-ok" {
		t.Errorf("Expected WebSocket message 'ws-ok', got '%s'", msg)
	}

	t.Log("Both HTTP and WebSocket requests worked successfully!")
}
