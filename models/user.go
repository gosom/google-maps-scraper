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
