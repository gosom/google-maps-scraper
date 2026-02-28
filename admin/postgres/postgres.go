package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/gosom/google-maps-scraper/admin"
	"github.com/gosom/google-maps-scraper/cryptoext"
)

var _ admin.IStore = (*store)(nil)

type store struct {
	db            *pgxpool.Pool
	encryptionKey []byte
}

func New(ctx context.Context, dsn string, encryptionKey []byte) (admin.IStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}

	return &store{
		db:            pool,
		encryptionKey: encryptionKey,
	}, nil
}

// NewWithPool creates a store from an existing connection pool.
func NewWithPool(db *pgxpool.Pool, encryptionKey []byte) admin.IStore {
	return &store{
		db:            db,
		encryptionKey: encryptionKey,
	}
}

func (s *store) SetConfig(ctx context.Context, cfg *admin.AppConfig, encrypt bool) error {
	value := cfg.Value
	if encrypt {
		encrypted, err := cryptoext.Encrypt(value, s.encryptionKey)
		if err != nil {
			return err
		}

		value = encrypted
	}

	cfg.UpdatedAt = time.Now().UTC()

	_, err := s.db.Exec(ctx,
		`INSERT INTO app_config (key, value, encrypted, updated_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (key) DO UPDATE SET value = $2, encrypted = $3, updated_at = $4`,
		cfg.Key, value, encrypt, cfg.UpdatedAt,
	)

	return err
}

func (s *store) GetConfig(ctx context.Context, key string) (*admin.AppConfig, error) {
	var cfg admin.AppConfig

	var encrypted bool

	err := s.db.QueryRow(ctx,
		`SELECT key, value, encrypted, updated_at FROM app_config WHERE key = $1`,
		key,
	).Scan(&cfg.Key, &cfg.Value, &encrypted, &cfg.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	if encrypted {
		decrypted, err := cryptoext.Decrypt(cfg.Value, s.encryptionKey)
		if err != nil {
			return nil, err
		}

		cfg.Value = decrypted
	}

	return &cfg, nil
}

func (s *store) DeleteConfig(ctx context.Context, key string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM app_config WHERE key = $1`, key)
	return err
}

func (s *store) CreateUser(ctx context.Context, username, password string) (*admin.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	var user admin.User

	err = s.db.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2)
		 RETURNING id, username, password_hash, created_at, updated_at`,
		username, string(hash),
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if err.Error() == `ERROR: duplicate key value violates unique constraint "users_username_key" (SQLSTATE 23505)` {
			return nil, admin.ErrUserExists
		}

		return nil, err
	}

	return &user, nil
}

func (s *store) GetUser(ctx context.Context, username string) (*admin.User, error) {
	var user admin.User

	err := s.db.QueryRow(ctx,
		`SELECT id, username, password_hash, totp_secret, totp_enabled, backup_codes_hash, created_at, updated_at FROM users WHERE username = $1`,
		username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.TOTPSecret, &user.TOTPEnabled, &user.BackupCodesHash, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, admin.ErrUserNotFound
		}

		return nil, err
	}

	return &user, nil
}

// GetUserByID retrieves a user by ID.
func (s *store) GetUserByID(ctx context.Context, id int) (*admin.User, error) {
	var user admin.User

	err := s.db.QueryRow(ctx,
		`SELECT id, username, password_hash, totp_secret, totp_enabled, backup_codes_hash, created_at, updated_at FROM users WHERE id = $1`,
		id,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.TOTPSecret, &user.TOTPEnabled, &user.BackupCodesHash, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, admin.ErrUserNotFound
		}

		return nil, err
	}

	return &user, nil
}

// UpdatePassword updates the password for an existing user.
func (s *store) UpdatePassword(ctx context.Context, username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	result, err := s.db.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE username = $2`,
		string(hash), username,
	)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return admin.ErrUserNotFound
	}

	return nil
}

// GetTOTPSecret retrieves and decrypts the TOTP secret for a user.
func (s *store) GetTOTPSecret(ctx context.Context, userID int) (string, error) {
	var encrypted *string

	err := s.db.QueryRow(ctx,
		`SELECT totp_secret FROM users WHERE id = $1`,
		userID,
	).Scan(&encrypted)
	if err != nil {
		return "", err
	}

	if encrypted == nil || *encrypted == "" {
		return "", nil
	}

	return cryptoext.Decrypt(*encrypted, s.encryptionKey)
}

// SetTOTPSecret sets the TOTP secret for a user (encrypted).
func (s *store) SetTOTPSecret(ctx context.Context, userID int, secret string) error {
	encrypted, err := cryptoext.Encrypt(secret, s.encryptionKey)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx,
		`UPDATE users SET totp_secret = $1, updated_at = NOW() WHERE id = $2`,
		encrypted, userID,
	)

	return err
}

// EnableTOTP enables TOTP for a user.
func (s *store) EnableTOTP(ctx context.Context, userID int) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET totp_enabled = TRUE, updated_at = NOW() WHERE id = $1`,
		userID,
	)

	return err
}

