package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// CreateScrapingJob creates a new scraping job in the database
func (db *Db) CreateScrapingJob(ctx context.Context, job *lead_scraper_servicev1.ScrapingJob) (*lead_scraper_servicev1.ScrapingJob, error) {
	if job == nil {
		return nil, ErrInvalidInput
	}

	// ensure the db operation executes within the specified timeout
	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// validate the job
	if err := job.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate scraping job: %w", err)
	}

	// convert to ORM model
	jobORM, err := job.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	// create the job
	result := db.Client.Engine.WithContext(ctx).Create(&jobORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to create scraping job: %w", result.Error)
	}

	// convert back to protobuf
	pbResult, err := jobORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
} 