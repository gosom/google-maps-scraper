package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// DeleteAPIKey deletes an API key by ID
func (db *Db) DeleteAPIKey(ctx context.Context, id uint64) error {
	if id == 0 {
		return ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	result := db.Client.Engine.WithContext(ctx).Where("id = ?", id).Delete(&lead_scraper_servicev1.APIKeyORM{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete API key: %w", result.Error)
	}

	return nil
}