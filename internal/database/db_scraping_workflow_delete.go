package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// DeleteScrapingWorkflow deletes a scraping workflow by ID
func (db *Db) DeleteScrapingWorkflow(ctx context.Context, id uint64) error {
	if id == 0 {
		return ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	result := db.Client.Engine.WithContext(ctx).Where("id = ?", id).Delete(&lead_scraper_servicev1.ScrapingWorkflowORM{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete scraping workflow: %w", result.Error)
	}

	return nil
} 