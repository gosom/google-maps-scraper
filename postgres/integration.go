package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gosom/google-maps-scraper/models"
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

	return &i, nil
}

func (r *IntegrationRepository) Save(ctx context.Context, integration *models.UserIntegration) error {
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
		integration.AccessToken,
		integration.RefreshToken,
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
