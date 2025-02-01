package database

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

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
					Industry: "Technology",
					Domain: "updated-domain.com",
					GdprCompliant: true,
					HipaaCompliant: true,
					Soc2Compliant: true,
					StorageQuota: 1000000,
					UsedStorage: 500000,
				},
			},
			wantErr: false,
			validate: func(t *testing.T, updated *lead_scraper_servicev1.Workspace) {
				assert.NotNil(t, updated)
				assert.Equal(t, workspace.Id, updated.Id)
				assert.Equal(t, "Updated Workspace Name", updated.Name)
				assert.Equal(t, "Technology", updated.Industry)
				assert.Equal(t, "updated-domain.com", updated.Domain)
				assert.Equal(t, true, updated.GdprCompliant)
				assert.Equal(t, true, updated.HipaaCompliant)
				assert.Equal(t, true, updated.Soc2Compliant)
				assert.Equal(t, int64(1000000), updated.StorageQuota)
				assert.Equal(t, int64(500000), updated.UsedStorage)
			},
		},
		{
			name: "[failure scenario] - nil workspace",
			args: args{
				ctx:       context.Background(),
				workspace: nil,
			},
			wantErr: true,
			errType: ErrInvalidInput,
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
			errType: ErrWorkspaceDoesNotExist,
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
			errType: ErrInvalidInput,
		},
		{
			name: "[failure scenario] - missing required fields",
			args: args{
				ctx: context.Background(),
				workspace: &lead_scraper_servicev1.Workspace{
					Id: workspace.Id,
					// Missing Name and other required fields
				},
			},
			wantErr: true,
			errType: ErrInvalidInput,
		},
		{
			name: "[failure scenario] - context timeout",
			args: args{
				ctx: func() context.Context {
					ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
					defer cancel()
					time.Sleep(2 * time.Millisecond)
					return ctx
				}(),
				workspace: &lead_scraper_servicev1.Workspace{
					Id:   workspace.Id,
					Name: "Timeout Test",
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
	results := make(chan *lead_scraper_servicev1.Workspace, numUpdates)

	// Perform concurrent updates
	for i := 0; i < numUpdates; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			updateWorkspace := &lead_scraper_servicev1.Workspace{
				Id:   workspace.Id,
				Name: fmt.Sprintf("Updated Name %d", index),
				Industry: fmt.Sprintf("Industry %d", index),
				Domain: fmt.Sprintf("domain-%d.com", index),
				GdprCompliant: true,
				HipaaCompliant: true,
				Soc2Compliant: true,
				StorageQuota: int64(1000000 + index),
				UsedStorage: int64(500000 + index),
			}
			updated, err := conn.UpdateWorkspace(context.Background(), updateWorkspace)
			if err != nil {
				errors <- err
				return
			}
			results <- updated
		}(i)
	}

	wg.Wait()
	close(errors)
	close(results)

	// Check for errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}
	require.Empty(t, errs, "Expected no errors during concurrent updates, got: %v", errs)

	// Verify final state
	finalWorkspace, err := conn.GetWorkspace(context.Background(), workspace.Id)
	require.NoError(t, err)
	assert.NotNil(t, finalWorkspace)
	assert.Contains(t, finalWorkspace.Name, "Updated Name")
	assert.Contains(t, finalWorkspace.Industry, "Industry")
	assert.Contains(t, finalWorkspace.Domain, "domain-")
	assert.True(t, finalWorkspace.GdprCompliant)
	assert.True(t, finalWorkspace.HipaaCompliant)
	assert.True(t, finalWorkspace.Soc2Compliant)
	assert.Greater(t, finalWorkspace.StorageQuota, int64(1000000))
	assert.Greater(t, finalWorkspace.UsedStorage, int64(500000))

	// Verify all updates were successful
	var updates []*lead_scraper_servicev1.Workspace
	for result := range results {
		updates = append(updates, result)
	}
	require.Len(t, updates, numUpdates, "Expected %d successful updates", numUpdates)
}
