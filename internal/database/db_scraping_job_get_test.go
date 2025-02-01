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

func TestGetScrapingJob(t *testing.T) {
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
		id        uint64
		wantError bool
		errType   error
		validate  func(t *testing.T, job *lead_scraper_servicev1.ScrapingJob)
	}{
		{
			name:      "[success scenario] - valid id",
			id:        created.Id,
			wantError: false,
			validate: func(t *testing.T, job *lead_scraper_servicev1.ScrapingJob) {
				assert.NotNil(t, job)
				assert.Equal(t, created.Id, job.Id)
				assert.Equal(t, created.Status, job.Status)
				assert.Equal(t, created.Priority, job.Priority)
				assert.Equal(t, created.PayloadType, job.PayloadType)
				assert.Equal(t, created.Payload, job.Payload)
				assert.Equal(t, created.Name, job.Name)
				assert.Equal(t, created.Keywords, job.Keywords)
				assert.Equal(t, created.Lang, job.Lang)
				assert.Equal(t, created.Zoom, job.Zoom)
				assert.Equal(t, created.Lat, job.Lat)
				assert.Equal(t, created.Lon, job.Lon)
				assert.Equal(t, created.FastMode, job.FastMode)
				assert.Equal(t, created.Radius, job.Radius)
				assert.Equal(t, created.MaxTime, job.MaxTime)
			},
		},
		{
			name:      "[failure scenario] - invalid id",
			id:        0,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - non-existent id",
			id:        999999,
			wantError: true,
			errType:   ErrJobDoesNotExist,
		},
		{
			name:      "[failure scenario] - context timeout",
			id:        created.Id,
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

			result, err := conn.GetScrapingJob(ctx, tt.id)

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

func TestGetScrapingJob_ConcurrentReads(t *testing.T) {
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

	// Clean up after test
	defer func() {
		err := conn.DeleteScrapingJob(context.Background(), created.Id)
		require.NoError(t, err)
	}()

	numReads := 10
	var wg sync.WaitGroup
	errors := make(chan error, numReads)

	// Perform concurrent reads
	for i := 0; i < numReads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := conn.GetScrapingJob(context.Background(), created.Id)
			if err != nil {
				errors <- err
				return
			}
			assert.Equal(t, created.Id, job.Id)
			assert.Equal(t, created.Status, job.Status)
			assert.Equal(t, created.PayloadType, job.PayloadType)
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}
	require.Empty(t, errs, "Expected no errors during concurrent reads, got: %v", errs)
}
