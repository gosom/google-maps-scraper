package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/models"
	"github.com/gosom/google-maps-scraper/pkg/encryption"
)

type IntegrationRepository struct {
	db *sql.DB
}

func NewIntegrationRepository(db *sql.DB) *IntegrationRepository {
	return &IntegrationRepository{db: db}
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
		decrypted, err := encryption.Decrypt(string(i.AccessToken))
		if err != nil {
			if strings.Contains(err.Error(), "ENCRYPTION_KEY") {
				slog.Debug("encryption not configured, using plaintext access_token", slog.String("user_id", i.UserID), slog.String("provider", i.Provider))
			} else {
				slog.Error("failed to decrypt access_token, data may be corrupted or wrong key", slog.String("user_id", i.UserID), slog.String("provider", i.Provider), slog.String("error", err.Error()))
			}
		} else {
			i.AccessToken = []byte(decrypted)
		}
	}
	if len(i.RefreshToken) > 0 {
		decrypted, err := encryption.Decrypt(string(i.RefreshToken))
		if err != nil {
			if strings.Contains(err.Error(), "ENCRYPTION_KEY") {
				slog.Debug("encryption not configured, using plaintext refresh_token", slog.String("user_id", i.UserID), slog.String("provider", i.Provider))
			} else {
				slog.Error("failed to decrypt refresh_token, data may be corrupted or wrong key", slog.String("user_id", i.UserID), slog.String("provider", i.Provider), slog.String("error", err.Error()))
			}
		} else {
			i.RefreshToken = []byte(decrypted)
		}
	}

	return &i, nil
}

func (r *IntegrationRepository) Save(ctx context.Context, integration *models.UserIntegration) error {
	var encAccessToken, encRefreshToken []byte
	if len(integration.AccessToken) > 0 {
		encrypted, err := encryption.Encrypt(string(integration.AccessToken))
		if err != nil {
			return fmt.Errorf("encrypting access token: %w", err)
		}
		encAccessToken = []byte(encrypted)
	}
	if len(integration.RefreshToken) > 0 {
		encrypted, err := encryption.Encrypt(string(integration.RefreshToken))
		if err != nil {
			return fmt.Errorf("encrypting refresh token: %w", err)
		}
		encRefreshToken = []byte(encrypted)
	}

	query := `
		INSERT INTO user_integrations (user_id, provider, access_token, refresh_token, expiry, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, provider) DO UPDATE SET
			access_token = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token,
			expiry = EXCLUDED.expiry,
			updated_at = EXCLUDED.updated_at
		RETURNING id
	`

	err := r.db.QueryRowContext(ctx, query,
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

func (r *IntegrationRepository) Delete(ctx context.Context, userID, provider string) error {
	query := `DELETE FROM user_integrations WHERE user_id = $1 AND provider = $2`
	_, err := r.db.ExecContext(ctx, query, userID, provider)
	return err
}
