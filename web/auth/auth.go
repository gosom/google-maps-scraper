package auth

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/user"
	"github.com/gosom/google-maps-scraper/postgres"
)

// AuthMiddleware handles Clerk authentication and adds user info to the request context
type AuthMiddleware struct {
	userAPI  *user.Client
	userRepo postgres.UserRepository
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
func NewAuthMiddleware(clerkAPIKey string, userRepo postgres.UserRepository) (*AuthMiddleware, error) {
	// Configure Clerk SDK with the provided secret key
	clerk.SetKey(clerkAPIKey)

	return &AuthMiddleware{
		userAPI: user.NewClient(&clerk.ClientConfig{
			BackendConfig: clerk.BackendConfig{
				Key: clerk.String(clerkAPIKey),
			},
		}),
		userRepo: userRepo,
	}, nil
}

// Authenticate is the middleware function for authentication
func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	extractToken := func(r *http.Request) string {
		// Prefer Authorization header
		authHeader := strings.TrimSpace(r.Header.Get(AuthHeaderName))
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}
		// Fallback to __session cookie (used by Clerk)
		if sessionCookie, err := r.Cookie("__session"); err == nil && sessionCookie.Value != "" {
			return sessionCookie.Value
		}
		return ""
	}

	// Clerk middleware: verify token (header or cookie) and attach session claims
	clerkAuth := clerkhttp.RequireHeaderAuthorization(
		clerkhttp.AuthorizationJWTExtractor(extractToken),
	)

	return clerkAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Retrieve verified session claims from context
		claims, ok := clerk.SessionClaimsFromContext(r.Context())
		if !ok || claims == nil || claims.Subject == "" {
			http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
			return
		}

		userID := claims.Subject

		// Check if user exists in our database
		userRecord, err := m.userRepo.GetByID(r.Context(), userID)
		if err != nil {
			// If user doesn't exist, fetch from Clerk and create locally
			clerkUser, err := m.userAPI.Get(r.Context(), userID)
			if err != nil {
				log.Printf("ERROR: Failed to retrieve user %s from Clerk: %v", userID, err)
				http.Error(w, "Failed to retrieve user information", http.StatusInternalServerError)
				return
			}

			// Get the primary email
			var email string
			if clerkUser.PrimaryEmailAddressID != nil {
				primaryID := *clerkUser.PrimaryEmailAddressID
				for _, emailAddr := range clerkUser.EmailAddresses {
					if emailAddr.ID == primaryID {
						email = emailAddr.EmailAddress
						break
					}
				}
			} else if len(clerkUser.EmailAddresses) > 0 {
				email = clerkUser.EmailAddresses[0].EmailAddress
			}

			if email == "" {
				log.Printf("ERROR: User %s has no email address in Clerk", userID)
				http.Error(w, "User has no email address", http.StatusBadRequest)
				return
			}

			userRecord = postgres.User{
				ID:    userID,
				Email: email,
			}
			if err := m.userRepo.Create(r.Context(), &userRecord); err != nil {
				log.Printf("ERROR: Failed to create user %s in local database: %v", userID, err)
				http.Error(w, "Failed to create user record: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Add user ID to request context
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}))
}

// GetUserID extracts the user ID from the request context
func GetUserID(ctx context.Context) (string, error) {
	userID, ok := ctx.Value(UserIDKey).(string)
	if !ok {
		return "", errors.New("user not authenticated")
	}
	return userID, nil
}
