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

func TestCreateLead(t *testing.T) {
	// Create a test scraping job first
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

	createdJob, err := conn.CreateScrapingJob(context.Background(), testJob)
	require.NoError(t, err)
	require.NotNil(t, createdJob)

	// Clean up job after all tests
	defer func() {
		if createdJob != nil {
			err := conn.DeleteScrapingJob(context.Background(), createdJob.Id)
			require.NoError(t, err)
		}
	}()

	validLead := &lead_scraper_servicev1.Lead{
		Name:          "Test Lead",
		Website:       "https://test-lead.com",
		Phone:         "+1234567890",
		Address:       "123 Test St",
		City:          "Test City",
		State:         "Test State",
		Country:       "Test Country",
		Industry:      "Technology",
		PlaceId:       "ChIJ_test123",
		GoogleMapsUrl: "https://maps.google.com/?q=40.7128,-74.0060",
		Latitude:      40.7128,
		Longitude:     -74.0060,
		GoogleRating:  4.5,
		ReviewCount:   100,
	}

	tests := []struct {
		name          string
		scrapingJobID uint64
		lead          *lead_scraper_servicev1.Lead
		wantError     bool
		errType       error
		validate      func(t *testing.T, lead *lead_scraper_servicev1.Lead)
	}{
		{
			name:          "[success scenario] - valid lead",
			scrapingJobID: createdJob.Id,
			lead:          validLead,
			wantError:     false,
			validate: func(t *testing.T, lead *lead_scraper_servicev1.Lead) {
				assert.NotNil(t, lead)
				assert.NotZero(t, lead.Id)
				assert.Equal(t, validLead.Name, lead.Name)
				assert.Equal(t, validLead.Website, lead.Website)
				assert.Equal(t, validLead.Phone, lead.Phone)
				assert.Equal(t, validLead.Address, lead.Address)
				assert.Equal(t, validLead.City, lead.City)
				assert.Equal(t, validLead.State, lead.State)
				assert.Equal(t, validLead.Country, lead.Country)
				assert.Equal(t, validLead.Industry, lead.Industry)
				assert.Equal(t, validLead.PlaceId, lead.PlaceId)
				assert.Equal(t, validLead.GoogleMapsUrl, lead.GoogleMapsUrl)
				assert.Equal(t, validLead.Latitude, lead.Latitude)
				assert.Equal(t, validLead.Longitude, lead.Longitude)
				assert.Equal(t, validLead.GoogleRating, lead.GoogleRating)
				assert.Equal(t, validLead.ReviewCount, lead.ReviewCount)
			},
		},
		{
			name:          "[failure scenario] - nil lead",
			scrapingJobID: createdJob.Id,
			lead:          nil,
			wantError:     true,
			errType:       ErrInvalidInput,
		},
		{
			name:          "[failure scenario] - invalid scraping job ID",
			scrapingJobID: 0,
			lead:          validLead,
			wantError:     true,
			errType:       ErrInvalidInput,
		},
		{
			name:          "[failure scenario] - non-existent scraping job ID",
			scrapingJobID: 999999,
			lead:          validLead,
			wantError:     true,
			errType:       ErrJobDoesNotExist,
		},
		{
			name:          "[failure scenario] - context timeout",
			scrapingJobID: createdJob.Id,
			lead:          validLead,
			wantError:     true,
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

			result, err := conn.CreateLead(ctx, tt.scrapingJobID, tt.lead)

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

			// Clean up created lead
			if result != nil {
				err := conn.DeleteLead(context.Background(), result.Id, DeletionTypeSoft)
				require.NoError(t, err)
			}
		})
	}
}

func TestCreateLead_ConcurrentCreation(t *testing.T) {
	// Create a test scraping job first
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

	createdJob, err := conn.CreateScrapingJob(context.Background(), testJob)
	require.NoError(t, err)
	require.NotNil(t, createdJob)

	// Clean up job after test
	defer func() {
		err := conn.DeleteScrapingJob(context.Background(), createdJob.Id)
		require.NoError(t, err)
	}()

	numLeads := 5
	var wg sync.WaitGroup
	errors := make(chan error, numLeads)
	results := make(chan *lead_scraper_servicev1.Lead, numLeads)

	// Create leads concurrently
	for i := 0; i < numLeads; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			lead := &lead_scraper_servicev1.Lead{
				Name:          fmt.Sprintf("Test Lead %d", index),
				Website:       fmt.Sprintf("https://test-lead-%d.com", index),
				Phone:         fmt.Sprintf("+%d", 1234567890+index),
				Address:       fmt.Sprintf("123 Test St %d", index),
				City:          "Test City",
				State:         "Test State",
				Country:       "Test Country",
				Industry:      "Technology",
				PlaceId:       fmt.Sprintf("ChIJ_test%d", index),
				GoogleMapsUrl: "https://maps.google.com/?q=40.7128,-74.0060",
				Latitude:      40.7128,
				Longitude:     -74.0060,
				GoogleRating:  4.5,
				ReviewCount:   100,
			}

			result, err := conn.CreateLead(context.Background(), createdJob.Id, lead)
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

	// Clean up created leads
	createdLeads := make([]*lead_scraper_servicev1.Lead, 0)
	for result := range results {
		createdLeads = append(createdLeads, result)
	}

	defer func() {
		for _, lead := range createdLeads {
			if lead != nil {
				err := conn.DeleteLead(context.Background(), lead.Id, DeletionTypeSoft)
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

	// Verify all leads were created successfully
	require.Equal(t, numLeads, len(createdLeads))
	for i, lead := range createdLeads {
		require.NotNil(t, lead)
		require.NotZero(t, lead.Id)
		assert.Equal(t, fmt.Sprintf("Test Lead %d", i), lead.Name)
	}
} 