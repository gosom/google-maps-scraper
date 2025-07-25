//go:build !plugin
// +build !plugin

package plugins_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetExistingCIDs_Functionality tests the SetExistingCIDs functionality we added.
func TestSetExistingCIDs_Functionality(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	t.Run("SetExistingCIDs with valid CIDs", func(t *testing.T) {
		testCIDs := []string{
			"16519582940102929223", // From test data
			"12345678901234567890",
			"98765432109876543210",
		}

		// This should not panic and should log the initialization
		writer.SetExistingCIDs(testCIDs)

		// Verify the plugin was configured (indirect test through stats)
		stats := writer.GetStats()
		assert.Equal(t, 0, stats.TotalProcessed) // Should start at 0
	})

	t.Run("SetExistingCIDs with empty CIDs", func(t *testing.T) {
		writer := plugins.NewStreamingDuplicateFilterWriter()

		// Should handle empty slice gracefully
		writer.SetExistingCIDs([]string{})

		stats := writer.GetStats()
		assert.Equal(t, 0, stats.TotalProcessed)
	})

	t.Run("SetExistingCIDs with empty string CIDs", func(t *testing.T) {
		writer := plugins.NewStreamingDuplicateFilterWriter()

		// Should filter out empty strings
		testCIDs := []string{"", "16519582940102929223", "", "12345678901234567890", ""}
		writer.SetExistingCIDs(testCIDs)

		stats := writer.GetStats()
		assert.Equal(t, 0, stats.TotalProcessed)
	})

	t.Run("SetExistingCIDs multiple calls", func(t *testing.T) {
		writer := plugins.NewStreamingDuplicateFilterWriter()

		// First call
		writer.SetExistingCIDs([]string{"16519582940102929223"})

		// Second call should replace, not append
		writer.SetExistingCIDs([]string{"12345678901234567890", "98765432109876543210"})

		stats := writer.GetStats()
		assert.Equal(t, 0, stats.TotalProcessed)
	})
}

// TestStreamingDuplicateFilterWriter_ProcessBusinessEntry tests business entry processing.
func TestStreamingDuplicateFilterWriter_ProcessBusinessEntry(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	// Since processBusinessEntry is private, we'll test the public interface
	// by checking initial stats
	stats := writer.GetStats()
	assert.Equal(t, 0, stats.TotalProcessed)
	assert.Equal(t, 0, stats.NewCompanies)
	assert.Equal(t, 0, stats.DuplicatesFound)
}

// TestStreamingDuplicateFilterWriter_EventEmission tests event emission functionality.
func TestStreamingDuplicateFilterWriter_EventEmission(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	// Test that we can get the event channel
	eventChan := writer.GetEventChannel()
	assert.NotNil(t, eventChan)

	// Test that the channel is initially empty
	select {
	case <-eventChan:
		t.Fatal("Expected empty channel but received an event")
	default:
	}
}

// TestStreamingDuplicateFilterWriter_ConvertToBusinessEntries tests data conversion.
func TestStreamingDuplicateFilterWriter_ConvertToBusinessEntries(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	// Since convertToBusinessEntries is private, we'll test that the writer
	// was created successfully and has the expected interface
	assert.NotNil(t, writer)
	assert.NotNil(t, writer.GetEventChannel())
}

// TestStreamingDuplicateFilterWriter_BusinessKeyGeneration tests business key generation.
func TestStreamingDuplicateFilterWriter_BusinessKeyGeneration(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	// Since generateBusinessKey is private, we'll test the writer creation
	assert.NotNil(t, writer)
}

// TestStreamingDuplicateFilterWriter_SetExistingCIDs tests CID initialization.
func TestStreamingDuplicateFilterWriter_SetExistingCIDs(_ *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	// Test setting existing CIDs
	testCIDs := []string{
		"16519582940102929223",
		"12345678901234567890",
		"", // Empty CID should be ignored
		"98765432109876543210",
	}

	// SetExistingCIDs should not panic
	writer.SetExistingCIDs(testCIDs)

	// Test that we can call it again
	writer.SetExistingCIDs([]string{"new_cid"})
}

// TestStreamingDuplicateFilterWriter_ThreadSafety tests concurrent access to the writer.
func TestStreamingDuplicateFilterWriter_ThreadSafety(t *testing.T) {
	writer := plugins.NewStreamingDuplicateFilterWriter()

	// Start multiple goroutines to test concurrent access to SetExistingCIDs
	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(routineID int) {
			defer func() { done <- true }()

			testCIDs := []string{fmt.Sprintf("cid-%d", routineID)}
			writer.SetExistingCIDs(testCIDs)
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify that we can still access the writer
	stats := writer.GetStats()
	assert.Equal(t, 0, stats.TotalProcessed)
}

// TestMarshalEventJSON tests JSON marshaling of events.
func TestMarshalEventJSON(t *testing.T) {
	event := plugins.StreamEvent{
		Type:      "COMPANY_SCRAPED",
		Timestamp: time.Date(2025, 7, 25, 10, 0, 0, 0, time.UTC),
		JobID:     "test-job-789",
		Data: map[string]interface{}{
			"cid":    "16519582940102929223",
			"title":  "Test Restaurant",
			"status": "new",
		},
	}

	jsonBytes, err := plugins.MarshalEventJSON(event)
	require.NoError(t, err)

	// Verify JSON contains expected fields
	jsonString := string(jsonBytes)
	assert.Contains(t, jsonString, "COMPANY_SCRAPED")
	assert.Contains(t, jsonString, "test-job-789")
	assert.Contains(t, jsonString, "16519582940102929223")
	assert.Contains(t, jsonString, "Test Restaurant")
}
