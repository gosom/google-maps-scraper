package database

import (
	"context"
	"fmt"
)

// DeleteScrapingWorkflow deletes a scraping workflow by ID
func (db *Db) DeleteScrapingWorkflow(ctx context.Context, id uint64) error {
	var (
		swQop = db.QueryOperator.ScrapingWorkflowORM

	)

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	if id == 0 {
		return ErrInvalidInput
	}

	
	// check and ensure the scraping workflow exists
	swORM, err := swQop.WithContext(ctx).Where(swQop.Id.Eq(id)).First()
	if err != nil {
		return fmt.Errorf("workflow does not exist: %w", err)
	}

	if swORM == nil {
		return fmt.Errorf("workflow does not exist: %w", ErrWorkflowDoesNotExist)
	}

	result, err := swQop.WithContext(ctx).Where(swQop.Id.Eq(id)).Delete()
	if err != nil {
		return fmt.Errorf("failed to delete scraping workflow: %w", err)
	}

	if result.RowsAffected == 0 {
		return fmt.Errorf("scraping workflow not found: %w", ErrWorkflowDoesNotExist)
	}

	return nil
} 