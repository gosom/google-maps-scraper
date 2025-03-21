package models

import (
	"context"
	"time"
)

// User represents a registered user in the system
type User struct {
	ID        string
	Email     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserRepository manages user operations
type UserRepository interface {
	GetByID(ctx context.Context, id string) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	Create(ctx context.Context, user *User) error
	Delete(ctx context.Context, id string) error
}

// UserUsage represents a user's usage of the system
type UserUsage struct {
	ID          int
	UserID      string
	JobCount    int
	LastJobDate time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UsageLimiter manages usage limits for users
type UsageLimiter interface {
	// CheckLimit verifies if a user has reached their usage limit
	CheckLimit(ctx context.Context, userID string) (bool, error)

	// IncrementUsage increases a user's usage count
	IncrementUsage(ctx context.Context, userID string) error

	// GetUsage retrieves a user's current usage
	GetUsage(ctx context.Context, userID string) (UserUsage, error)
}