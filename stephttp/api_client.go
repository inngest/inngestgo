package stephttp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/internal/sdkrequest"
	"github.com/oklog/ulid/v2"
)

// checkpointAPI handles API function runs and step checkpointing
type checkpointAPI interface {
	CheckpointNewRun(ctx context.Context, input NewAPIRunData) error
	CheckpointSteps(ctx context.Context, runID ulid.ULID, steps []sdkrequest.GeneratorOpcode) error
	CheckpointResponse(ctx context.Context, runID ulid.ULID, result APIResult) error
}

// NewAPIRunRequest represents the entire request payload used to create new
// API-based runs.
type CheckpointNewRunRequest struct {
	// Seed allows us to construct a deterministic run ID from this data and the
	// event TS.
	Seed string `json:"seed"`

	// Idempotency allows the customization of an idempotency key, allowing us to
	// handle API idempotency using Inngest.
	Idempotency string `json:"idempotency"`

	// Event embeds the key request information which is used as the triggering
	// event for API-based runs.
	Event inngestgo.GenericEvent[NewAPIRunData] `json:"event"`
}

// NewAPIRunData represents event data stored and used to create new API-based
// runs.
type NewAPIRunData struct {
	// Domain is the domain that served the incoming request.
	Domain string `json:"domain"`
	// Method is the incoming request method.  This is used for RESTful
	// API endpoints.
	Method string `json:"method"`
	// Path is the path for the incoming request.
	Path string `json:"path"` // request path
	// Fn is the optional function slug.  If not present, this is created
	// using a combination of the method and the path: "POST /v1/runs"
	Fn string `json:"fn"`

	// IP is the IP that created the request.
	IP string `json:"ip"` // incoming IP
	// ContentType is the content type for the request.
	ContentType string `json:"content_type"`
	// QueryParams are the query parameters for the request, as a single string
	// without the leading "?".
	//
	// NOTE: This is optional;  we do not require that users store the query params
	// for every request, as this may contain data that users choose not to log.
	QueryParams string `json:"query_params"`
	// Body is the incoming request body.
	//
	// NOTE: This is optional;  we do not require that users store the body for
	// every request, as this may contain data that users choose not to log.
	Body json.RawMessage `json:"body"`
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
