package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/encryption"
)

type IntegrationRepository struct {
	db  *sql.DB
	enc *encryption.Encryptor // nil means no encryption
}

func NewIntegrationRepository(db *sql.DB, enc *encryption.Encryptor) *IntegrationRepository {
	return &IntegrationRepository{db: db, enc: enc}
}

func (r *IntegrationRepository) Get(ctx context.Context, userID, provider string) (*models.UserIntegration, error) {
	query := `
		SELECT id, user_id, provider, access_token, refresh_token, expiry, created_at, updated_at
		FROM user_integrations
		WHERE user_id = $1 AND provider = $2
	`

	var i models.UserIntegration
	err := r.db.QueryRowContext(ctx, query, userID, provider).Scan(
		&i.ID,
		&i.UserID,
		&i.Provider,
		&i.AccessToken,
		&i.RefreshToken,
		&i.Expiry,
		&i.CreatedAt,
		&i.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrNotFound
		}
		return nil, err
	}

	if len(i.AccessToken) > 0 {
		decrypted, err := r.decryptToken(i.AccessToken)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt integration access token: %w", err)
		}
		i.AccessToken = []byte(decrypted)
	}
	if len(i.RefreshToken) > 0 {
		decrypted, err := r.decryptToken(i.RefreshToken)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt integration refresh token: %w", err)
		}
		i.RefreshToken = []byte(decrypted)
	}

	return &i, nil
}

// decryptToken attempts to decrypt the token. If no encryptor is configured,
// or if the value appears to be valid UTF-8 plaintext (legacy fallback),
// it returns the value as-is.
func (r *IntegrationRepository) decryptToken(token []byte) (string, error) {
	if r.enc == nil {
		return string(token), nil
	}
	decrypted, err := r.enc.Decrypt(string(token))
	if err != nil {
		// Legacy plaintext fallback: if decryption fails and the stored value
		// is valid UTF-8, assume it was stored before encryption was enabled.
		if utf8.Valid(token) {
			return string(token), nil
		}
		return "", err
	}
	return decrypted, nil
}

func (r *IntegrationRepository) Save(ctx context.Context, integration *models.UserIntegration) error {
	var encAccessToken, encRefreshToken []byte
	if len(integration.AccessToken) > 0 {
		encrypted, err := r.encryptToken(string(integration.AccessToken))
		if err != nil {
			return fmt.Errorf("encrypting access token: %w", err)
		}
		encAccessToken = []byte(encrypted)
	}
	if len(integration.RefreshToken) > 0 {
		encrypted, err := r.encryptToken(string(integration.RefreshToken))
		if err != nil {
			return fmt.Errorf("encrypting refresh token: %w", err)
		}
		encRefreshToken = []byte(encrypted)
	}

	query := `
		INSERT INTO user_integrations (id, user_id, provider, access_token, refresh_token, expiry, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, provider) DO UPDATE SET
			access_token = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expiry = EXCLUDED.expiry,
			updated_at = EXCLUDED.updated_at
		RETURNING id
	`

	err := r.db.QueryRowContext(ctx, query,
		uuid.Must(uuid.NewV7()).String(),
		integration.UserID,
		integration.Provider,
		encAccessToken,
		encRefreshToken,
		integration.Expiry,
		integration.CreatedAt,
		time.Now(),
	).Scan(&integration.ID)

	return err
}

// encryptToken encrypts the token if an encryptor is available,
// otherwise stores it as plaintext.
func (r *IntegrationRepository) encryptToken(plaintext string) (string, error) {
	if r.enc == nil {
		return plaintext, nil
	}
	return r.enc.Encrypt(plaintext)
}

func (r *IntegrationRepository) Delete(ctx context.Context, userID, provider string) error {
	query := `DELETE FROM user_integrations WHERE user_id = $1 AND provider = $2`
	_, err := r.db.ExecContext(ctx, query, userID, provider)
	return err
}
