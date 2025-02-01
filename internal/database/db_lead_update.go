package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

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