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

func TestGetScrapingWorkflow(t *testing.T) {
	// Create a test workflow first
	testWorkflow := &lead_scraper_servicev1.ScrapingWorkflow{
		CronExpression:         "0 0 * * *",
		RetryCount:            0,
		MaxRetries:            5,
		AlertEmails:           "test@example.com",
		OrgId:                "test-org",
		TenantId:             "test-tenant",
		GeoFencingRadius:     1000.0,
		GeoFencingLat:        40.7128,
		GeoFencingLon:        -74.0060,
		GeoFencingZoomMin:    10,
		GeoFencingZoomMax:    20,
		IncludeReviews:       true,
		IncludePhotos:        true,
		IncludeBusinessHours: true,
		MaxReviewsPerBusiness: 100,
		RespectRobotsTxt:     true,
		AcceptTermsOfService:  true,
		UserAgent:            "TestBot/1.0",
	}

	created, err := conn.CreateScrapingWorkflow(context.Background(), testWorkflow)
	require.NoError(t, err)
	require.NotNil(t, created)

	// Clean up after all tests
	defer func() {
		if created != nil {
			err := conn.DeleteScrapingWorkflow(context.Background(), created.Id)
			require.NoError(t, err)
		}
	}()

	tests := []struct {
		name      string
		id        uint64
		wantError bool
		errType   error
		validate  func(t *testing.T, workflow *lead_scraper_servicev1.ScrapingWorkflow)
	}{
		{
			name:      "[success scenario] - valid id",
			id:        created.Id,
			wantError: false,
			validate: func(t *testing.T, workflow *lead_scraper_servicev1.ScrapingWorkflow) {
				assert.NotNil(t, workflow)
				assert.Equal(t, created.Id, workflow.Id)
				assert.Equal(t, created.CronExpression, workflow.CronExpression)
				assert.Equal(t, created.RetryCount, workflow.RetryCount)
				assert.Equal(t, created.MaxRetries, workflow.MaxRetries)
				assert.Equal(t, created.AlertEmails, workflow.AlertEmails)
				assert.Equal(t, created.OrgId, workflow.OrgId)
				assert.Equal(t, created.TenantId, workflow.TenantId)
				assert.Equal(t, created.GeoFencingRadius, workflow.GeoFencingRadius)
				assert.Equal(t, created.GeoFencingLat, workflow.GeoFencingLat)
				assert.Equal(t, created.GeoFencingLon, workflow.GeoFencingLon)
				assert.Equal(t, created.IncludeReviews, workflow.IncludeReviews)
				assert.Equal(t, created.IncludePhotos, workflow.IncludePhotos)
				assert.Equal(t, created.IncludeBusinessHours, workflow.IncludeBusinessHours)
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
			errType:   ErrWorkflowDoesNotExist,
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

			result, err := conn.GetScrapingWorkflow(ctx, tt.id)

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

func TestGetScrapingWorkflow_ConcurrentReads(t *testing.T) {
	// Create a test workflow first
	testWorkflow := &lead_scraper_servicev1.ScrapingWorkflow{
		CronExpression:         "0 0 * * *",
		RetryCount:            0,
		MaxRetries:            5,
		AlertEmails:           "test@example.com",
		OrgId:                "test-org",
		TenantId:             "test-tenant",
		GeoFencingRadius:     1000.0,
		GeoFencingLat:        40.7128,
		GeoFencingLon:        -74.0060,
		GeoFencingZoomMin:    10,
		GeoFencingZoomMax:    20,
		IncludeReviews:       true,
		IncludePhotos:        true,
		IncludeBusinessHours: true,
		MaxReviewsPerBusiness: 100,
		RespectRobotsTxt:     true,
		AcceptTermsOfService:  true,
		UserAgent:            "TestBot/1.0",
	}

	created, err := conn.CreateScrapingWorkflow(context.Background(), testWorkflow)
	require.NoError(t, err)
	require.NotNil(t, created)

	// Clean up after test
	defer func() {
		err := conn.DeleteScrapingWorkflow(context.Background(), created.Id)
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
			workflow, err := conn.GetScrapingWorkflow(context.Background(), created.Id)
			if err != nil {
				errors <- err
				return
			}
			assert.Equal(t, created.Id, workflow.Id)
			assert.Equal(t, created.CronExpression, workflow.CronExpression)
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
