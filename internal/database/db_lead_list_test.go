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

func TestListLeads(t *testing.T) {
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
	numLeads := 5
	leadIDs := make([]uint64, numLeads)

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
				for i, lead := range leads {
					assert.NotNil(t, lead)
					assert.NotZero(t, lead.Id)
					assert.Equal(t, fmt.Sprintf("Test Lead %d", i), lead.Name)
					assert.Equal(t, fmt.Sprintf("https://test-lead-%d.com", i), lead.Website)
					assert.Equal(t, fmt.Sprintf("+%d", 1234567890+i), lead.Phone)
					assert.Equal(t, fmt.Sprintf("123 Test St %d", i), lead.Address)
					assert.Equal(t, "Test City", lead.City)
					assert.Equal(t, "Test State", lead.State)
					assert.Equal(t, "Test Country", lead.Country)
					assert.Equal(t, "Technology", lead.Industry)
					assert.Equal(t, fmt.Sprintf("ChIJ_test%d", i), lead.PlaceId)
					assert.Equal(t, "https://maps.google.com/?q=40.7128,-74.0060", lead.GoogleMapsUrl)
					assert.Equal(t, float64(40.7128), lead.Latitude)
					assert.Equal(t, float64(-74.0060), lead.Longitude)
					assert.Equal(t, float32(4.5), lead.GoogleRating)
					assert.Equal(t, int32(100), lead.ReviewCount)
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
				for i, lead := range leads {
					assert.NotNil(t, lead)
					assert.NotZero(t, lead.Id)
					assert.Equal(t, fmt.Sprintf("Test Lead %d", i), lead.Name)
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
				for i, lead := range leads {
					assert.NotNil(t, lead)
					assert.NotZero(t, lead.Id)
					assert.Equal(t, fmt.Sprintf("Test Lead %d", i+3), lead.Name)
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
	results, err := conn.ListLeads(context.Background(), 10, 0)
	require.NoError(t, err)
	assert.Empty(t, results)
} 