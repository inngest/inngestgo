package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/inngest/inngestgo/step"
	"github.com/inngest/inngestgo/stephttp"
)

func main() {
	// Create Inngest API middleware
	provider := stephttp.Setup(stephttp.SetupOpts{
		// SigningKey: "your-signing-key", // TODO: Load from environment, remove from here.
		Domain: "api.mycompany.com",
	})

	// Create HTTP server with step tooling
	http.HandleFunc("/users", provider.ServeHTTP(handleUsers))

	fmt.Println("API server with Inngest step tooling running on :8080")
	fmt.Println("Try: curl -X POST http://localhost:8080/users -d '{\"email\":\"user@example.com\"}'")
	_ = http.ListenAndServe(":8080", nil)
}

// handleUsers demonstrates API function with step tooling
func handleUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// stephttp.Function(
	// 	stephttp.Config{
	// 		Slug: "/users/{id}",
	// 	},
	// 	func () {
	// 		// everything in the API is in this func.
	// 	},
	// })

	// stephttp.OmitBody()
	// stephttp.SetRetries(ctx, 10)
	// stephttp.SetFnSlug(ctx, "/users/{id}")
	// stephttp.SetAsyncResponse(ctx, stephttp.AsyncRedirect|stephttp.AsyncToken|stephttp.Custom)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Step 1: Authenticate (with full observability)
	auth, err := step.Run(ctx, "authenticate", func(ctx context.Context) (*AuthResult, error) {
		var req CreateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}

		// Simulate auth check
		time.Sleep(50 * time.Millisecond)
		return &AuthResult{
			UserID: "user_123",
			Email:  req.Email,
			Name:   req.Name,
			Valid:  true,
		}, nil
	})
	if err != nil {
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
		return
	}

	step.Run(ctx, "whatever", func(ctx context.Context) (any, error) {
		return "yea", nil
	})

	// step.Sleep(ctx, "sleep", time.Second)

	// Step 2: Validate user data
	validation, err := step.Run(ctx, "validate-user", func(ctx context.Context) (*ValidationResult, error) {
		// Simulate validation
		time.Sleep(5 * time.Millisecond)
		if auth.UserID == "" {
			return nil, fmt.Errorf("user ID is required")
		}
		return &ValidationResult{
			Valid:  true,
			Errors: nil,
		}, nil
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Validation failed: %v", err), http.StatusBadRequest)
		return
	}

	// Step 3: Create user in database
	user, err := step.Run(ctx, "create-user", func(ctx context.Context) (*User, error) {
		// Simulate database insert
		time.Sleep(100 * time.Millisecond)
		return &User{
			ID:    "user_" + fmt.Sprintf("%d", time.Now().Unix()),
			Email: auth.Email,
			Name:  auth.Name,
		}, nil
	})
	if err != nil {
		http.Error(w, "Failed to create user", http.StatusInternalServerError)
		return
	}

	// Step 4: Send welcome email (this could trigger background jobs)
	_, err = step.Run(ctx, "send-welcome-email", func(ctx context.Context) (*EmailResult, error) {
		// This step is fully traced and can trigger background events
		// step.SendEvent() could be called here to trigger async workflows
		time.Sleep(200 * time.Millisecond)
		return &EmailResult{
			Sent:      true,
			MessageID: "msg_" + user.ID,
		}, nil
	})
	if err != nil {
		// Email failure doesn't fail the API - log it but continue
		fmt.Printf("Warning: Failed to send welcome email: %v\n", err)
	}

	// Response
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(CreateUserResponse{
		User:    *user,
		AuthID:  auth.UserID,
		Valid:   validation.Valid,
		Message: "User created successfully",
	})
}

// Request/Response types
type CreateUserRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

type CreateUserResponse struct {
	User    User   `json:"user"`
	AuthID  string `json:"auth_id"`
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
}

type AuthResult struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Valid  bool   `json:"valid"`
}

type ValidationResult struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type EmailResult struct {
	Sent      bool   `json:"sent"`
	MessageID string `json:"message_id"`
}
