package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// UpdateScrapingJob updates an existing scraping job
func (db *Db) UpdateScrapingJob(ctx context.Context, job *lead_scraper_servicev1.ScrapingJob) (*lead_scraper_servicev1.ScrapingJob, error) {
	if job == nil || job.Id == 0 {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// convert to ORM model
	jobORM, err := job.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	// update the job
	result := db.Client.Engine.WithContext(ctx).Where("id = ?", job.Id).Updates(&jobORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to update scraping job: %w", result.Error)
	}

	// get the updated record
	if result := db.Client.Engine.WithContext(ctx).Where("id = ?", job.Id).First(&jobORM); result.Error != nil {
		return nil, fmt.Errorf("failed to get updated scraping job: %w", result.Error)
	}

	pbResult, err := jobORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
} 