// DisableTOTP disables TOTP for a user and removes the secret.
func (s *store) DisableTOTP(ctx context.Context, userID int) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET totp_enabled = FALSE, totp_secret = NULL, backup_codes_hash = NULL, updated_at = NOW() WHERE id = $1`,
		userID,
	)

	return err
}

// SetBackupCodes saves hashed backup codes for a user.
func (s *store) SetBackupCodes(ctx context.Context, userID int, hashedCodes string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET backup_codes_hash = $1, updated_at = NOW() WHERE id = $2`,
		hashedCodes, userID,
	)

	return err
}

// ValidateBackupCode checks if a backup code is valid and removes it if so.
// Uses a transaction with row-level locking to prevent race conditions.
func (s *store) ValidateBackupCode(ctx context.Context, userID int, code string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}

	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the row to prevent concurrent modifications
	var hashesStr *string

	err = tx.QueryRow(ctx,
		`SELECT backup_codes_hash FROM users WHERE id = $1 FOR UPDATE`,
		userID,
	).Scan(&hashesStr)
	if err != nil {
		return false, err
	}

	if hashesStr == nil || *hashesStr == "" {
		return false, nil
	}

	codeHash := sha256.Sum256([]byte(code))
	codeHashStr := hex.EncodeToString(codeHash[:])

	hashes := strings.Split(*hashesStr, ",")
	found := -1

	for i, h := range hashes {
		if h == codeHashStr {
			found = i
			break
		}
	}

	if found == -1 {
		return false, nil
	}

	hashes = append(hashes[:found], hashes[found+1:]...)
	newHashes := hashes
	newHashesStr := strings.Join(newHashes, ",")

	_, err = tx.Exec(ctx,
		`UPDATE users SET backup_codes_hash = $1, updated_at = NOW() WHERE id = $2`,
		newHashesStr, userID,
	)
	if err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}

	return true, nil
}

// CreateSession creates a new session for a user.
func (s *store) CreateSession(ctx context.Context, userID int, ipAddress, userAgent string, duration time.Duration) (*admin.Session, error) {
	sessionID, _ := cryptoext.GenerateRandomHexString(32)
	expiresAt := time.Now().UTC().Add(duration)

	var session admin.Session

	err := s.db.QueryRow(ctx,
		`INSERT INTO admin_sessions (id, user_id, expires_at, ip_address, user_agent)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, user_id, created_at, expires_at, ip_address, user_agent`,
		sessionID, userID, expiresAt, ipAddress, userAgent,
	).Scan(&session.ID, &session.UserID, &session.CreatedAt, &session.ExpiresAt, &session.IPAddress, &session.UserAgent)
	if err != nil {
		return nil, err
	}

	return &session, nil
}

// GetSession retrieves and validates a session.
func (s *store) GetSession(ctx context.Context, sessionID string) (*admin.Session, error) {
	var session admin.Session

	err := s.db.QueryRow(ctx,
		`SELECT id, user_id, created_at, expires_at, ip_address, user_agent
		 FROM admin_sessions WHERE id = $1`,
		sessionID,
	).Scan(&session.ID, &session.UserID, &session.CreatedAt, &session.ExpiresAt, &session.IPAddress, &session.UserAgent)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, admin.ErrSessionNotFound
		}

		return nil, err
	}

	if time.Now().After(session.ExpiresAt) {
		_ = s.DeleteSession(ctx, sessionID)
		return nil, admin.ErrSessionExpired
	}

	return &session, nil
}

// DeleteSession deletes a session.
func (s *store) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM admin_sessions WHERE id = $1`, sessionID)
	return err
}

// DeleteUserSessionsExcept deletes all sessions for a user except the specified one.
// Used to invalidate other sessions after a password change.
func (s *store) DeleteUserSessionsExcept(ctx context.Context, userID int, exceptSessionID string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM admin_sessions WHERE user_id = $1 AND id != $2`,
		userID, exceptSessionID,
	)

	return err
}

// CleanupExpiredSessions removes all expired sessions from the database.
func (s *store) CleanupExpiredSessions(ctx context.Context) (int64, error) {
	result, err := s.db.Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at < NOW()`)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected(), nil
}

// CreateAPIKey inserts a new API key record.
func (s *store) CreateAPIKey(ctx context.Context, userID int, name, keyHash, keyPrefix string) (*admin.APIKey, error) {
	var key admin.APIKey

	err := s.db.QueryRow(ctx,
		`INSERT INTO api_keys (user_id, name, key_hash, key_prefix)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, name, key_prefix, created_at, last_used_at, revoked_at`,
		userID, name, keyHash, keyPrefix,
	).Scan(&key.ID, &key.UserID, &key.Name, &key.KeyPrefix, &key.CreatedAt, &key.LastUsedAt, &key.RevokedAt)
	if err != nil {
		return nil, err
	}

	return &key, nil
}

// ListAPIKeys returns all API keys for a user.
func (s *store) ListAPIKeys(ctx context.Context, userID int) ([]admin.APIKey, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, user_id, name, key_prefix, created_at, last_used_at, revoked_at
		 FROM api_keys WHERE user_id = $1
		 ORDER BY created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []admin.APIKey

	for rows.Next() {
		var key admin.APIKey
		if err := rows.Scan(&key.ID, &key.UserID, &key.Name, &key.KeyPrefix, &key.CreatedAt, &key.LastUsedAt, &key.RevokedAt); err != nil {
			return nil, err
		}

		keys = append(keys, key)
	}

	return keys, rows.Err()
}

// RevokeAPIKey soft-deletes an API key by setting revoked_at.
func (s *store) RevokeAPIKey(ctx context.Context, userID, keyID int) error {
	result, err := s.db.Exec(ctx,
		`UPDATE api_keys SET revoked_at = NOW() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		keyID, userID,
	)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return errors.New("api key not found or already revoked")
	}

	return nil
}

