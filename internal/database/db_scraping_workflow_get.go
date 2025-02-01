package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// GetScrapingWorkflow retrieves a scraping workflow by ID
func (db *Db) GetScrapingWorkflow(ctx context.Context, id uint64) (*lead_scraper_servicev1.ScrapingWorkflow, error) {
	if id == 0 {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	var workflowORM lead_scraper_servicev1.ScrapingWorkflowORM
	result := db.Client.Engine.WithContext(ctx).Where("id = ?", id).First(&workflowORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to get scraping workflow: %w", result.Error)
	}

	pbResult, err := workflowORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
} 