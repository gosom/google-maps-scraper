package database

import (
	"context"
	"fmt"
)

// DeleteScrapingJob deletes a scraping job by ID
func (db *Db) DeleteScrapingJob(ctx context.Context, id uint64) error {
	var (
		sQop = db.QueryOperator.ScrapingJobORM
	)

	if id == 0 {
		return ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	if _, err := sQop.WithContext(ctx).Where(sQop.Id.Eq(id)).Delete(); err != nil {
		return fmt.Errorf("failed to delete scraping job: %w", err)
	}

	return nil
} 