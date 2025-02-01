package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// ListScrapingJobs retrieves a list of scraping jobs with pagination
func (db *Db) ListScrapingJobs(ctx context.Context, limit, offset int) ([]*lead_scraper_servicev1.ScrapingJob, error) {
	if limit <= 0 {
		limit = 10 // default limit
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	var jobsORM []lead_scraper_servicev1.ScrapingJobORM
	result := db.Client.Engine.WithContext(ctx).Limit(limit).Offset(offset).Find(&jobsORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list scraping jobs: %w", result.Error)
	}

	jobs := make([]*lead_scraper_servicev1.ScrapingJob, 0, len(jobsORM))
	for _, jobORM := range jobsORM {
		job, err := jobORM.ToPB(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
		}
		jobs = append(jobs, &job)
	}

	return jobs, nil
} 