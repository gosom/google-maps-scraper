package database

import (
	"context"
	"fmt"

	"gorm.io/gen/field"
)

// DeleteAPIKey deletes an API key by ID
func (db *Db) DeleteAPIKey(ctx context.Context, id uint64) error {
	var (
		aQop = db.QueryOperator.APIKeyORM
	)

	if id == 0 {
		return ErrInvalidInput
	}

	ctx, cancel := context.WithTimeout(ctx, db.GetQueryTimeout())
	defer cancel()

	// check if the API key exists
	apiKey, err := aQop.WithContext(ctx).Where(aQop.Id.Eq(id)).First()
	if err != nil {
		return err
	}

	if apiKey == nil {
		return ErrAPIKeyDoesNotExist
	}

	// delete the API key
	if _, err := aQop.WithContext(ctx).Where(aQop.Id.Eq(id)).Select(field.AssociationFields).Delete(); err != nil {
		return fmt.Errorf("failed to delete API key: %w", err)
	}

	return nil
}