package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// CreateScrapingWorkflow creates a new scraping workflow in the database
func (db *Db) CreateScrapingWorkflow(ctx context.Context, workflow *lead_scraper_servicev1.ScrapingWorkflow) (*lead_scraper_servicev1.ScrapingWorkflow, error) {
	if workflow == nil {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// validate the workflow
	if err := workflow.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate scraping workflow: %w", err)
	}

	// convert to ORM model
	workflowORM, err := workflow.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	// create the workflow
	result := db.Client.Engine.WithContext(ctx).Create(&workflowORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to create scraping workflow: %w", result.Error)
	}

	// convert back to protobuf
	pbResult, err := workflowORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
} 