package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListScrapingJobs(t *testing.T) {
	// Create multiple test jobs
	numJobs := 5
	jobIDs := make([]uint64, numJobs)
	
	for i := 0; i < numJobs; i++ {
		job := &lead_scraper_servicev1.ScrapingJob{
			Status:      0,
			Priority:    int32(i + 1),
			PayloadType: "scraping_job",
			Payload:     []byte(fmt.Sprintf(`{"query": "test query %d"}`, i)),
			Name:        fmt.Sprintf("Test Job %d", i),
			Keywords:    []string{fmt.Sprintf("keyword%d", i)},
			Lang:        "en",
			Zoom:        15,
			Lat:         "40.7128",
			Lon:         "-74.0060",
			FastMode:    false,
			Radius:      10000,
			MaxTime:     3600,
		}
		created, err := conn.CreateScrapingJob(context.Background(), job)
		require.NoError(t, err)
		require.NotNil(t, created)
		jobIDs[i] = created.Id
	}

	// Clean up after all tests
	defer func() {
		for _, id := range jobIDs {
			err := conn.DeleteScrapingJob(context.Background(), id)
			require.NoError(t, err)
		}
	}()

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantError bool
		errType   error
		validate  func(t *testing.T, jobs []*lead_scraper_servicev1.ScrapingJob)
	}{
		{
			name:      "[success scenario] - get all jobs",
			limit:     10,
			offset:    0,
			wantError: false,
			validate: func(t *testing.T, jobs []*lead_scraper_servicev1.ScrapingJob) {
				assert.Len(t, jobs, numJobs)
				for i, job := range jobs {
					assert.NotNil(t, job)
					assert.NotZero(t, job.Id)
					assert.Equal(t, fmt.Sprintf("Test Job %d", i), job.Name)
				}
			},
		},
		{
			name:      "[success scenario] - pagination first page",
			limit:     3,
			offset:    0,
			wantError: false,
			validate: func(t *testing.T, jobs []*lead_scraper_servicev1.ScrapingJob) {
				assert.Len(t, jobs, 3)
				for i, job := range jobs {
					assert.NotNil(t, job)
					assert.NotZero(t, job.Id)
					assert.Equal(t, fmt.Sprintf("Test Job %d", i), job.Name)
				}
			},
		},
		{
			name:      "[success scenario] - pagination second page",
			limit:     3,
			offset:    3,
			wantError: false,
			validate: func(t *testing.T, jobs []*lead_scraper_servicev1.ScrapingJob) {
				assert.Len(t, jobs, 2) // Only 2 remaining jobs
				for i, job := range jobs {
					assert.NotNil(t, job)
					assert.NotZero(t, job.Id)
					assert.Equal(t, fmt.Sprintf("Test Job %d", i+3), job.Name)
				}
			},
		},
		{
			name:      "[success scenario] - empty result",
			limit:     10,
			offset:    numJobs + 1,
			wantError: false,
			validate: func(t *testing.T, jobs []*lead_scraper_servicev1.ScrapingJob) {
				assert.Empty(t, jobs)
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

			results, err := conn.ListScrapingJobs(ctx, tt.limit, tt.offset)

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

func TestListScrapingJobs_EmptyDatabase(t *testing.T) {
	results, err := conn.ListScrapingJobs(context.Background(), 10, 0)
	require.NoError(t, err)
	assert.Empty(t, results)
}
