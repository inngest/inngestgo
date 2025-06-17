package stephttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/inngest/inngestgo/internal/sdkrequest"
)

// syncRunAPI handles API function runs and step checkpointing
type syncRunAPI interface {
	// CreateAPIRun creates a new API run in Inngest cloud
	CreateAPIRun(ctx context.Context, domain, endpoint, method string, input []byte, metadata map[string]any) (string, error)
	// CheckpointStep sends step data to Inngest in the background
	CheckpointStep(ctx context.Context, runID string, step sdkrequest.GeneratorOpcode) error
	// StoreResult stores the final API response as the function result
	StoreResult(ctx context.Context, runID string, result APIResult) error
}

// APIResult represents the final result of an API function call
type APIResult struct {
	// StatusCode represents the status code for the API result
	StatusCode int `json:"status_code"`
	// Headers represents any response headers sent in the server response
	Headers map[string]string `json:"headers"`
	// Body represents the API response.  This may be nil by default.  It is only
	// captured when you manually specify that you want to track the result.
	Body []byte `json:"body,omitempty"`
	// Duration represents the duration
	Duration time.Duration `json:"duration"`
	// Error represents any error from the API.  This is only for internal errors,
	// eg. when a step permanently fails
	Error string `json:"error,omitempty"`
}

// syncClient implements the API interface
type syncClient struct {
	baseURL    string
	signingKey string
	client     *http.Client
}

// NewAPIManager creates a new API manager for handling API function runs.  The base URL is
// the URL endpoint for Inngest's API, eg "https://api.inngest.com".
func NewAPIManager(baseURL, env, signingKey string) syncRunAPI {
	return &syncClient{
		baseURL:    baseURL,
		signingKey: signingKey,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CreateAPIRunRequest represents the request to create a new API run
type CreateAPIRunRequest struct {
	Domain   string          `json:"domain"`
	Endpoint string          `json:"endpoint"`
	Method   string          `json:"method"`
	Input    json.RawMessage `json:"input"`
	Metadata map[string]any  `json:"metadata"`
}

// CreateAPIRunResponse represents the response from creating an API run
type CreateAPIRunResponse struct {
	RunID string `json:"run_id"`
}

func (m *syncClient) CreateAPIRun(ctx context.Context, domain, endpoint, method string, input []byte, metadata map[string]any) (string, error) {
	req := CreateAPIRunRequest{
		Domain:   domain,
		Endpoint: endpoint,
		Method:   method,
		Input:    json.RawMessage(input),
		Metadata: metadata,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal create run request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", m.baseURL+"/v1/api-runs", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.signingKey)

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to create API run: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("API run creation failed with status %d", resp.StatusCode)
	}

	var createResp CreateAPIRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", fmt.Errorf("failed to decode create run response: %w", err)
	}

	return createResp.RunID, nil
}

// CheckpointStepRequest represents a step checkpoint request
type CheckpointStepRequest struct {
	RunID string                     `json:"run_id"`
	Step  sdkrequest.GeneratorOpcode `json:"step"`
}

func (m *syncClient) CheckpointStep(ctx context.Context, runID string, step sdkrequest.GeneratorOpcode) error {
	req := CheckpointStepRequest{
		RunID: runID,
		Step:  step,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint request: %w", err)
	}

	// Send checkpoint in background to avoid blocking the API response
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		httpReq, err := http.NewRequestWithContext(ctx, "POST", m.baseURL+"/v1/api-runs/checkpoint", bytes.NewReader(reqBody))
		if err != nil {
			// TODO: Add proper logging
			return
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+m.signingKey)

		resp, err := m.client.Do(httpReq)
		if err != nil {
			// TODO: Add proper logging
			return
		}
		defer resp.Body.Close()

		// TODO: Add proper logging for non-200 responses
	}()

	return nil
}

// StoreResultRequest represents a request to store the final API result
type StoreResultRequest struct {
	RunID  string    `json:"run_id"`
	Result APIResult `json:"result"`
}

func (m *syncClient) StoreResult(ctx context.Context, runID string, result APIResult) error {
	req := StoreResultRequest{
		RunID:  runID,
		Result: result,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal store result request: %w", err)
	}

	// Send result storage in background
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		httpReq, err := http.NewRequestWithContext(ctx, "POST", m.baseURL+"/v1/api-runs/result", bytes.NewReader(reqBody))
		if err != nil {
			// TODO: Add proper logging
			return
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+m.signingKey)

		resp, err := m.client.Do(httpReq)
		if err != nil {
			// TODO: Add proper logging
			return
		}
		defer resp.Body.Close()

		// TODO: Add proper logging for non-200 responses
	}()

	return nil
}
