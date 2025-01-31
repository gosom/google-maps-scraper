package database

import (
	"context"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"go.uber.org/zap"
)

// DeleteWorkspace deletes a workspace from the database
func (db *Db) DeleteWorkspace(ctx context.Context, id uint64) error {
	workspace := &lead_scraper_servicev1.Workspace{Id: id}
	// check that the workspace exists
	existing, err := db.GetWorkspace(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}
	
	if existing == nil {
		return fmt.Errorf("workspace not found")
	}


	if err := lead_scraper_servicev1.DefaultDeleteWorkspace(ctx, workspace, db.Client.Engine); err != nil {
		db.Logger.Error("failed to delete workspace", zap.Error(err))
		return fmt.Errorf("failed to delete workspace: %w", err)
	}

	return nil
} 