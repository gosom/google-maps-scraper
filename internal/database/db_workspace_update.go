package database

import (
	"context"
	"errors"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// UpdateWorkspace updates an existing workspace in the database
func (db *Db) UpdateWorkspace(ctx context.Context, workspace *lead_scraper_servicev1.Workspace) (*lead_scraper_servicev1.Workspace, error) {
	if workspace == nil {
		return nil, ErrInvalidInput
	}

	if workspace.Id == 0 {
		return nil, ErrInvalidInput
	}

	// query the workspace
	existing, err := db.GetWorkspace(ctx, workspace.Id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkspaceDoesNotExist
		}
		db.Logger.Error("failed to get workspace", zap.Error(err))
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}

	// check if the workspace exists
	if existing == nil {
		return nil, ErrWorkspaceDoesNotExist
	}

	result, err := lead_scraper_servicev1.DefaultStrictUpdateWorkspace(ctx, workspace, db.Client.Engine)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkspaceDoesNotExist
		}
		db.Logger.Error("failed to update workspace", zap.Error(err))
		return nil, fmt.Errorf("failed to update workspace: %w", err)
	}

	return result, nil
} 