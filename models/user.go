package models

import (
	"context"
	"time"
)

// Role constants for RBAC.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// User represents a registered user in the system
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserRepository manages user operations
type UserRepository interface {
	GetByID(ctx context.Context, id string) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	Create(ctx context.Context, user *User) error
	Delete(ctx context.Context, id string) error
}
