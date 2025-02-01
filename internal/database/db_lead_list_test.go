package database

import (
	"context"
	"testing"
	"time"

	"github.com/Vector/vector-leads-scraper/internal/testutils"
	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListLeads(t *testing.T) {
	// Create a test scraping job first
	testJob := testutils.GenerateRandomizedScrapingJob()

	createdJob, err := conn.CreateScrapingJob(context.Background(), testJob)
	require.NoError(t, err)
	require.NotNil(t, createdJob)

	// Create multiple test leads
	numLeads := 5
	leadIDs := make([]uint64, numLeads)

	for i := 0; i < numLeads; i++ {
		lead := testutils.GenerateRandomLead()
		created, err := conn.CreateLead(context.Background(), createdJob.Id, lead)
		require.NoError(t, err)
		require.NotNil(t, created)
		leadIDs[i] = created.Id
	}

	// Clean up after all tests
	defer func() {
		for _, id := range leadIDs {
			err := conn.DeleteLead(context.Background(), id, DeletionTypeSoft)
			require.NoError(t, err)
		}
		if createdJob != nil {
			err := conn.DeleteScrapingJob(context.Background(), createdJob.Id)
			require.NoError(t, err)
		}
	}()

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantError bool
		errType   error
		validate  func(t *testing.T, leads []*lead_scraper_servicev1.Lead)
	}{
		{
			name:      "[success scenario] - get all leads",
			limit:     10,
			offset:    0,
			wantError: false,
			validate: func(t *testing.T, leads []*lead_scraper_servicev1.Lead) {
				assert.Len(t, leads, numLeads)
				for _, lead := range leads {
					assert.NotNil(t, lead)
					assert.NotZero(t, lead.Id)
				}
			},
		},
		{
			name:      "[success scenario] - pagination first page",
			limit:     3,
			offset:    0,
			wantError: false,
			validate: func(t *testing.T, leads []*lead_scraper_servicev1.Lead) {
				assert.Len(t, leads, 3)
				for _, lead := range leads {
					assert.NotNil(t, lead)
					assert.NotZero(t, lead.Id)
				}
			},
		},
		{
			name:      "[success scenario] - pagination second page",
			limit:     3,
			offset:    3,
			wantError: false,
			validate: func(t *testing.T, leads []*lead_scraper_servicev1.Lead) {
				assert.Len(t, leads, 2) // Only 2 remaining leads
				for _, lead := range leads {
					assert.NotNil(t, lead)
					assert.NotZero(t, lead.Id)
				}
			},
		},
		{
			name:      "[success scenario] - empty result",
			limit:     10,
			offset:    numLeads + 1,
			wantError: false,
			validate: func(t *testing.T, leads []*lead_scraper_servicev1.Lead) {
				assert.Empty(t, leads)
			},
		},
		{
			name:      "[failure scenario] - invalid limit",
			limit:     -1,
			offset:    0,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - invalid offset",
			limit:     10,
			offset:    -1,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - context timeout",
			limit:     10,
			offset:    0,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.name == "[failure scenario] - context timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 1*time.Nanosecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond)
			}

			results, err := conn.ListLeads(ctx, tt.limit, tt.offset)

			if tt.wantError {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				assert.Nil(t, results)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, results)

			if tt.validate != nil {
				tt.validate(t, results)
			}
		})
	}
}

func TestListLeads_EmptyDatabase(t *testing.T) {
	_, err := conn.ListLeads(context.Background(), 10, 0)
	require.NoError(t, err)
} 