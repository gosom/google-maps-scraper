package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// UpdateAPIKey updates an existing API key
func (db *Db) UpdateAPIKey(ctx context.Context, apiKey *lead_scraper_servicev1.APIKey) (*lead_scraper_servicev1.APIKey, error) {
	if apiKey == nil || apiKey.Id == 0 {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// convert to ORM model
	apiKeyORM, err := apiKey.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	// update the API key
	result := db.Client.Engine.WithContext(ctx).Where("id = ?", apiKey.Id).Updates(&apiKeyORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to update API key: %w", result.Error)
	}

	// get the updated record
	if result := db.Client.Engine.WithContext(ctx).Where("id = ?", apiKey.Id).First(&apiKeyORM); result.Error != nil {
		return nil, fmt.Errorf("failed to get updated API key: %w", result.Error)
	}

	pbResult, err := apiKeyORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
}