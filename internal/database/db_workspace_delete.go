package database

import (
	"context"
	"errors"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// DeleteWorkspace deletes a workspace from the database
func (db *Db) DeleteWorkspace(ctx context.Context, id uint64) error {
	if id == 0 {
		return ErrInvalidInput
	}

	workspace := &lead_scraper_servicev1.Workspace{Id: id}
	// check that the workspace exists
	existing, err := db.GetWorkspace(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWorkspaceDoesNotExist
		}
		db.Logger.Error("failed to get workspace", zap.Error(err))
		return fmt.Errorf("failed to get workspace: %w", err)
	}
	
	if existing == nil {
		return ErrWorkspaceDoesNotExist
	}

	if err := lead_scraper_servicev1.DefaultDeleteWorkspace(ctx, workspace, db.Client.Engine); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrWorkspaceDoesNotExist
		}
		db.Logger.Error("failed to delete workspace", zap.Error(err))
		return fmt.Errorf("failed to delete workspace: %w", err)
	}

	return nil
} 