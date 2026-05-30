package api

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const apiKeyContextKey contextKey = "api_key"

// KeyInfo represents minimal API key information stored in context.
type KeyInfo struct {
	ID   int
	Name string
}

// ValidateKeyFunc is a function that validates an API key and returns key info.
type ValidateKeyFunc func(ctx context.Context, key string) (keyID int, keyName string, err error)

// KeyAuth middleware validates API keys from Authorization or X-API-Key headers.
func KeyAuth(validateKey ValidateKeyFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				// Also check X-API-Key header
				authHeader = r.Header.Get("X-API-Key")
			}

			if authHeader == "" {
				http.Error(w, `{"error": "missing api key"}`, http.StatusUnauthorized)
				return
			}

			// Extract the key from "Bearer <key>" or just "<key>"
			key := strings.TrimPrefix(authHeader, "Bearer ")

			keyID, keyName, err := validateKey(r.Context(), key)
			if err != nil {
				http.Error(w, `{"error": "invalid api key"}`, http.StatusUnauthorized)
				return
			}

			apiKeyInfo := &KeyInfo{ID: keyID, Name: keyName}
			ctx := context.WithValue(r.Context(), apiKeyContextKey, apiKeyInfo)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// KeyFromContext retrieves the API key info from context.
func KeyFromContext(ctx context.Context) *KeyInfo {
	if key, ok := ctx.Value(apiKeyContextKey).(*KeyInfo); ok {
		return key
	}

	return nil
}
