package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/inngest/inngestgo/api"
	"github.com/inngest/inngestgo/step"
)

func main() {
	// Create Inngest API middleware
	middleware := api.NewMiddleware(api.MiddlewareOpts{
		BaseURL:    "https://api.inngest.com", // or your Inngest instance URL
		SigningKey: "your-signing-key",        // TODO: Load from environment
		AppID:      "my-api-app",
		Domain:     "api.mycompany.com",
	})

	// Create HTTP server with step tooling
	http.HandleFunc("/users", middleware.Handler(handleUsers))
	http.HandleFunc("/orders", middleware.Handler(handleOrders))

	fmt.Println("API server with Inngest step tooling running on :8080")
	fmt.Println("Try: curl -X POST http://localhost:8080/users -d '{\"email\":\"user@example.com\"}'")
	http.ListenAndServe(":8080", nil)
}

// handleUsers demonstrates API function with step tooling
func handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Step 1: Authenticate (with full observability)
	auth, err := step.Run(r.Context(), "authenticate", func(ctx context.Context) (*AuthResult, error) {
		// Simulate auth check
		time.Sleep(50 * time.Millisecond)
		return &AuthResult{
			UserID: "user_123",
			Valid:  true,
		}, nil
	})
	if err != nil {
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
		return
	}

	// Step 2: Validate user data
	validation, err := step.Run(r.Context(), "validate-user", func(ctx context.Context) (*ValidationResult, error) {
		// Simulate validation
		if req.Email == "" {
			return nil, fmt.Errorf("email is required")
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
	user, err := step.Run(r.Context(), "create-user", func(ctx context.Context) (*User, error) {
		// Simulate database insert
		time.Sleep(100 * time.Millisecond)
		return &User{
			ID:    "user_" + fmt.Sprintf("%d", time.Now().Unix()),
			Email: req.Email,
			Name:  req.Name,
		}, nil
	})
	if err != nil {
		http.Error(w, "Failed to create user", http.StatusInternalServerError)
		return
	}

	// Step 4: Send welcome email (this could trigger background jobs)
	_, err = step.Run(r.Context(), "send-welcome-email", func(ctx context.Context) (*EmailResult, error) {
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
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(CreateUserResponse{
		User:    *user,
		AuthID:  auth.UserID,
		Valid:   validation.Valid,
		Message: "User created successfully",
	})
}

// handleOrders demonstrates another API endpoint with step tooling
func handleOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Step 1: Process payment
	payment, err := step.Run(r.Context(), "process-payment", func(ctx context.Context) (*PaymentResult, error) {
		// Simulate payment processing with retries built-in
		time.Sleep(300 * time.Millisecond)
		return &PaymentResult{
			TransactionID: "txn_123",
			Amount:        9999, // $99.99
			Status:        "completed",
		}, nil
	})
	if err != nil {
		http.Error(w, "Payment failed", http.StatusPaymentRequired)
		return
	}

	// Step 2: Create order
	order, err := step.Run(r.Context(), "create-order", func(ctx context.Context) (*Order, error) {
		return &Order{
			ID:            "order_" + payment.TransactionID,
			TransactionID: payment.TransactionID,
			Amount:        payment.Amount,
			Status:        "confirmed",
		}, nil
	})
	if err != nil {
		http.Error(w, "Order creation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
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

type PaymentResult struct {
	TransactionID string `json:"transaction_id"`
	Amount        int    `json:"amount"`
	Status        string `json:"status"`
}

type Order struct {
	ID            string `json:"id"`
	TransactionID string `json:"transaction_id"`
	Amount        int    `json:"amount"`
	Status        string `json:"status"`
}