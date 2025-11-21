package checkpoint

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/inngest/inngest/pkg/enums"
	"github.com/inngest/inngestgo/internal/opcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Checkpoint_Success(t *testing.T) {
	var receivedAuthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	// Set INNGEST_DEV to use our test server
	t.Setenv("INNGEST_DEV", server.URL)

	client := NewClient("primary-key", "fallback-key")
	client.httpClient = server.Client()

	req := AsyncRequest{
		RunID:        "run-123",
		FnID:         uuid.New(),
		QueueItemRef: "qi-456",
		Steps: []opcode.Step{
			{
				Op: enums.OpcodeStepRun,
				ID: "step-1",
			},
		},
	}

	err := client.Checkpoint(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, "Bearer primary-key", receivedAuthHeader)
	assert.False(t, client.useFallback.Load())
}

func TestClient_Checkpoint_FallbackOnAuth(t *testing.T) {
	var callCount atomic.Int32
	var receivedAuthHeaders []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		receivedAuthHeaders = append(receivedAuthHeaders, authHeader)
		count := callCount.Add(1)

		if count == 1 {
			// First call with primary key - fail with 401
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}

		// Second call with fallback key - succeed
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	// Set INNGEST_DEV to use our test server
	t.Setenv("INNGEST_DEV", server.URL)

	client := NewClient("primary-key", "fallback-key")
	client.httpClient = server.Client()

	req := AsyncRequest{
		RunID:        "run-123",
		FnID:         uuid.New(),
		QueueItemRef: "qi-456",
		Steps: []opcode.Step{
			{
				Op: enums.OpcodeStepRun,
				ID: "step-1",
			},
		},
	}

	err := client.Checkpoint(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, int32(2), callCount.Load(), "should have made exactly 2 calls")
	assert.Len(t, receivedAuthHeaders, 2, "should have received 2 auth headers")
	assert.Equal(t, "Bearer primary-key", receivedAuthHeaders[0])
	assert.Equal(t, "Bearer fallback-key", receivedAuthHeaders[1])
	assert.True(t, client.useFallback.Load(), "should have switched to fallback")
}

func TestClient_Checkpoint_BothKeysFail(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		// Always fail with 401
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	// Set INNGEST_DEV to use our test server
	t.Setenv("INNGEST_DEV", server.URL)

	client := NewClient("primary-key", "fallback-key")
	client.httpClient = server.Client()

	req := AsyncRequest{
		RunID:        "run-123",
		FnID:         uuid.New(),
		QueueItemRef: "qi-456",
		Steps:        []opcode.Step{},
	}

	err := client.Checkpoint(context.Background(), req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Equal(t, int32(2), callCount.Load(), "should have tried both keys")
	assert.True(t, client.useFallback.Load(), "should have switched to fallback")
}

func TestClient_Checkpoint_NoFallbackKey(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		// Fail with 401
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	// Set INNGEST_DEV to use our test server
	t.Setenv("INNGEST_DEV", server.URL)

	// Create client with no fallback key
	client := NewClient("primary-key", "")
	client.httpClient = server.Client()

	req := AsyncRequest{
		RunID:        "run-123",
		FnID:         uuid.New(),
		QueueItemRef: "qi-456",
		Steps:        []opcode.Step{},
	}

	err := client.Checkpoint(context.Background(), req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Equal(t, int32(1), callCount.Load(), "should have tried only once")
	assert.False(t, client.useFallback.Load(), "should not have switched to fallback")
}

func TestClient_Checkpoint_Non401Error(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		// Fail with 500 (not 401)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	// Set INNGEST_DEV to use our test server
	t.Setenv("INNGEST_DEV", server.URL)

	client := NewClient("primary-key", "fallback-key")
	client.httpClient = server.Client()

	req := AsyncRequest{
		RunID:        "run-123",
		FnID:         uuid.New(),
		QueueItemRef: "qi-456",
		Steps:        []opcode.Step{},
	}

	err := client.Checkpoint(context.Background(), req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Equal(t, int32(1), callCount.Load(), "should not retry on non-401 errors")
	assert.False(t, client.useFallback.Load(), "should not have switched to fallback")
}

func TestClient_Checkpoint_FallbackAlreadyActive(t *testing.T) {
	var callCount atomic.Int32
	var receivedAuthHeaders []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		receivedAuthHeaders = append(receivedAuthHeaders, authHeader)
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	// Set INNGEST_DEV to use our test server
	t.Setenv("INNGEST_DEV", server.URL)

	client := NewClient("primary-key", "fallback-key")
	client.httpClient = server.Client()

	// Manually activate fallback mode
	client.useFallback.Store(true)

	req := AsyncRequest{
		RunID:        "run-123",
		FnID:         uuid.New(),
		QueueItemRef: "qi-456",
		Steps:        []opcode.Step{},
	}

	err := client.Checkpoint(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, int32(1), callCount.Load(), "should make only one call")
	assert.Len(t, receivedAuthHeaders, 1)
	assert.Equal(t, "Bearer fallback-key", receivedAuthHeaders[0], "should use fallback key from the start")
}

func TestClient_Checkpoint_ValidRequestBody(t *testing.T) {
	var receivedBody AsyncRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewDecoder(r.Body).Decode(&receivedBody)
		require.NoError(t, err)

		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	// Set INNGEST_DEV to use our test server
	t.Setenv("INNGEST_DEV", server.URL)

	client := NewClient("primary-key", "fallback-key")
	client.httpClient = server.Client()

	fnID := uuid.New()
	req := AsyncRequest{
		RunID:        "run-123",
		FnID:         fnID,
		QueueItemRef: "qi-456",
		Steps: []opcode.Step{
			{
				Op: enums.OpcodeStepRun,
				ID: "step-1",
			},
			{
				Op: enums.OpcodeStepRun,
				ID: "step-2",
			},
		},
	}

	err := client.Checkpoint(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, "run-123", receivedBody.RunID)
	assert.Equal(t, fnID, receivedBody.FnID)
	assert.Equal(t, "qi-456", receivedBody.QueueItemRef)
	assert.Len(t, receivedBody.Steps, 2)
	assert.Equal(t, "step-1", receivedBody.Steps[0].ID)
	assert.Equal(t, "step-2", receivedBody.Steps[1].ID)
}
