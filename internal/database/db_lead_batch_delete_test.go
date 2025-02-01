package database

import (
	"context"
	"testing"
	"time"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchDeleteLeads(t *testing.T) {
	// Create a test scraping job first
	testJob := testutils.GenerateRandomizedScrapingJob()

	createdJob, err := conn.CreateScrapingJob(context.Background(), testJob)
	require.NoError(t, err)
	require.NotNil(t, createdJob)

	// Create multiple test leads
	numLeads := 10
	leadIDs := make([]uint64, numLeads)
	for i := 0; i < numLeads; i++ {
		lead := testutils.GenerateRandomLead()
		created, err := conn.CreateLead(context.Background(), createdJob.Id, lead)
		require.NoError(t, err)
		require.NotNil(t, created)
		leadIDs[i] = created.Id
	}

	// Clean up job after all tests
	defer func() {
		if createdJob != nil {
			err := conn.DeleteScrapingJob(context.Background(), createdJob.Id)
			require.NoError(t, err)
		}
	}()

	tests := []struct {
		name      string
		leadIDs   []uint64
		wantError bool
		errType   error
		setup     func(t *testing.T) []uint64
		validate  func(t *testing.T, ids []uint64)
	}{
		{
			name:      "[success scenario] - delete all leads",
			leadIDs:   leadIDs,
			wantError: false,
			validate: func(t *testing.T, ids []uint64) {
				// Verify all leads were deleted
				for _, id := range ids {
					_, err := conn.GetLead(context.Background(), id)
					assert.Error(t, err)
					assert.ErrorIs(t, err, ErrJobDoesNotExist)
				}
			},
		},
		{
			name:      "[failure scenario] - nil lead IDs",
			leadIDs:   nil,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - empty lead IDs slice",
			leadIDs:   []uint64{},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - invalid lead IDs",
			setup: func(t *testing.T) []uint64 {
				return []uint64{0, 999999}
			},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - context timeout",
			leadIDs:   leadIDs,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ids []uint64
			if tt.setup != nil {
				ids = tt.setup(t)
			} else {
				ids = tt.leadIDs
			}

			ctx := context.Background()
			if tt.name == "[failure scenario] - context timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 1*time.Nanosecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond)
			}

			err := conn.BatchDeleteLeads(ctx, ids)

			if tt.wantError {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)

			if tt.validate != nil {
				tt.validate(t, ids)
			}
		})
	}
}

func TestBatchDeleteLeads_LargeBatch(t *testing.T) {
	// Create a test scraping job first
	testJob := testutils.GenerateRandomizedScrapingJob()

	createdJob, err := conn.CreateScrapingJob(context.Background(), testJob)
	require.NoError(t, err)
	require.NotNil(t, createdJob)

	// Create a large number of test leads
	numLeads := 1000 // This will test multiple batches
	leadIDs := make([]uint64, numLeads)
	for i := 0; i < numLeads; i++ {
		lead := testutils.GenerateRandomLead()
		created, err := conn.CreateLead(context.Background(), createdJob.Id, lead)
		require.NoError(t, err)
		require.NotNil(t, created)
		leadIDs[i] = created.Id
	}

	// Clean up job after test
	defer func() {
		err := conn.DeleteScrapingJob(context.Background(), createdJob.Id)
		require.NoError(t, err)
	}()

	// Perform batch delete
	err = conn.BatchDeleteLeads(context.Background(), leadIDs)
	require.NoError(t, err)

	// Verify all leads were deleted
	for _, id := range leadIDs {
		_, err := conn.GetLead(context.Background(), id)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrJobDoesNotExist)
	}
} 