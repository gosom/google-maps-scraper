// Package ratelimit provides rate limiting functionality with pluggable storage backends.
package ratelimit

import (
	"context"
	"errors"
	"time"
)

// ErrRateLimited is returned when a request exceeds the rate limit.
var ErrRateLimited = errors.New("rate limit exceeded")

// Result contains the outcome of a rate limit check.
type Result struct {
	// Allowed indicates whether the request is permitted.
	Allowed bool
	// Remaining is the number of requests remaining in the current window.
	Remaining int
	// ResetAt is when the current rate limit window resets.
	ResetAt time.Time
}

// Store defines the interface for rate limit storage backends.
type Store interface {
	// Check increments the counter for the given key and returns whether
	// the request is allowed based on the maximum requests per window.
	//
	// Parameters:
	//   - key: unique identifier for the rate limit (e.g., "login:username")
	//   - max: maximum number of requests allowed in the window
	//   - window: duration of the rate limit window
	//
	// Returns a Result indicating if the request is allowed, remaining
	// attempts, and when the window resets.
	Check(ctx context.Context, key string, limit int, window time.Duration) (Result, error)

	// Reset clears the rate limit for the given key, allowing immediate
	// access again. Useful after successful authentication.
	Reset(ctx context.Context, key string) error
}
