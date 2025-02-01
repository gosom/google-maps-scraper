package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"go.uber.org/zap"
)

// BatchUpdateScrapingJobs updates multiple scraping jobs in a single batch operation.
// It processes jobs in batches to avoid overwhelming the database.
// If an error occurs during batch processing, it will be logged and the operation
// will continue with the next batch.
func (db *Db) BatchUpdateScrapingJobs(ctx context.Context, jobs []*lead_scraper_servicev1.ScrapingJob) ([]*lead_scraper_servicev1.ScrapingJob, error) {
	if len(jobs) == 0 {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// Start a transaction
	tx := db.Client.Engine.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", tx.Error)
	}

	batches := BreakIntoBatches[*lead_scraper_servicev1.ScrapingJob](jobs, batchSize)
	resultingJobs := make([]*lead_scraper_servicev1.ScrapingJob, 0, len(jobs))

	// Process each batch
	for _, batch := range batches {
		updatedJobs, err := lead_scraper_servicev1.DefaultPatchSetScrapingJob(ctx, batch, nil, tx)
		if err != nil {
			db.Logger.Error("failed to update batch",
				zap.Error(err),
				zap.Int("batchSize", len(batch)),
			)
			continue
		}
		resultingJobs = append(resultingJobs, updatedJobs...)
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		db.Logger.Error("failed to commit transaction",
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return resultingJobs, nil
} 