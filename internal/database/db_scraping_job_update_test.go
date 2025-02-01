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

func TestUpdateScrapingJob(t *testing.T) {
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

	// Clean up after all tests
	defer func() {
		if created != nil {
			err := conn.DeleteScrapingJob(context.Background(), created.Id)
			require.NoError(t, err)
		}
	}()

	tests := []struct {
		name      string
		job       *lead_scraper_servicev1.ScrapingJob
		wantError bool
		errType   error
		setup     func(t *testing.T) *lead_scraper_servicev1.ScrapingJob
		validate  func(t *testing.T, job *lead_scraper_servicev1.ScrapingJob)
	}{
		{
			name: "[success scenario] - valid update",
			job: &lead_scraper_servicev1.ScrapingJob{
				Id:          created.Id,
				Status:      1, // Update to a different status
				Priority:    2,
				PayloadType: "scraping_job",
				Payload:     []byte(`{"query": "updated query"}`),
				Name:        "Updated Test Job",
				Keywords:    []string{"updated_keyword"},
				Lang:        "fr",
				Zoom:        10,
				Lat:         "48.8566",
				Lon:         "2.3522",
				FastMode:    true,
				Radius:      5000,
				MaxTime:     1800,
			},
			wantError: false,
			validate: func(t *testing.T, job *lead_scraper_servicev1.ScrapingJob) {
				assert.NotNil(t, job)
				assert.Equal(t, created.Id, job.Id)
				assert.Equal(t, int32(1), job.Status)
				assert.Equal(t, int32(2), job.Priority)
				assert.Equal(t, "scraping_job", job.PayloadType)
				assert.Equal(t, []byte(`{"query": "updated query"}`), job.Payload)
				assert.Equal(t, "Updated Test Job", job.Name)
				assert.Equal(t, []string{"updated_keyword"}, job.Keywords)
				assert.Equal(t, "fr", job.Lang)
				assert.Equal(t, int32(10), job.Zoom)
				assert.Equal(t, "48.8566", job.Lat)
				assert.Equal(t, "2.3522", job.Lon)
				assert.True(t, job.FastMode)
				assert.Equal(t, int32(5000), job.Radius)
				assert.Equal(t, int32(1800), job.MaxTime)
			},
		},
		{
			name:      "[failure scenario] - nil job",
			job:       nil,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - zero id",
			job: &lead_scraper_servicev1.ScrapingJob{
				Id:          0,
				Status:      1,
				Priority:    2,
				PayloadType: "scraping_job",
				Name:        "Updated Test Job",
			},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - non-existent id",
			job: &lead_scraper_servicev1.ScrapingJob{
				Id:          999999,
				Status:      1,
				Priority:    2,
				PayloadType: "scraping_job",
				Name:        "Updated Test Job",
			},
			wantError: true,
			errType:   ErrJobDoesNotExist,
		},
		{
			name: "[failure scenario] - invalid status",
			job: &lead_scraper_servicev1.ScrapingJob{
				Id:          created.Id,
				Status:      999, // Invalid status
				Priority:    2,
				PayloadType: "scraping_job",
				Name:        "Updated Test Job",
			},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - context timeout",
			job: &lead_scraper_servicev1.ScrapingJob{
				Id:          created.Id,
				Status:      1,
				Priority:    2,
				PayloadType: "scraping_job",
				Name:        "Updated Test Job",
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var job *lead_scraper_servicev1.ScrapingJob
			if tt.setup != nil {
				job = tt.setup(t)
			} else {
				job = tt.job
			}

			ctx := context.Background()
			if tt.name == "[failure scenario] - context timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 1*time.Nanosecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond)
			}

			result, err := conn.UpdateScrapingJob(ctx, job)

			if tt.wantError {
				require.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)

			if tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}

func TestUpdateScrapingJob_ConcurrentUpdates(t *testing.T) {
	// Create a test job first
	testJob := &lead_scraper_servicev1.ScrapingJob{
		Status:      0,
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

	// Clean up after test
	defer func() {
		err := conn.DeleteScrapingJob(context.Background(), created.Id)
		require.NoError(t, err)
	}()

	numUpdates := 5
	var wg sync.WaitGroup
	errors := make(chan error, numUpdates)
	results := make(chan *lead_scraper_servicev1.ScrapingJob, numUpdates)

	// Perform concurrent updates
	for i := 0; i < numUpdates; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			
			updateJob := &lead_scraper_servicev1.ScrapingJob{
				Id:          created.Id,
				Status:      lead_scraper_servicev1.BackgroundJobStatus(index + 1), // Different status for each update
				Priority:    int32(index + 1),
				PayloadType: "scraping_job",
				Payload:     []byte(fmt.Sprintf(`{"query": "concurrent update %d"}`, index)),
				Name:        fmt.Sprintf("Updated Job %d", index),
				Keywords:    []string{fmt.Sprintf("keyword%d", index)},
				Lang:        "en",
				Zoom:        15,
				Lat:         "40.7128",
				Lon:         "-74.0060",
				FastMode:    false,
				Radius:      10000,
				MaxTime:     3600,
			}

			result, err := conn.UpdateScrapingJob(context.Background(), updateJob)
			if err != nil {
				errors <- err
				return
			}
			results <- result
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

	// Verify the final state
	finalJob, err := conn.GetScrapingJob(context.Background(), created.Id)
	require.NoError(t, err)
	require.NotNil(t, finalJob)
	assert.Equal(t, created.Id, finalJob.Id)
	assert.NotEqual(t, created.Status, finalJob.Status)
	assert.NotEqual(t, created.Priority, finalJob.Priority)
	assert.NotEqual(t, created.Name, finalJob.Name)
}
