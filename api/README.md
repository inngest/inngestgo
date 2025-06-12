# Inngest API Functions

This package enables using Inngest's step tooling within synchronous API handlers, providing full observability and tracing for your HTTP endpoints.

## Features

- **Full step observability**: Every `step.Run()` call is automatically traced and logged
- **Background checkpointing**: Step data is sent to Inngest in the background without blocking your API
- **Seamless integration**: Use existing `step.Run()` functions in API handlers
- **APM out of the box**: Get metrics, traces, and monitoring for every API endpoint
- **Event triggering**: Send events from API handlers that trigger async workflows

## Usage

### Basic Setup

```go
package main

import (
    "net/http"
    "github.com/inngest/inngestgo/api"
)

func main() {
    // Create Inngest API middleware
    middleware := api.NewMiddleware(api.MiddlewareOpts{
        BaseURL:    "https://api.inngest.com",
        SigningKey: "your-signing-key",
        AppID:      "my-api-app", 
        Domain:     "api.mycompany.com",
    })

    // Wrap your HTTP handlers
    http.HandleFunc("/users", middleware.Handler(handleUsers))
    http.ListenAndServe(":8080", nil)
}
```

### Using Steps in API Handlers

```go
func handleUsers(w http.ResponseWriter, r *http.Request) {
    // Each step.Run() is automatically tracked and traced
    auth, err := step.Run(r.Context(), "authenticate", func(ctx context.Context) (*AuthResult, error) {
        return validateToken(r.Header.Get("Authorization"))
    })
    if err != nil {
        http.Error(w, "Unauthorized", 401)
        return
    }

    user, err := step.Run(r.Context(), "create-user", func(ctx context.Context) (*User, error) {
        return createUserInDB(auth.UserID, userData)
    })
    if err != nil {
        http.Error(w, "Failed to create user", 500)
        return
    }

    // Send events to trigger async workflows
    step.SendEvent(r.Context(), inngest.Event{
        Name: "user.created",
        Data: map[string]interface{}{
            "user_id": user.ID,
            "email":   user.Email,
        },
    })

    json.NewEncoder(w).Encode(user)
}
```

## How It Works

1. **Middleware Setup**: The middleware creates a new "API run" in Inngest for each HTTP request
2. **Step Execution**: Each `step.Run()` executes immediately (synchronous) but checkpoints data in the background
3. **Observability**: All step data, timing, and results are sent to Inngest for full APM
4. **Tracing**: Request → Steps → Events → Background Functions are all linked in the Inngest dashboard

## Differences from Async Functions

| Aspect | Async Functions | API Functions |
|--------|----------------|---------------|
| Execution | Steps pause execution, function resumes later | Steps execute immediately, continue to next step |
| Step Mode | `StepModeReturn` | `StepModeContinue` |
| Checkpointing | Synchronous (blocks until stored) | Asynchronous (background) |
| Error Handling | Function stops, retries later | HTTP error response |
| Use Case | Background workflows, queues | Real-time API responses |

## Configuration

```go
type MiddlewareOpts struct {
    BaseURL    string // Inngest API URL
    SigningKey string // Your Inngest signing key  
    AppID      string // Application identifier
    Domain     string // Your API domain for grouping
}
```

## Examples

See `examples/api/main.go` for a complete working example with user creation and order processing endpoints.