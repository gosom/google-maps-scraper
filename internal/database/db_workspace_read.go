package database

import (
	"context"
	"errors"
	"fmt"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// GetWorkspace retrieves a workspace by ID from the database
func (db *Db) GetWorkspace(ctx context.Context, id uint64) (*lead_scraper_servicev1.Workspace, error) {
	workspace := &lead_scraper_servicev1.Workspace{Id: id}
	result, err := lead_scraper_servicev1.DefaultReadWorkspace(ctx, workspace, db.Client.Engine)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("workspace not found")
		}
		db.Logger.Error("failed to get workspace", zap.Error(err))
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}

	return result, nil
}

// ListWorkspaces retrieves a paginated list of workspaces from the database
func (db *Db) ListWorkspaces(ctx context.Context, limit, offset int) ([]*lead_scraper_servicev1.Workspace, error) {
	workspaces, err := lead_scraper_servicev1.DefaultListWorkspace(ctx, db.Client.Engine.Limit(limit).Offset(offset))
	if err != nil {
		db.Logger.Error("failed to list workspaces", zap.Error(err))
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	return workspaces, nil
} 