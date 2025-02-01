package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
)

// ValidateAPIKey checks if an API key hash exists and is valid
func (db *Db) ValidateAPIKey(ctx context.Context, hash string) (*lead_scraper_servicev1.APIKey, error) {
	var (
		aQop = db.QueryOperator.APIKeyORM
		apiKeyORM *lead_scraper_servicev1.APIKeyORM
		err error
	)

	if hash == "" {
		return nil, ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	if apiKeyORM, err = aQop.WithContext(ctx).Where(aQop.KeyHash.Eq(hash)).First(); err != nil {
		if err.Error() == "record not found" {
			return nil, fmt.Errorf("%w: %v", ErrJobDoesNotExist, err)
		}
		return nil, fmt.Errorf("failed to validate API key: %w", err)
	}

	pbResult, err := apiKeyORM.ToPB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to protobuf: %w", err)
	}

	return &pbResult, nil
} 