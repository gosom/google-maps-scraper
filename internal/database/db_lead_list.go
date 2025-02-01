package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

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