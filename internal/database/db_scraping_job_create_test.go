package database

import (
	"context"
	"sync"
	"testing"
	"time"

	lead_scraper_servicev1 "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateScrapingJob(t *testing.T) {
	validJob := &lead_scraper_servicev1.ScrapingJob{
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

	tests := []struct {
		name      string
		job       *lead_scraper_servicev1.ScrapingJob
		wantError bool
		errType   error
		validate  func(t *testing.T, job *lead_scraper_servicev1.ScrapingJob)
	}{
		{
			name:      "[success scenario] - valid job",
			job:       validJob,
			wantError: false,
			validate: func(t *testing.T, job *lead_scraper_servicev1.ScrapingJob) {
				assert.NotNil(t, job)
				assert.NotZero(t, job.Id)
				assert.Equal(t, validJob.Status, job.Status)
				assert.Equal(t, validJob.Priority, job.Priority)
				assert.Equal(t, validJob.PayloadType, job.PayloadType)
				assert.Equal(t, validJob.Payload, job.Payload)
				assert.Equal(t, validJob.Name, job.Name)
				assert.Equal(t, validJob.Keywords, job.Keywords)
				assert.Equal(t, validJob.Lang, job.Lang)
				assert.Equal(t, validJob.Zoom, job.Zoom)
				assert.Equal(t, validJob.Lat, job.Lat)
				assert.Equal(t, validJob.Lon, job.Lon)
				assert.Equal(t, validJob.FastMode, job.FastMode)
				assert.Equal(t, validJob.Radius, job.Radius)
				assert.Equal(t, validJob.MaxTime, job.MaxTime)
			},
		},
		{
			name:      "[failure scenario] - nil job",
			job:       nil,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - missing required fields",
			job: &lead_scraper_servicev1.ScrapingJob{
				Status: 0,
				// Missing other required fields
			},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - invalid status",
			job: &lead_scraper_servicev1.ScrapingJob{
				Status:      999, // Invalid status
				Priority:    1,
				PayloadType: "scraping_job",
				Name:        "Test Job",
			},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - context timeout",
			job:  validJob,
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

			result, err := conn.CreateScrapingJob(ctx, tt.job)

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

			// Clean up created job
			if result != nil {
				err := conn.DeleteScrapingJob(context.Background(), result.Id)
				require.NoError(t, err)
			}
		})
	}
}

func TestCreateScrapingJob_ConcurrentCreation(t *testing.T) {
	numJobs := 5
	var wg sync.WaitGroup
	errors := make(chan error, numJobs)
	jobs := make(chan *lead_scraper_servicev1.ScrapingJob, numJobs)

	// Create jobs concurrently
	for i := 0; i < numJobs; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
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
			if err != nil {
				errors <- err
				return
			}
			jobs <- created
		}(i)
	}

	wg.Wait()
	close(errors)
	close(jobs)

	// Clean up created jobs
	createdJobs := make([]*lead_scraper_servicev1.ScrapingJob, 0)
	for job := range jobs {
		createdJobs = append(createdJobs, job)
	}

	defer func() {
		for _, job := range createdJobs {
			if job != nil {
				err := conn.DeleteScrapingJob(context.Background(), job.Id)
				require.NoError(t, err)
			}
		}
	}()

	// Check for errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}
	require.Empty(t, errs, "Expected no errors during concurrent creation, got: %v", errs)

	// Verify all jobs were created successfully
	require.Equal(t, numJobs, len(createdJobs))
	for _, job := range createdJobs {
		require.NotNil(t, job)
		require.NotZero(t, job.Id)
		assert.Equal(t, int32(0), job.Status) // Assuming 0 is PENDING in the protobuf enum
		assert.Equal(t, "scraping_job", job.PayloadType)
		assert.Equal(t, "Test Job", job.Name)
	}
}
