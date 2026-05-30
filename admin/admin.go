package admin

import (
	"context"
	"errors"
	"html/template"
	"time"

	"github.com/gosom/google-maps-scraper/ratelimit"
	"github.com/gosom/google-maps-scraper/rqueue"
)

type AppState struct {
	Store         IStore
	RateLimiter   ratelimit.Store
	Templates     *template.Template
	EncryptionKey []byte
	CookieName    string
	RQueueClient  *rqueue.Client
}

var (
	ErrUserExists       = errors.New("user already exists")
	ErrUserNotFound     = errors.New("user not found")
	ErrInvalidPassword  = errors.New("invalid password")
	ErrSessionNotFound  = errors.New("session not found")
	ErrSessionExpired   = errors.New("session expired")
	ErrResourceNotFound = errors.New("provisioned resource not found")
)

type IStore interface {
	// Config
	GetConfig(ctx context.Context, key string) (*AppConfig, error)
	SetConfig(ctx context.Context, cfg *AppConfig, encrypt bool) error
	DeleteConfig(ctx context.Context, key string) error

	// Users
	CreateUser(ctx context.Context, username, password string) (*User, error)
	GetUser(ctx context.Context, username string) (*User, error)
	GetUserByID(ctx context.Context, id int) (*User, error)
	UpdatePassword(ctx context.Context, username, password string) error

	// TOTP
	GetTOTPSecret(ctx context.Context, userID int) (string, error)
	SetTOTPSecret(ctx context.Context, userID int, secret string) error
	EnableTOTP(ctx context.Context, userID int) error
	DisableTOTP(ctx context.Context, userID int) error
	SetBackupCodes(ctx context.Context, userID int, hashedCodes string) error
	ValidateBackupCode(ctx context.Context, userID int, code string) (bool, error)

	// Sessions
	CreateSession(ctx context.Context, userID int, ipAddress, userAgent string, duration time.Duration) (*Session, error)
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteUserSessionsExcept(ctx context.Context, userID int, exceptSessionID string) error
	CleanupExpiredSessions(ctx context.Context) (int64, error)

	// API Keys
	CreateAPIKey(ctx context.Context, userID int, name, keyHash, keyPrefix string) (*APIKey, error)
	ListAPIKeys(ctx context.Context, userID int) ([]APIKey, error)
	RevokeAPIKey(ctx context.Context, userID int, keyID int) error

	// Provisioned Resources
	CreateProvisionedResource(ctx context.Context, res *ProvisionedResource) (*ProvisionedResource, error)
	GetProvisionedResource(ctx context.Context, id int) (*ProvisionedResource, error)
	ListProvisionedResources(ctx context.Context, provider string) ([]ProvisionedResource, error)
	UpdateProvisionedResourceStatus(ctx context.Context, id int, status, ipAddress string) error
	SoftDeleteProvisionedResource(ctx context.Context, id int) error
}

// AppConfig represents an application configuration setting.
type AppConfig struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// User represents an admin user.
type User struct {
	ID              int
	Username        string
	PasswordHash    string
	TOTPSecret      *string
	TOTPEnabled     bool
	BackupCodesHash *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Session represents an admin session.
type Session struct {
	ID        string
	UserID    int
	CreatedAt time.Time
	ExpiresAt time.Time
	IPAddress string
	UserAgent string
}

// APIKey represents an API key.
type APIKey struct {
	ID         int
	UserID     int
	Name       string
	KeyPrefix  string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// ProvisionedResource represents a cloud resource (e.g. a DigitalOcean droplet).
type ProvisionedResource struct {
	ID           int
	Provider     string
	ResourceType string
	ResourceID   string
	Name         string
	Region       string
	Size         string
	Status       string
	IPAddress    string
	Metadata     map[string]any
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}
