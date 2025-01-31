package database

import (
	"context"
	"fmt"
	"sync"
	"testing"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDb_UpdateWorkspace(t *testing.T) {
	// Create a test workspace first
	mockWorkspace := testContext.Workspace
	workspace, err := conn.CreateWorkspace(context.Background(), mockWorkspace)
	require.NoError(t, err)
	require.NotNil(t, workspace)

	// Clean up after all tests
	defer func() {
		err := conn.DeleteWorkspace(context.Background(), workspace.Id)
		require.NoError(t, err)
	}()

	type args struct {
		ctx       context.Context
		workspace *lead_scraper_servicev1.Workspace
	}

	tests := []struct {
		name     string
		args     args
		wantErr  bool
		errType  error
		validate func(t *testing.T, workspace *lead_scraper_servicev1.Workspace)
	}{
		{
			name: "[success scenario] - update existing workspace",
			args: args{
				ctx: context.Background(),
				workspace: &lead_scraper_servicev1.Workspace{
					Id:   workspace.Id,
					Name: "Updated Workspace Name",
				},
			},
			wantErr: false,
			validate: func(t *testing.T, updated *lead_scraper_servicev1.Workspace) {
				assert.NotNil(t, updated)
				assert.Equal(t, workspace.Id, updated.Id)
				assert.Equal(t, "Updated Workspace Name", updated.Name)
			},
		},
		{
			name: "[failure scenario] - nil workspace",
			args: args{
				ctx:       context.Background(),
				workspace: nil,
			},
			wantErr: true,
		},
		{
			name: "[failure scenario] - workspace does not exist",
			args: args{
				ctx: context.Background(),
				workspace: &lead_scraper_servicev1.Workspace{
					Id:   999999, // Non-existent ID
					Name: "Non-existent Workspace",
				},
			},
			wantErr: true,
		},
		{
			name: "[failure scenario] - zero ID",
			args: args{
				ctx: context.Background(),
				workspace: &lead_scraper_servicev1.Workspace{
					Id:   0,
					Name: "Zero ID Workspace",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updated, err := conn.UpdateWorkspace(tt.args.ctx, tt.args.workspace)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)
			if tt.validate != nil {
				tt.validate(t, updated)
			}
		})
	}
}

func TestDb_UpdateWorkspace_ConcurrentUpdates(t *testing.T) {
	// Create initial workspace
	mockWorkspace := testContext.Workspace
	workspace, err := conn.CreateWorkspace(context.Background(), mockWorkspace)
	require.NoError(t, err)
	require.NotNil(t, workspace)

	// Clean up after test
	defer func() {
		err := conn.DeleteWorkspace(context.Background(), workspace.Id)
		require.NoError(t, err)
	}()

	numUpdates := 5
	var wg sync.WaitGroup
	errors := make(chan error, numUpdates)

	// Perform concurrent updates
	for i := 0; i < numUpdates; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			updateWorkspace := &lead_scraper_servicev1.Workspace{
				Id:   workspace.Id,
				Name: fmt.Sprintf("Updated Name %d", index),
			}
			_, err := conn.UpdateWorkspace(context.Background(), updateWorkspace)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Error during concurrent update: %v", err)
	}

	// Verify final state
	finalWorkspace, err := conn.GetWorkspace(context.Background(), workspace.Id)
	require.NoError(t, err)
	assert.NotNil(t, finalWorkspace)
	assert.Contains(t, finalWorkspace.Name, "Updated Name")
}
