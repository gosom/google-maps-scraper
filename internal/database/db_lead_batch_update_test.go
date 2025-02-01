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

func TestBatchUpdateLeads(t *testing.T) {
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

	// Create multiple test leads
	numLeads := 10
	createdLeads := make([]*lead_scraper_servicev1.Lead, numLeads)
	for i := 0; i < numLeads; i++ {
		lead := &lead_scraper_servicev1.Lead{
			Name:          fmt.Sprintf("Test Lead %d", i),
			Website:       fmt.Sprintf("https://test-lead-%d.com", i),
			Phone:         fmt.Sprintf("+%d", 1234567890+i),
			Address:       fmt.Sprintf("123 Test St %d", i),
			City:          "Test City",
			State:         "Test State",
			Country:       "Test Country",
			Industry:      "Technology",
			PlaceId:       fmt.Sprintf("ChIJ_test%d", i),
			GoogleMapsUrl: "https://maps.google.com/?q=40.7128,-74.0060",
			Latitude:      40.7128,
			Longitude:     -74.0060,
			GoogleRating:  4.5,
			ReviewCount:   100,
		}
		created, err := conn.CreateLead(context.Background(), createdJob.Id, lead)
		require.NoError(t, err)
		require.NotNil(t, created)
		createdLeads[i] = created
	}

	// Clean up after all tests
	defer func() {
		for _, lead := range createdLeads {
			if lead != nil {
				err := conn.DeleteLead(context.Background(), lead.Id, DeletionTypeSoft)
				require.NoError(t, err)
			}
		}
		if createdJob != nil {
			err := conn.DeleteScrapingJob(context.Background(), createdJob.Id)
			require.NoError(t, err)
		}
	}()

	tests := []struct {
		name      string
		leads     []*lead_scraper_servicev1.Lead
		wantError bool
		errType   error
		setup     func(t *testing.T) []*lead_scraper_servicev1.Lead
		validate  func(t *testing.T, leads []*lead_scraper_servicev1.Lead)
	}{
		{
			name: "[success scenario] - update all leads",
			setup: func(t *testing.T) []*lead_scraper_servicev1.Lead {
				updatedLeads := make([]*lead_scraper_servicev1.Lead, len(createdLeads))
				for i, lead := range createdLeads {
					updatedLead := *lead
					updatedLead.Name = fmt.Sprintf("Updated Lead %d", i)
					updatedLead.Website = fmt.Sprintf("https://updated-lead-%d.com", i)
					updatedLead.Phone = fmt.Sprintf("+%d", 9876543210+i)
					updatedLead.Address = fmt.Sprintf("456 Updated St %d", i)
					updatedLead.City = "Updated City"
					updatedLead.State = "Updated State"
					updatedLead.Country = "Updated Country"
					updatedLead.Industry = "Updated Industry"
					updatedLead.PlaceId = fmt.Sprintf("ChIJ_updated%d", i)
					updatedLead.GoogleMapsUrl = "https://maps.google.com/?q=41.8781,-87.6298"
					updatedLead.Latitude = 41.8781
					updatedLead.Longitude = -87.6298
					updatedLead.GoogleRating = 4.8
					updatedLead.ReviewCount = 200
					updatedLeads[i] = &updatedLead
				}
				return updatedLeads
			},
			wantError: false,
			validate: func(t *testing.T, leads []*lead_scraper_servicev1.Lead) {
				assert.Len(t, leads, numLeads)
				for i, lead := range leads {
					assert.NotNil(t, lead)
					assert.Equal(t, fmt.Sprintf("Updated Lead %d", i), lead.Name)
					assert.Equal(t, fmt.Sprintf("https://updated-lead-%d.com", i), lead.Website)
					assert.Equal(t, fmt.Sprintf("+%d", 9876543210+i), lead.Phone)
					assert.Equal(t, fmt.Sprintf("456 Updated St %d", i), lead.Address)
					assert.Equal(t, "Updated City", lead.City)
					assert.Equal(t, "Updated State", lead.State)
					assert.Equal(t, "Updated Country", lead.Country)
					assert.Equal(t, "Updated Industry", lead.Industry)
					assert.Equal(t, fmt.Sprintf("ChIJ_updated%d", i), lead.PlaceId)
					assert.Equal(t, "https://maps.google.com/?q=41.8781,-87.6298", lead.GoogleMapsUrl)
					assert.Equal(t, float64(41.8781), lead.Latitude)
					assert.Equal(t, float64(-87.6298), lead.Longitude)
					assert.Equal(t, float32(4.8), lead.GoogleRating)
					assert.Equal(t, int32(200), lead.ReviewCount)
				}
			},
		},
		{
			name:      "[failure scenario] - nil leads",
			leads:     nil,
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name:      "[failure scenario] - empty leads slice",
			leads:     []*lead_scraper_servicev1.Lead{},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - leads with invalid IDs",
			setup: func(t *testing.T) []*lead_scraper_servicev1.Lead {
				invalidLeads := make([]*lead_scraper_servicev1.Lead, 2)
				invalidLeads[0] = &lead_scraper_servicev1.Lead{Id: 0}
				invalidLeads[1] = &lead_scraper_servicev1.Lead{Id: 999999}
				return invalidLeads
			},
			wantError: true,
			errType:   ErrInvalidInput,
		},
		{
			name: "[failure scenario] - context timeout",
			setup: func(t *testing.T) []*lead_scraper_servicev1.Lead {
				return createdLeads
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var leads []*lead_scraper_servicev1.Lead
			if tt.setup != nil {
				leads = tt.setup(t)
			} else {
				leads = tt.leads
			}

			ctx := context.Background()
			if tt.name == "[failure scenario] - context timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 1*time.Nanosecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond)
			}

			results, err := conn.BatchUpdateLeads(ctx, leads)

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

func TestBatchUpdateLeads_LargeBatch(t *testing.T) {
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

	// Create a large number of test leads
	numLeads := 1000 // This will test multiple batches
	createdLeads := make([]*lead_scraper_servicev1.Lead, numLeads)
	for i := 0; i < numLeads; i++ {
		lead := &lead_scraper_servicev1.Lead{
			Name:          fmt.Sprintf("Test Lead %d", i),
			Website:       fmt.Sprintf("https://test-lead-%d.com", i),
			Phone:         fmt.Sprintf("+%d", 1234567890+i),
			Address:       fmt.Sprintf("123 Test St %d", i),
			City:          "Test City",
			State:         "Test State",
			Country:       "Test Country",
			Industry:      "Technology",
			PlaceId:       fmt.Sprintf("ChIJ_test%d", i),
			GoogleMapsUrl: "https://maps.google.com/?q=40.7128,-74.0060",
			Latitude:      40.7128,
			Longitude:     -74.0060,
			GoogleRating:  4.5,
			ReviewCount:   100,
		}
		created, err := conn.CreateLead(context.Background(), createdJob.Id, lead)
		require.NoError(t, err)
		require.NotNil(t, created)
		createdLeads[i] = created
	}

	// Clean up after test
	defer func() {
		for _, lead := range createdLeads {
			if lead != nil {
				err := conn.DeleteLead(context.Background(), lead.Id, DeletionTypeSoft)
				require.NoError(t, err)
			}
		}
		err := conn.DeleteScrapingJob(context.Background(), createdJob.Id)
		require.NoError(t, err)
	}()

	// Prepare updates
	updatedLeads := make([]*lead_scraper_servicev1.Lead, len(createdLeads))
	for i, lead := range createdLeads {
		updatedLead := *lead
		updatedLead.Name = fmt.Sprintf("Updated Lead %d", i)
		updatedLead.Website = fmt.Sprintf("https://updated-lead-%d.com", i)
		updatedLead.Phone = fmt.Sprintf("+%d", 9876543210+i)
		updatedLead.Address = fmt.Sprintf("456 Updated St %d", i)
		updatedLead.City = "Updated City"
		updatedLead.State = "Updated State"
		updatedLead.Country = "Updated Country"
		updatedLead.Industry = "Updated Industry"
		updatedLead.PlaceId = fmt.Sprintf("ChIJ_updated%d", i)
		updatedLead.GoogleMapsUrl = "https://maps.google.com/?q=41.8781,-87.6298"
		updatedLead.Latitude = 41.8781
		updatedLead.Longitude = -87.6298
		updatedLead.GoogleRating = 4.8
		updatedLead.ReviewCount = 200
		updatedLeads[i] = &updatedLead
	}

	// Perform batch update
	results, err := conn.BatchUpdateLeads(context.Background(), updatedLeads)
	require.NoError(t, err)
	require.NotNil(t, results)
	assert.Len(t, results, numLeads)

	// Verify all leads were updated correctly
	for i, lead := range results {
		assert.NotNil(t, lead)
		assert.Equal(t, fmt.Sprintf("Updated Lead %d", i), lead.Name)
		assert.Equal(t, fmt.Sprintf("https://updated-lead-%d.com", i), lead.Website)
		assert.Equal(t, fmt.Sprintf("+%d", 9876543210+i), lead.Phone)
		assert.Equal(t, fmt.Sprintf("456 Updated St %d", i), lead.Address)
		assert.Equal(t, "Updated City", lead.City)
		assert.Equal(t, "Updated State", lead.State)
		assert.Equal(t, "Updated Country", lead.Country)
		assert.Equal(t, "Updated Industry", lead.Industry)
		assert.Equal(t, fmt.Sprintf("ChIJ_updated%d", i), lead.PlaceId)
		assert.Equal(t, "https://maps.google.com/?q=41.8781,-87.6298", lead.GoogleMapsUrl)
		assert.Equal(t, float64(41.8781), lead.Latitude)
		assert.Equal(t, float64(-87.6298), lead.Longitude)
		assert.Equal(t, float32(4.8), lead.GoogleRating)
		assert.Equal(t, int32(200), lead.ReviewCount)
	}
} 