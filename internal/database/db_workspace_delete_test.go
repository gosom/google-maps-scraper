package database

import (
	"context"
	"sync"
	"testing"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDb_DeleteWorkspace(t *testing.T) {
	type args struct {
		ctx context.Context
		id  uint64
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T) *lead_scraper_servicev1.Workspace
		wantErr bool
		errType error
	}{
		{
			name: "[success scenario] - delete existing workspace",
			args: args{
				ctx: context.Background(),
			},
			setup: func(t *testing.T) *lead_scraper_servicev1.Workspace {
				workspace, err := conn.CreateWorkspace(context.Background(), testContext.Workspace)
				require.NoError(t, err)
				require.NotNil(t, workspace)
				return workspace
			},
			wantErr: false,
		},
		{
			name: "[failure scenario] - workspace does not exist",
			args: args{
				ctx: context.Background(),
				id:  999999, // Non-existent ID
			},
			wantErr: true,
		},
		{
			name: "[failure scenario] - zero ID",
			args: args{
				ctx: context.Background(),
				id:  0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var workspace *lead_scraper_servicev1.Workspace
			if tt.setup != nil {
				workspace = tt.setup(t)
				tt.args.id = workspace.Id
			}

			err := conn.DeleteWorkspace(tt.args.ctx, tt.args.id)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)

			// Verify workspace no longer exists
			_, err = conn.GetWorkspace(context.Background(), tt.args.id)
			require.Error(t, err)
		})
	}
}

func TestDb_DeleteWorkspace_ConcurrentDeletions(t *testing.T) {
	// Create multiple workspaces
	numWorkspaces := 5
	workspaces := make([]*lead_scraper_servicev1.Workspace, 0, numWorkspaces)

	for i := 0; i < numWorkspaces; i++ {
		workspace, err := conn.CreateWorkspace(context.Background(), testContext.Workspace)
		require.NoError(t, err)
		workspaces = append(workspaces, workspace)
	}

	var wg sync.WaitGroup
	errors := make(chan error, len(workspaces))

	// Delete workspaces concurrently
	for _, workspace := range workspaces {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			if err := conn.DeleteWorkspace(context.Background(), id); err != nil {
				errors <- err
			}
		}(workspace.Id)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Error during concurrent deletion: %v", err)
	}

	// Verify all workspaces are deleted
	for _, workspace := range workspaces {
		_, err := conn.GetWorkspace(context.Background(), workspace.Id)
		require.Error(t, err)
	}
}
