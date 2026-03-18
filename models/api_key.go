package models

import (
	"context"
	"errors"
	"net"
	"time"
)

// ErrAPIKeyNotFound is returned when an API key does not exist or has already been revoked.
var ErrAPIKeyNotFound = errors.New("api key not found")

// APIKey represents an API key for programmatic access
type APIKey struct {
	ID            string
	UserID        string
	Name          string
	LookupHash    string
	KeyHash       string
	KeySalt       []byte
	HashAlgorithm string
	KeyHintPrefix string
	KeyHintSuffix string
	LastUsedAt    *time.Time
	LastUsedIP    *net.IP
	UsageCount    int64
	CreatedAt     time.Time
	RevokedAt     *time.Time
	Scopes        []string
}

// APIKeyUsageLog represents an audit log entry for API key usage
type APIKeyUsageLog struct {
	ID          string
	APIKeyID    string
	UsedAt      time.Time
	IPAddress   net.IP
	Endpoint    string
	UserAgent   string
	CountryCode string
	City        string
	CreatedAt   time.Time
}

// APIKeyRepository manages API key operations
type APIKeyRepository interface {
	// Create inserts a new API key
	Create(ctx context.Context, apiKey *APIKey) error

	// GetByID retrieves an API key by ID
	GetByID(ctx context.Context, id string) (*APIKey, error)

	// GetByLookupHash retrieves an active API key by its lookup hash
	GetByLookupHash(ctx context.Context, lookupHash string) (*APIKey, error)

	// ListByUserID retrieves all API keys for a user (including revoked)
	ListByUserID(ctx context.Context, userID string) ([]*APIKey, error)

	// ListActiveByUserID retrieves all active (non-revoked) API keys for a user
	ListActiveByUserID(ctx context.Context, userID string) ([]*APIKey, error)

	// UpdateLastUsed updates the last used timestamp and IP for an API key
	UpdateLastUsed(ctx context.Context, id string, ipAddress net.IP) error

	// Revoke soft-deletes an API key by setting revoked_at
	Revoke(ctx context.Context, id string, ownerUserID string) error

	// LogUsage records an API key usage event
	LogUsage(ctx context.Context, log *APIKeyUsageLog) error
}
