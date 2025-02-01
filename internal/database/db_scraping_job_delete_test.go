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

func TestDeleteScrapingJob(t *testing.T) {
	// Create a test job first
	testJob := &lead_scraper_servicev1.ScrapingJob{
		Status:      0, // Assuming 0 is PENDING in the protobuf enum
		Priority:    1,
		PayloadType: "scraping_job",
		Payload:     []byte(`{"query": "test query"}`),
		Name:        "Test Job",
		Keywords:    []string{"keyword1", "keyword2"},
		Lang:        "en",
		Zoom:        15,
		Lat:         "40.7128",
		Lon:         "-74.0060",
		FastMode:    false,
		Radius:      10000,
		MaxTime:     3600,
	}

	created, err := conn.CreateScrapingJob(context.Background(), testJob)
	require.NoError(t, err)
	require.NotNil(t, created)

	tests := []struct {
		name      string
		id        string
		wantError bool
		errType   error
		setup     func(t *testing.T) string
		validate  func(t *testing.T, id string)
	}{
		{
			name:      "[success scenario] - valid id",
			id:        fmt.Sprintf("%d", created.Id),
			wantError: false,
			validate: func(t *testing.T, id string) {
				// Verify the job was deleted
				_, err := conn.GetScrapingJob(context.Background(), created.Id)
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrJobDoesNotExist)
			},
		},
		{
			name:      "[failure scenario] - invalid id",
			id:        "",
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - non-existent id",
			id:        "999999",
			wantError: true,
			errType:   ErrJobDoesNotExist,
		},
		{
			name:      "[failure scenario] - already deleted job",
			wantError: true,
			errType:   ErrJobDoesNotExist,
			setup: func(t *testing.T) string {
				// Create and delete a job
				job := &lead_scraper_servicev1.ScrapingJob{
					Status:      0,
					Priority:    1,
					PayloadType: "scraping_job",
					Name:        "Test Job",
				}
				created, err := conn.CreateScrapingJob(context.Background(), job)
				require.NoError(t, err)
				require.NotNil(t, created)

				id := fmt.Sprintf("%d", created.Id)
				err = conn.DeleteScrapingJob(context.Background(), uint64(0))
				require.NoError(t, err)

				return id
			},
		},
		{
			name:      "[failure scenario] - context timeout",
			id:        fmt.Sprintf("%d", created.Id),
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id string
			if tt.setup != nil {
				id = tt.setup(t)
			} else {
				id = tt.id
			}

			ctx := context.Background()
			if tt.name == "[failure scenario] - context timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 1*time.Nanosecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond)
			}

			err := conn.DeleteScrapingJob(ctx, uint64(0))

			if tt.wantError {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}

			require.NoError(t, err)

			if tt.validate != nil {
				tt.validate(t, id)
			}
		})
	}
}

func TestDeleteScrapingJob_ConcurrentDeletions(t *testing.T) {
	numJobs := 5
	var wg sync.WaitGroup
	errors := make(chan error, numJobs)
	jobIDs := make([]string, numJobs)

	// Create test jobs
	for i := 0; i < numJobs; i++ {
		job := &lead_scraper_servicev1.ScrapingJob{
			Status:      0, // Assuming 0 is PENDING in the protobuf enum
			Priority:    1,
			PayloadType: "scraping_job",
			Payload:     []byte(`{"query": "test query"}`),
			Name:        "Test Job",
			Keywords:    []string{"keyword1", "keyword2"},
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
		jobIDs[i] = fmt.Sprintf("%d", created.Id)
	}

	// Delete jobs concurrently
	for i := 0; i < numJobs; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := conn.DeleteScrapingJob(context.Background(), uint64(0)); err != nil {
				errors <- err
			}
		}(jobIDs[i])
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}
	require.Empty(t, errs, "Expected no errors during concurrent deletions, got: %v", errs)

	// Verify all jobs were deleted
	for _, id := range jobIDs {
		idUint64 := uint64(0)
		_, err := fmt.Sscanf(id, "%d", &idUint64)
		require.NoError(t, err)
		_, err = conn.GetScrapingJob(context.Background(), idUint64)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrJobDoesNotExist)
	}
}
