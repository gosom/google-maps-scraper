package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/clerkinc/clerk-sdk-go/clerk"
	"github.com/gosom/google-maps-scraper/postgres"
)

// AuthMiddleware handles Clerk authentication and adds user info to the request context
type AuthMiddleware struct {
	client       clerk.Client
	userRepo     postgres.UserRepository
	usageLimiter postgres.UsageLimiter
}

// ContextKey is used to store user information in the request context
type ContextKey string

const (
	// UserIDKey is the context key for storing the user ID
	UserIDKey ContextKey = "user_id"
	// AuthHeaderName is the name of the authentication header
	AuthHeaderName = "Authorization"
)

// NewAuthMiddleware creates a new AuthMiddleware
func NewAuthMiddleware(clerkAPIKey string, userRepo postgres.UserRepository, usageLimiter postgres.UsageLimiter) (*AuthMiddleware, error) {
	client, err := clerk.NewClient(clerkAPIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create Clerk client: %w", err)
	}

	return &AuthMiddleware{
		client:       client,
		userRepo:     userRepo,
		usageLimiter: usageLimiter,
	}, nil
}

// Authenticate is the middleware function for authentication
func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get token from Authorization header
		authHeader := r.Header.Get(AuthHeaderName)
		if authHeader == "" {
			http.Error(w, "Unauthorized: missing authorization header", http.StatusUnauthorized)
			return
		}

		// Extract token from Bearer format
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Unauthorized: invalid authorization format", http.StatusUnauthorized)
			return
		}
		token := parts[1]

		// DEBUG: Log token verification attempt
		log.Printf("DEBUG: Attempting to verify token for request %s %s (first 20 chars: %s...)", r.Method, r.URL.Path,
			func() string {
				if len(token) > 20 {
					return token[:20]
				}
				return token
			}())

		// Verify token with Clerk
		claims, err := m.client.VerifyToken(token)
		if err != nil {
			log.Printf("DEBUG: Token verification failed for %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		log.Printf("DEBUG: Token verification successful for user %s on %s %s", claims.Subject, r.Method, r.URL.Path)

		// Get user ID from verified claims
		userID := claims.Subject
		if userID == "" {
			http.Error(w, "Unauthorized: invalid user claims", http.StatusUnauthorized)
			return
		}

		// Check if user exists in our database
		user, err := m.userRepo.GetByID(r.Context(), userID)
		if err != nil {
			// If user doesn't exist, get their email and create them
			clerkUser, err := m.client.Users().Read(userID)
			if err != nil {
				http.Error(w, "Failed to retrieve user information", http.StatusInternalServerError)
				return
			}

			// Get the primary email
			var email string
			// Handle potential nil PrimaryEmailAddressID
			if clerkUser.PrimaryEmailAddressID != nil {
				primaryID := *clerkUser.PrimaryEmailAddressID
				for _, emailAddr := range clerkUser.EmailAddresses {
					if emailAddr.ID == primaryID {
						email = emailAddr.EmailAddress
						break
					}
				}
			} else if len(clerkUser.EmailAddresses) > 0 {
				// Fallback to first email if no primary is set
				email = clerkUser.EmailAddresses[0].EmailAddress
			}

			if email == "" {
				http.Error(w, "User has no email address", http.StatusBadRequest)
				return
			}

			// Create a new user in our database
			user = postgres.User{
				ID:    userID,
				Email: email,
			}
			err = m.userRepo.Create(r.Context(), &user)
			if err != nil {
				http.Error(w, "Failed to create user record", http.StatusInternalServerError)
				return
			}
		}

		// Add user ID to request context
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CheckUsageLimit is middleware to enforce usage limits
func (m *AuthMiddleware) CheckUsageLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip usage check for GET requests
		if r.Method == http.MethodGet {
			log.Printf("DEBUG: Skipping usage check for GET request %s %s", r.Method, r.URL.Path)
			next.ServeHTTP(w, r)
			return
		}

		// Get user ID from context
		userID, ok := r.Context().Value(UserIDKey).(string)
		if !ok {
			log.Printf("DEBUG: User not authenticated in CheckUsageLimit for %s %s", r.Method, r.URL.Path)
			http.Error(w, "Unauthorized: user not authenticated", http.StatusUnauthorized)
			return
		}

		log.Printf("DEBUG: Checking usage limit for user %s on %s %s", userID, r.Method, r.URL.Path)

		// Check if user has reached their limit
		allowed, err := m.usageLimiter.CheckLimit(r.Context(), userID)
		if err != nil {
			log.Printf("DEBUG: Error checking usage limit for user %s: %v", userID, err)
			http.Error(w, "Failed to check usage limit", http.StatusInternalServerError)
			return
		}

		log.Printf("DEBUG: Usage limit check result for user %s: allowed=%v", userID, allowed)

		if !allowed {
			// Get current usage to provide better error message
			usage, err := m.usageLimiter.GetUsage(r.Context(), userID)
			if err != nil {
				log.Printf("DEBUG: Failed to get usage details for limit exceeded message: %v", err)
				http.Error(w, "Usage limit reached. Try again tomorrow.", http.StatusTooManyRequests)
				return
			}
			errorMsg := fmt.Sprintf("Daily usage limit reached. You have used %d jobs today. Try again tomorrow or contact support.", usage.JobCount)
			log.Printf("DEBUG: Usage limit exceeded for user %s: %s", userID, errorMsg)
			http.Error(w, errorMsg, http.StatusTooManyRequests)
			return
		}

		log.Printf("DEBUG: Usage limit check passed for user %s, proceeding with request", userID)
		// User is under their limit, proceed
		next.ServeHTTP(w, r)
	})
}

// GetUserID extracts the user ID from the request context
func GetUserID(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(UserIDKey).(string)
	if !ok {
		return "", errors.New("user not authenticated")
	}
	return userID, nil
}

// IncrementUsage increases the usage count for a user
func (m *AuthMiddleware) IncrementUsage(ctx context.Context) error {
	userID, err := GetUserID(ctx)
	if err != nil {
		return err
	}
	return m.usageLimiter.IncrementUsage(ctx, userID)
}
