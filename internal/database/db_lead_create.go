package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
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
	scrapingJob, err := sQop.WithContext(ctx).Where(sQop.Id.Eq(scrapingJobID)).First()
	if err != nil {
		return nil, fmt.Errorf("failed to get scraping job: %w", err)
	}

	// convert to ORM model
	leadORM, err := lead.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	if err := sQop.Leads.WithContext(ctx).Model(scrapingJob).Append(&leadORM); err != nil {
		return nil, fmt.Errorf("failed to append lead to scraping job: %w", err)
	}

	// save the scraping job
	if _, err := sQop.WithContext(ctx).Updates(scrapingJob); err != nil {
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