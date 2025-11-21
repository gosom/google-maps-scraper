package models

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

// UserIntegration represents an external service integration for a user
type UserIntegration struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Provider     string    `json:"provider"`
	AccessToken  []byte    `json:"-"` // Stored encrypted
	RefreshToken []byte    `json:"-"` // Stored encrypted
	Expiry       time.Time `json:"expiry"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IntegrationRepository manages user integration operations
type IntegrationRepository interface {
	Get(ctx context.Context, userID, provider string) (*UserIntegration, error)
	Save(ctx context.Context, integration *UserIntegration) error
	Delete(ctx context.Context, userID, provider string) error
}
