package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// UpdateScrapingWorkflow updates an existing scraping workflow
func (db *Db) UpdateScrapingWorkflow(ctx context.Context, workflow *lead_scraper_servicev1.ScrapingWorkflow) (*lead_scraper_servicev1.ScrapingWorkflow, error) {
	if workflow == nil || workflow.Id == 0 {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// convert to ORM model
	workflowORM, err := workflow.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	// update the workflow
	result := db.Client.Engine.WithContext(ctx).Where("id = ?", workflow.Id).Updates(&workflowORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to update scraping workflow: %w", result.Error)
	}

	// get the updated record
	if result := db.Client.Engine.WithContext(ctx).Where("id = ?", workflow.Id).First(&workflowORM); result.Error != nil {
		return nil, fmt.Errorf("failed to get updated scraping workflow: %w", result.Error)
	}

	pbResult, err := workflowORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
} 