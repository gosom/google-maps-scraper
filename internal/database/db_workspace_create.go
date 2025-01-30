package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"go.uber.org/zap"
)

// CreateWorkspace creates a new workspace in the database
func (db *Db) CreateWorkspace(ctx context.Context, workspace *lead_scraper_servicev1.Workspace) (*lead_scraper_servicev1.Workspace, error) {
	if workspace == nil {
		return nil, fmt.Errorf("workspace cannot be nil")
	}

	result, err := lead_scraper_servicev1.DefaultCreateWorkspace(ctx, workspace, db.Client.Engine)
	if err != nil {
		db.Logger.Error("failed to create workspace", zap.Error(err))
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	return result, nil
} 