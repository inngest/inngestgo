package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/inngest/inngestgo/internal/sdkrequest"
)

// APIManager handles API function runs and step checkpointing
type APIManager interface {
	// CreateAPIRun creates a new API run in Inngest cloud
	CreateAPIRun(ctx context.Context, domain, endpoint, method string, input []byte, metadata map[string]interface{}) (string, error)
	// CheckpointStep sends step data to Inngest in the background
	CheckpointStep(ctx context.Context, runID string, step sdkrequest.GeneratorOpcode) error
	// StoreResult stores the final API response as the function result
	StoreResult(ctx context.Context, runID string, result APIResult) error
}

// APIResult represents the final result of an API function call
type APIResult struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body,omitempty"`
	Duration   time.Duration     `json:"duration"`
	Error      string            `json:"error,omitempty"`
}

// apiManager implements APIManager
type apiManager struct {
	baseURL    string
	signingKey string
	client     *http.Client
}

// NewAPIManager creates a new API manager for handling API function runs
func NewAPIManager(baseURL, signingKey string) APIManager {
	return &apiManager{
		baseURL:    baseURL,
		signingKey: signingKey,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CreateAPIRunRequest represents the request to create a new API run
type CreateAPIRunRequest struct {
	Domain   string                 `json:"domain"`
	Endpoint string                 `json:"endpoint"`
	Method   string                 `json:"method"`
	Input    json.RawMessage        `json:"input"`
	Metadata map[string]interface{} `json:"metadata"`
}

// CreateAPIRunResponse represents the response from creating an API run
type CreateAPIRunResponse struct {
	RunID string `json:"run_id"`
}

func (m *apiManager) CreateAPIRun(ctx context.Context, domain, endpoint, method string, input []byte, metadata map[string]interface{}) (string, error) {
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

func (m *apiManager) CheckpointStep(ctx context.Context, runID string, step sdkrequest.GeneratorOpcode) error {
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

func (m *apiManager) StoreResult(ctx context.Context, runID string, result APIResult) error {
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