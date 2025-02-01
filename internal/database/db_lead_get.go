package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

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