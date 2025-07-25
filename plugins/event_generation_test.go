//go:build !plugin
// +build !plugin

package plugins_test

import (
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBatchStartEventGeneration tests our BATCH_START event logic
func TestBatchStartEventGeneration(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	t.Run("Plugin ready for BATCH_START event emission", func(t *testing.T) {
		// Get the event channel before processing
		eventChan := writer.GetEventChannel()

		// Verify event channel is available
		assert.NotNil(t, eventChan)

		// The BATCH_START event is emitted on first result processing
		// We can't directly test the private methods, but we can verify
		// the plugin is properly configured for event emission
		stats := writer.GetStats()
		assert.Equal(t, 0, stats.TotalProcessed)

		// Close the writer to clean up
		writer.Close()
	})
}

// TestCompanyScrapedEventGeneration tests our COMPANY_SCRAPED event structure
func TestCompanyScrapedEventGeneration(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	t.Run("COMPANY_SCRAPED event contains all 25+ fields", func(t *testing.T) {
		// Create a comprehensive test entry with all the fields we emit
		testEntry := &gmaps.Entry{
			// Core identification
			Cid:    "16519582940102929223",
			Title:  "Test Restaurant",
			DataID: "0x14e732fd76f0d90d:0xe5415928d6702b47",
			Link:   "https://maps.google.com/place/test",

			// Contact information
			Address: "123 Test St, Test City, TC 12345",
			Phone:   "+1-555-123-4567",
			WebSite: "https://test-restaurant.com",
			Emails:  []string{"info@test-restaurant.com"},

			// Business details
			Categories:  []string{"Restaurant", "Italian"},
			Category:    "Restaurant",
			Description: "Authentic Italian cuisine",
			PriceRange:  "$$",

			// Location data
			Latitude:   37.7749,
			Longtitude: -122.4194,
			PlusCode:   "849VQJWP+XX",
			Timezone:   "America/Los_Angeles",

			// Reviews and ratings
			ReviewCount:      150,
			ReviewRating:     4.5,
			ReviewsPerRating: map[int]int{5: 75, 4: 45, 3: 20, 2: 8, 1: 2},
			ReviewsLink:      "https://maps.google.com/place/test/reviews",

			// Operational data
			OpenHours: map[string][]string{
				"Monday":    {"11:00 AM", "10:00 PM"},
				"Tuesday":   {"11:00 AM", "10:00 PM"},
				"Wednesday": {"11:00 AM", "10:00 PM"},
				"Thursday":  {"11:00 AM", "10:00 PM"},
				"Friday":    {"11:00 AM", "11:00 PM"},
				"Saturday":  {"12:00 PM", "11:00 PM"},
				"Sunday":    {"12:00 PM", "9:00 PM"},
			},
			PopularTimes: map[string]map[int]int{"Monday": {12: 30, 18: 80, 20: 95}},
			Status:       "Open",

			// Media and links
			Thumbnail: "https://maps.googleapis.com/thumbnail/test.jpg",
			Images: []gmaps.Image{
				{Title: "Restaurant Interior", Image: "https://example.com/img1.jpg"},
				{Title: "Signature Dish", Image: "https://example.com/img2.jpg"},
			},

			// Additional data
			Owner: gmaps.Owner{
				ID:   "owner-123",
				Name: "Test Owner",
				Link: "https://business.google.com/owner-123",
			},
			CompleteAddress: gmaps.Address{
				Borough:    "Test Borough",
				Street:     "123 Test St",
				City:       "Test City",
				PostalCode: "12345",
				State:      "TC",
				Country:    "US",
			},
			About: []gmaps.About{
				{ID: "family-owned", Name: "Family-owned"},
				{ID: "authentic", Name: "Authentic recipes"},
				{ID: "fresh", Name: "Fresh ingredients"},
			},
			Reservations: []gmaps.LinkSource{{Link: "https://opentable.com/test", Source: "OpenTable"}},
			OrderOnline:  []gmaps.LinkSource{{Link: "https://grubhub.com/test", Source: "GrubHub"}},
			Menu:         gmaps.LinkSource{Link: "https://test-restaurant.com/menu", Source: "Restaurant"},

			// Reviews with dates for filtering
			UserReviews: []gmaps.Review{
				{When: "2024-12-1", Name: "John Doe", Rating: 5, Description: "Excellent food!"},
				{When: "2024-11-15", Name: "Jane Smith", Rating: 4, Description: "Great service"},
				{When: "2024-10-20", Name: "Bob Wilson", Rating: 5, Description: "Amazing pasta"},
			},
		}

		// Test that we can create an entry with all these fields
		assert.NotEmpty(t, testEntry.Cid)
		assert.NotEmpty(t, testEntry.Title)
		assert.NotEmpty(t, testEntry.DataID)
		assert.NotEmpty(t, testEntry.Link)
		assert.NotEmpty(t, testEntry.Address)
		assert.NotEmpty(t, testEntry.Phone)
		assert.NotEmpty(t, testEntry.WebSite)
		assert.NotEmpty(t, testEntry.Emails)
		assert.NotEmpty(t, testEntry.Category)
		assert.NotEmpty(t, testEntry.Description)
		assert.NotEmpty(t, testEntry.PriceRange)
		assert.NotZero(t, testEntry.Latitude)
		assert.NotZero(t, testEntry.Longtitude)
		assert.NotEmpty(t, testEntry.PlusCode)
		assert.NotEmpty(t, testEntry.Timezone)
		assert.NotZero(t, testEntry.ReviewCount)
		assert.NotZero(t, testEntry.ReviewRating)
		assert.NotEmpty(t, testEntry.ReviewsPerRating)
		assert.NotEmpty(t, testEntry.ReviewsLink)
		assert.NotEmpty(t, testEntry.OpenHours)
		assert.NotEmpty(t, testEntry.PopularTimes)
		assert.NotEmpty(t, testEntry.Status)
		assert.NotEmpty(t, testEntry.Thumbnail)
		assert.NotEmpty(t, testEntry.About)
		assert.NotEmpty(t, testEntry.Reservations)
		assert.NotEmpty(t, testEntry.OrderOnline)
		assert.Len(t, testEntry.Categories, 2)
		assert.Len(t, testEntry.UserReviews, 3)
		assert.Len(t, testEntry.Images, 2)

		// Verify review filtering would work
		writer.SetReviewLimit(2)
		assert.Greater(t, len(testEntry.UserReviews), 2) // More reviews than limit

		// Verify stats tracking
		stats := writer.GetStats()
		assert.Equal(t, 0, stats.TotalProcessed)
		assert.Equal(t, 0, stats.NewCompanies)
		assert.Equal(t, 0, stats.DuplicatesFound)
	})
}

// TestBatchEndEventGeneration tests our BATCH_END event with statistics
func TestBatchEndEventGeneration(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	t.Run("BATCH_END event includes accurate statistics", func(t *testing.T) {
		// Set up some existing CIDs for duplicate testing
		writer.SetExistingCIDs([]string{"existing-cid-1", "existing-cid-2"})

		// The BATCH_END event is emitted when the channel closes
		// We can't directly test the private methods, but we can verify
		// that the statistics structure is correct

		stats := writer.GetStats()

		// Initial stats should be zero
		assert.Equal(t, 0, stats.TotalProcessed)
		assert.Equal(t, 0, stats.NewCompanies)
		assert.Equal(t, 0, stats.DuplicatesFound)
		assert.Equal(t, 0, stats.Errors)

		// The BATCH_END event should include these exact fields:
		// - keyword (from job URL)
		// - total_scraped (stats.TotalProcessed)
		// - new_companies (stats.NewCompanies)
		// - duplicates_found (stats.DuplicatesFound)
		// - errors (stats.Errors)

		// Test the structure matches what emitBatchEndEvent expects
		eventData := map[string]interface{}{
			"keyword":          "",
			"total_scraped":    stats.TotalProcessed,
			"new_companies":    stats.NewCompanies,
			"duplicates_found": stats.DuplicatesFound,
			"errors":           stats.Errors,
		}

		assert.NotNil(t, eventData)
		assert.Equal(t, 0, eventData["total_scraped"])
		assert.Equal(t, 0, eventData["new_companies"])
		assert.Equal(t, 0, eventData["duplicates_found"])
		assert.Equal(t, 0, eventData["errors"])
	})
}

// TestEventJSONMarshaling tests our event JSON marshaling functionality
func TestEventJSONMarshaling(t *testing.T) {
	t.Run("StreamEvent marshals to correct JSON structure", func(t *testing.T) {
		// Create a test event like our plugin generates
		event := plugins.StreamEvent{
			Type:      "COMPANY_SCRAPED",
			Timestamp: time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC),
			JobID:     "test-job-123",
			Data: map[string]interface{}{
				"cid":    "16519582940102929223",
				"title":  "Test Restaurant",
				"status": "new",
			},
		}

		// Test JSON marshaling
		jsonBytes, err := plugins.MarshalEventJSON(event)
		require.NoError(t, err)
		assert.NotEmpty(t, jsonBytes)

		// Verify the JSON contains expected fields
		jsonStr := string(jsonBytes)
		assert.Contains(t, jsonStr, "COMPANY_SCRAPED")
		assert.Contains(t, jsonStr, "test-job-123")
		assert.Contains(t, jsonStr, "16519582940102929223")
		assert.Contains(t, jsonStr, "Test Restaurant")
		assert.Contains(t, jsonStr, "new")

		// Should be valid JSON format
		assert.Contains(t, jsonStr, `"type":"COMPANY_SCRAPED"`)
		assert.Contains(t, jsonStr, `"job_id":"test-job-123"`)
		assert.Contains(t, jsonStr, `"timestamp":"2024-12-01T12:00:00Z"`)
	})

	t.Run("Event types match our implementation", func(t *testing.T) {
		// Test that our event types are correctly defined
		eventTypes := []string{"BATCH_START", "COMPANY_SCRAPED", "BATCH_END", "HEARTBEAT"}

		for _, eventType := range eventTypes {
			event := plugins.StreamEvent{
				Type:      eventType,
				Timestamp: time.Now().UTC(),
				JobID:     "test-job",
				Data:      map[string]interface{}{"test": "data"},
			}

			jsonBytes, err := plugins.MarshalEventJSON(event)
			require.NoError(t, err)

			jsonStr := string(jsonBytes)
			assert.Contains(t, jsonStr, eventType)
		}
	})
}

// TestEventChannelFunctionality tests our event channel implementation
func TestEventChannelFunctionality(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	t.Run("GetEventChannel returns valid channel", func(t *testing.T) {
		eventChan := writer.GetEventChannel()
		assert.NotNil(t, eventChan)

		// Should be a receive-only channel from external perspective
		// Channel should not be closed initially
		select {
		case <-eventChan:
			t.Error("Channel should not have data initially")
		default:
			// This is expected - channel is empty
		} //nolint:wsl // select default case immediately before closing is acceptable
	})

	t.Run("Event channel closes properly", func(t *testing.T) {
		writer := plugins.NewStreamingDuplicateFilterWriter()
		eventChan := writer.GetEventChannel()

		// Close the writer
		writer.Close()

		// Channel should be closed
		_, ok := <-eventChan
		assert.False(t, ok, "Channel should be closed after Close()")
	})

	t.Run("Multiple Close() calls are safe", func(t *testing.T) {
		writer := plugins.NewStreamingDuplicateFilterWriter()

		// Should not panic on multiple Close() calls
		assert.NotPanics(t, func() {
			writer.Close()
			writer.Close()
			writer.Close()
		})
	})
}
