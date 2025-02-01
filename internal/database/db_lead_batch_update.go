package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"go.uber.org/zap"
)

// BatchUpdateLeads updates multiple leads in a single batch operation.
// It processes leads in batches of 100 to avoid overwhelming the database.
// If an error occurs during batch processing, it will be logged and the operation
// will continue with the next batch.
func (db *Db) BatchUpdateLeads(ctx context.Context, leads []*lead_scraper_servicev1.Lead) ([]*lead_scraper_servicev1.Lead, error) {
	if len(leads) == 0 {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// Start a transaction
	tx := db.Client.Engine.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", tx.Error)
	}

	batches := BreakIntoBatches[*lead_scraper_servicev1.Lead](leads, batchSize)
	resultingLeads := make([]*lead_scraper_servicev1.Lead, 0, len(leads))

	// Process each batch
	for _, batch := range batches {
		updatedLeads, err := lead_scraper_servicev1.DefaultPatchSetLead(ctx, batch, nil, tx)
		if err != nil {
			db.Logger.Error("failed to update batch",
				zap.Error(err),
				zap.Int("batchSize", len(batch)),
			)
			continue
		}
		resultingLeads = append(resultingLeads, updatedLeads...)
	}

	return resultingLeads, nil
} 