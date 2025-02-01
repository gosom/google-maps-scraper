package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// ListScrapingWorkflows retrieves a list of scraping workflows with pagination
func (db *Db) ListScrapingWorkflows(ctx context.Context, limit, offset int) ([]*lead_scraper_servicev1.ScrapingWorkflow, error) {
	if limit <= 0 {
		limit = 10 // default limit
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	var workflowsORM []lead_scraper_servicev1.ScrapingWorkflowORM
	result := db.Client.Engine.WithContext(ctx).Order("id asc").Limit(limit).Offset(offset).Find(&workflowsORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list scraping workflows: %w", result.Error)
	}

	workflows := make([]*lead_scraper_servicev1.ScrapingWorkflow, 0, len(workflowsORM))
	for _, workflowORM := range workflowsORM {
		workflow, err := workflowORM.ToPB(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
		}
		workflows = append(workflows, &workflow)
	}

	return workflows, nil
} 