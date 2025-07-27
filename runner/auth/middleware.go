// Package auth provides authentication middleware for the API.
package auth

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// minInt returns the smaller of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}

	return b
}

// ErrorResponse represents an API error response.
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// BearerTokenMiddleware creates a middleware that validates bearer tokens.
// If apiKey is empty, authentication is disabled and all requests pass through.
func BearerTokenMiddleware(apiKey string) func(http.Handler) http.Handler {
	// Log authentication status on startup
	if apiKey == "" {
		log.Println("🔓 Authentication DISABLED - API key not configured")
	} else {
		log.Printf("🔐 Authentication ENABLED - API key configured (%d chars)", len(apiKey))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If no API key is configured, skip authentication
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			// Extract Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				log.Printf("❌ AUTH FAILED - Missing token: %s %s", r.Method, r.URL.Path)
				SendUnauthorized(w, "Missing authentication token")

				return
			}

			// Validate Bearer token format
			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				log.Printf("❌ AUTH FAILED - Invalid format: %s %s", r.Method, r.URL.Path)
				SendUnauthorized(w, "Invalid authentication token format")

				return
			}

			// Compare token with configured API key
			token := parts[1]
			if token != apiKey {
				log.Printf("❌ AUTH FAILED - Invalid token: %s %s (token: %s...)", r.Method, r.URL.Path, token[:minInt(8, len(token))])
				SendUnauthorized(w, "Invalid authentication token")

				return
			}

			// Authentication successful, proceed to next handler
			log.Printf("✅ AUTH SUCCESS: %s %s", r.Method, r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}

// SendUnauthorized sends a 401 Unauthorized response with JSON error.
func SendUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)

	errResp := ErrorResponse{
		Code:    http.StatusUnauthorized,
		Message: message,
	}

	_ = json.NewEncoder(w).Encode(errResp)
}