// CreateProvisionedResource inserts a new provisioned resource record.
func (s *store) CreateProvisionedResource(ctx context.Context, res *admin.ProvisionedResource) (*admin.ProvisionedResource, error) {
	metadata, err := json.Marshal(res.Metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	var out admin.ProvisionedResource

	var metadataBytes []byte

	err = s.db.QueryRow(ctx,
		`INSERT INTO provisioned_resources (provider, resource_type, resource_id, name, region, size, status, ip_address, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, provider, resource_type, resource_id, name, region, size, status, ip_address, metadata, created_at, updated_at, deleted_at`,
		res.Provider, res.ResourceType, res.ResourceID, res.Name, res.Region, res.Size, res.Status, res.IPAddress, metadata,
	).Scan(&out.ID, &out.Provider, &out.ResourceType, &out.ResourceID, &out.Name, &out.Region, &out.Size, &out.Status, &out.IPAddress, &metadataBytes, &out.CreatedAt, &out.UpdatedAt, &out.DeletedAt)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(metadataBytes, &out.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &out, nil
}

// GetProvisionedResource retrieves a provisioned resource by ID.
func (s *store) GetProvisionedResource(ctx context.Context, id int) (*admin.ProvisionedResource, error) {
	var res admin.ProvisionedResource

	var metadataBytes []byte

	err := s.db.QueryRow(ctx,
		`SELECT id, provider, resource_type, resource_id, name, region, size, status, ip_address, metadata, created_at, updated_at, deleted_at
		 FROM provisioned_resources WHERE id = $1`,
		id,
	).Scan(&res.ID, &res.Provider, &res.ResourceType, &res.ResourceID, &res.Name, &res.Region, &res.Size, &res.Status, &res.IPAddress, &metadataBytes, &res.CreatedAt, &res.UpdatedAt, &res.DeletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, admin.ErrResourceNotFound
		}

		return nil, err
	}

	if err := json.Unmarshal(metadataBytes, &res.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &res, nil
}

// ListProvisionedResources returns all non-deleted provisioned resources, optionally filtered by provider.
func (s *store) ListProvisionedResources(ctx context.Context, provider string) ([]admin.ProvisionedResource, error) {
	var rows pgx.Rows

	var err error

	if provider != "" {
		rows, err = s.db.Query(ctx,
			`SELECT id, provider, resource_type, resource_id, name, region, size, status, ip_address, metadata, created_at, updated_at, deleted_at
			 FROM provisioned_resources WHERE deleted_at IS NULL AND provider = $1
			 ORDER BY created_at DESC`,
			provider,
		)
	} else {
		rows, err = s.db.Query(ctx,
			`SELECT id, provider, resource_type, resource_id, name, region, size, status, ip_address, metadata, created_at, updated_at, deleted_at
			 FROM provisioned_resources WHERE deleted_at IS NULL
			 ORDER BY created_at DESC`,
		)
	}

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var resources []admin.ProvisionedResource

	for rows.Next() {
		var res admin.ProvisionedResource

		var metadataBytes []byte

		if err := rows.Scan(&res.ID, &res.Provider, &res.ResourceType, &res.ResourceID, &res.Name, &res.Region, &res.Size, &res.Status, &res.IPAddress, &metadataBytes, &res.CreatedAt, &res.UpdatedAt, &res.DeletedAt); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(metadataBytes, &res.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}

		resources = append(resources, res)
	}

	return resources, rows.Err()
}

// UpdateProvisionedResourceStatus updates the status and IP address of a provisioned resource.
func (s *store) UpdateProvisionedResourceStatus(ctx context.Context, id int, status, ipAddress string) error {
	result, err := s.db.Exec(ctx,
		`UPDATE provisioned_resources SET status = $1, ip_address = $2, updated_at = NOW() WHERE id = $3 AND deleted_at IS NULL`,
		status, ipAddress, id,
	)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return admin.ErrResourceNotFound
	}

	return nil
}

// SoftDeleteProvisionedResource marks a provisioned resource as deleted.
func (s *store) SoftDeleteProvisionedResource(ctx context.Context, id int) error {
	result, err := s.db.Exec(ctx,
		`UPDATE provisioned_resources SET deleted_at = NOW(), status = 'deleted', updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`,
		id,
	)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return admin.ErrResourceNotFound
	}

	return nil
}
