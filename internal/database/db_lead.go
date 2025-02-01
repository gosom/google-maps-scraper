package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"go.uber.org/zap"
	"gorm.io/gen/field"
)

const (
	batchSize = 500
)

// CreateLead creates a new lead in the database
func (db *Db) CreateLead(ctx context.Context, scrapingJobID uint64, lead *lead_scraper_servicev1.Lead) (*lead_scraper_servicev1.Lead, error) {
	var (
		sQop = db.QueryOperator.ScrapingJobORM
	)
	
	if lead == nil {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// ensure the scraping job exists
	scrapingJob, err := db.GetScrapingJob(ctx, scrapingJobID)
	if err != nil {
		return nil, fmt.Errorf("failed to get scraping job: %w", err)
	}

	// convert to ORM
	scrapingJobORM, err := scrapingJob.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	// convert to ORM model
	leadORM, err := lead.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	if err := sQop.Leads.WithContext(ctx).Model(&scrapingJobORM).Append(&leadORM); err != nil {
		return nil, fmt.Errorf("failed to append lead to scraping job: %w", err)
	}

	// save the scraping job
	if _, err := sQop.WithContext(ctx).Updates(&scrapingJobORM); err != nil {
		return nil, fmt.Errorf("failed to save scraping job: %w", err)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to save scraping job: %w", err)
	}

	// convert back to protobuf
	pbResult, err := leadORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
}

// GetLead retrieves a lead by ID
func (db *Db) GetLead(ctx context.Context, id uint64) (*lead_scraper_servicev1.Lead, error) {
	var (
		lQop = db.QueryOperator.LeadORM
	)

	if id == 0 {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	var leadORM lead_scraper_servicev1.LeadORM
	if _, err := lQop.
		WithContext(ctx).
		Where(lQop.Id.Eq(id)).
		Preload(lQop.RegularHours).
		Preload(lQop.SpecialHours).
		First(); err != nil {
		return nil, fmt.Errorf("failed to get lead: %w", err)
	}

	pbResult, err := leadORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
}

// UpdateLead updates an existing lead
func (db *Db) UpdateLead(ctx context.Context, lead *lead_scraper_servicev1.Lead) (*lead_scraper_servicev1.Lead, error) {
	if lead == nil || lead.Id == 0 {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	var (
		lQop = db.QueryOperator.LeadORM
	)

	// convert to ORM model
	leadORM, err := lead.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	// update the lead
	if _, err := lQop.WithContext(ctx).Where(lQop.Id.Eq(lead.Id)).Updates(&leadORM); err != nil {
		return nil, fmt.Errorf("failed to update lead: %w", err)
	}

	// get the updated record
	if _, err := lQop.WithContext(ctx).Where(lQop.Id.Eq(lead.Id)).First(); err != nil {
		return nil, fmt.Errorf("failed to get updated lead: %w", err)
	}

	pbResult, err := leadORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
}

// DeleteLead deletes a lead by ID
func (db *Db) DeleteLead(ctx context.Context, id uint64, deletionType DeletionType) error {
	var (
		lQop = db.QueryOperator.LeadORM
	)

	if id == 0 {
		return ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	queryRef := lQop.WithContext(ctx)
	if deletionType == DeletionTypeSoft {
		queryRef = queryRef.Where(lQop.Id.Eq(id)).Select(field.AssociationFields)
	} else {
		queryRef = queryRef.Where(lQop.Id.Eq(id)).Unscoped().Select(field.AssociationFields)
	}

	if _, err := queryRef.Delete(); err != nil {
		return fmt.Errorf("failed to delete lead: %w", err)
	}

	return nil
}

// ListLeads retrieves a list of leads with pagination
func (db *Db) ListLeads(ctx context.Context, limit, offset int) ([]*lead_scraper_servicev1.Lead, error) {
	// validate the input
	if limit <= 0 {
		limit = 10 // default limit
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	var leadsORM []lead_scraper_servicev1.LeadORM
	result := db.Client.Engine.WithContext(ctx).Limit(limit).Offset(offset).Find(&leadsORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list leads: %w", result.Error)
	}

	leads := make([]*lead_scraper_servicev1.Lead, 0, len(leadsORM))
	for _, leadORM := range leadsORM {
		lead, err := leadORM.ToPB(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
		}
		leads = append(leads, &lead)
	}

	return leads, nil
}

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

// BreakIntoBatches splits a slice of any type into smaller batches of the specified size.
// Type parameter T can be any type (uint64, string, custom structs, etc.)
func BreakIntoBatches[T any](items []T, batchSize int) [][]T {
    if batchSize <= 0 {
        batchSize = 1 // Ensure minimum batch size of 1
    }

    numBatches := (len(items) + batchSize - 1) / batchSize
    batches := make([][]T, 0, numBatches)

    for i := 0; i < len(items); i += batchSize {
        end := i + batchSize
        if end > len(items) {
            end = len(items)
        }
        batches = append(batches, items[i:end])
    }

    return batches
}