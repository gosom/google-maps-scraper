package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// CreateAPIKey creates a new API key in the database
func (db *Db) CreateAPIKey(ctx context.Context, apiKey *lead_scraper_servicev1.APIKey) (*lead_scraper_servicev1.APIKey, error) {
	if apiKey == nil {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// convert to ORM model
	apiKeyORM, err := apiKey.ToORM(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ORM model: %w", err)
	}

	// create the API key
	result := db.Client.Engine.WithContext(ctx).Create(&apiKeyORM)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to create API key: %w", result.Error)
	}

	// convert back to protobuf
	pbResult, err := apiKeyORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
} 