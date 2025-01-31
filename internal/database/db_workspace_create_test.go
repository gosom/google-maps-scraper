package database

import (
	"context"
	"sync"
	"testing"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDb_CreateWorkspace(t *testing.T) {
	// Create test workspace
	validWorkspace := testutils.GenerateRandomWorkspace()

	type args struct {
		ctx       context.Context
		workspace *lead_scraper_servicev1.Workspace
		clean    func(t *testing.T, workspace *lead_scraper_servicev1.Workspace)
	}

	tests := []struct {
		name     string
		args     args
		wantErr  bool
		errType  error
		validate func(t *testing.T, workspace *lead_scraper_servicev1.Workspace)
	}{
		{
			name:    "[success scenario] - create new workspace",
			wantErr: false,
			args: args{
				ctx:       context.Background(),
				workspace: validWorkspace,
				clean: func(t *testing.T, workspace *lead_scraper_servicev1.Workspace) {
					err := conn.DeleteWorkspace(context.Background(), workspace.Id)
					require.NoError(t, err)
				},
			},
			validate: func(t *testing.T, workspace *lead_scraper_servicev1.Workspace) {
				assert.NotNil(t, workspace)
				assert.NotZero(t, workspace.Id)
				assert.Equal(t, validWorkspace.Name, workspace.Name)
			},
		},
		{
			name:    "[failure scenario] - nil workspace",
			wantErr: true,
			args: args{
				ctx:       context.Background(),
				workspace: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace, err := conn.CreateWorkspace(tt.args.ctx, tt.args.workspace)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, workspace)

			if tt.validate != nil {
				tt.validate(t, workspace)
			}

			// Cleanup after test
			if tt.args.clean != nil {
				tt.args.clean(t, workspace)
			}
		})
	}
}

func TestDb_CreateWorkspace_ConcurrentCreation(t *testing.T) {
	numWorkspaces := 5
	var wg sync.WaitGroup
	errors := make(chan error, numWorkspaces)
	workspaces := make(chan *lead_scraper_servicev1.Workspace, numWorkspaces)

	// Create workspaces concurrently
	for i := 0; i < numWorkspaces; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mockWorkspace := testutils.GenerateRandomWorkspace()
			workspace, err := conn.CreateWorkspace(context.Background(), mockWorkspace)
			if err != nil {
				errors <- err
				return
			}
			workspaces <- workspace
		}()
	}

	wg.Wait()
	close(errors)
	close(workspaces)

	// Check for errors
	for err := range errors {
		t.Errorf("Error during concurrent creation: %v", err)
	}

	// Clean up created workspaces
	for workspace := range workspaces {
		err := conn.DeleteWorkspace(context.Background(), workspace.Id)
		require.NoError(t, err)
	}
}
