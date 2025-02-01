package database

import (
	"context"

	"go.uber.org/zap"
)

// BatchDeleteLeads deletes multiple leads in batches.
// It processes leads in batches of batchSize to avoid overwhelming the database.
// If an error occurs during batch processing, it will be logged and the operation
// will continue with the next batch.
func (db *Db) BatchDeleteLeads(ctx context.Context, leadIDs []uint64) error {
	var (
		lQop = db.QueryOperator.LeadORM
	)

	// validate the input
	if len(leadIDs) == 0 {
		return ErrInvalidInput
	}

    ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
    defer cancel()

    // Break the IDs into batches
    batches := BreakIntoBatches[uint64](leadIDs, batchSize)

    // Process each batch
    for i, batch := range batches {
		result, err := lQop.WithContext(ctx).Where(lQop.Id.In(batch...)).Delete()
        if err != nil {
			db.Logger.Error("failed to delete batch",
				zap.Error(err),
				zap.Int("batchNumber", i+1),
				zap.Int("batchSize", len(batch)),
			)
			continue
		}

		db.Logger.Info("successfully deleted batch",
			zap.Int("batchNumber", i+1),
			zap.Int("batchSize", len(batch)),
			zap.Int64("deletedCount", result.RowsAffected),
		)
	}

	return nil
} 