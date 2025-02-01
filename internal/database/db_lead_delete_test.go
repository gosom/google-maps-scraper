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

func TestDeleteLead(t *testing.T) {
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

	// Create a test lead
	testLead := &lead_scraper_servicev1.Lead{
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

	createdLead, err := conn.CreateLead(context.Background(), createdJob.Id, testLead)
	require.NoError(t, err)
	require.NotNil(t, createdLead)

	// Clean up job after all tests
	defer func() {
		if createdJob != nil {
			err := conn.DeleteScrapingJob(context.Background(), createdJob.Id)
			require.NoError(t, err)
		}
	}()

	tests := []struct {
		name         string
		id           uint64
		deletionType DeletionType
		wantError    bool
		errType      error
		setup        func(t *testing.T) uint64
		validate     func(t *testing.T, id uint64)
	}{
		{
			name:         "[success scenario] - soft delete",
			id:           createdLead.Id,
			deletionType: DeletionTypeSoft,
			wantError:    false,
			validate: func(t *testing.T, id uint64) {
				// Verify the lead was soft deleted
				_, err := conn.GetLead(context.Background(), id)
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrJobDoesNotExist)
			},
		},
		{
			name: "[success scenario] - hard delete",
			setup: func(t *testing.T) uint64 {
				// Create a new lead for hard delete
				lead := &lead_scraper_servicev1.Lead{
					Name:          "Test Lead for Hard Delete",
					Website:       "https://test-lead-hard-delete.com",
					Phone:         "+1234567890",
					Address:       "123 Test St",
					City:          "Test City",
					State:         "Test State",
					Country:       "Test Country",
					Industry:      "Technology",
					PlaceId:       "ChIJ_test_hard_delete",
					GoogleMapsUrl: "https://maps.google.com/?q=40.7128,-74.0060",
					Latitude:      40.7128,
					Longitude:     -74.0060,
					GoogleRating:  4.5,
					ReviewCount:   100,
				}
				created, err := conn.CreateLead(context.Background(), createdJob.Id, lead)
				require.NoError(t, err)
				require.NotNil(t, created)
				return created.Id
			},
			deletionType: DeletionTypeHard,
			wantError:    false,
			validate: func(t *testing.T, id uint64) {
				// Verify the lead was hard deleted
				_, err := conn.GetLead(context.Background(), id)
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrJobDoesNotExist)
			},
		},
		{
			name:         "[failure scenario] - invalid id",
			id:           0,
			deletionType: DeletionTypeSoft,
			wantError:    true,
			errType:      ErrInvalidInput,
		},
		{
			name:         "[failure scenario] - non-existent id",
			id:           999999,
			deletionType: DeletionTypeSoft,
			wantError:    true,
			errType:      ErrJobDoesNotExist,
		},
		{
			name:         "[failure scenario] - already deleted lead",
			deletionType: DeletionTypeSoft,
			wantError:    true,
			errType:      ErrJobDoesNotExist,
			setup: func(t *testing.T) uint64 {
				// Create and delete a lead
				lead := &lead_scraper_servicev1.Lead{
					Name:          "Test Lead for Already Deleted",
					Website:       "https://test-lead-already-deleted.com",
					Phone:         "+1234567890",
					Address:       "123 Test St",
					City:          "Test City",
					State:         "Test State",
					Country:       "Test Country",
					Industry:      "Technology",
					PlaceId:       "ChIJ_test_already_deleted",
					GoogleMapsUrl: "https://maps.google.com/?q=40.7128,-74.0060",
					Latitude:      40.7128,
					Longitude:     -74.0060,
					GoogleRating:  4.5,
					ReviewCount:   100,
				}
				created, err := conn.CreateLead(context.Background(), createdJob.Id, lead)
				require.NoError(t, err)
				require.NotNil(t, created)

				err = conn.DeleteLead(context.Background(), created.Id, DeletionTypeSoft)
				require.NoError(t, err)

				return created.Id
			},
		},
		{
			name:         "[failure scenario] - context timeout",
			id:           createdLead.Id,
			deletionType: DeletionTypeSoft,
			wantError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var id uint64
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

			err := conn.DeleteLead(ctx, id, tt.deletionType)

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

func TestDeleteLead_ConcurrentDeletions(t *testing.T) {
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

	// Create test leads
	numLeads := 5
	createdLeads := make([]uint64, numLeads)
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
		createdLeads[i] = created.Id
	}

	// Clean up job after test
	defer func() {
		err := conn.DeleteScrapingJob(context.Background(), createdJob.Id)
		require.NoError(t, err)
	}()

	var wg sync.WaitGroup
	errors := make(chan error, numLeads)

	// Delete leads concurrently
	for i := 0; i < numLeads; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			if err := conn.DeleteLead(context.Background(), id, DeletionTypeSoft); err != nil {
				errors <- err
			}
		}(createdLeads[i])
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}
	require.Empty(t, errs, "Expected no errors during concurrent deletions, got: %v", errs)

	// Verify all leads were deleted
	for _, id := range createdLeads {
		_, err := conn.GetLead(context.Background(), id)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrJobDoesNotExist)
	}
} 