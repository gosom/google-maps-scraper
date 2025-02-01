package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// DeleteScrapingJob deletes a scraping job by ID
func (db *Db) DeleteScrapingJob(ctx context.Context, id string) error {
	if id == "" {
		return ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	result := db.Client.Engine.WithContext(ctx).Where("id = ?", id).Delete(&lead_scraper_servicev1.ScrapingJobORM{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete scraping job: %w", result.Error)
	}

	return nil
} 