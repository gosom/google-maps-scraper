package web_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventSerializationIntegration(t *testing.T) {
	// Test streaming event serialization/deserialization
	testEvent := plugins.StreamEvent{
		Type:      "COMPANY_SCRAPED",
		JobID:     testJobID,
		Data:      map[string]interface{}{"cid": "test-cid", "status": "new"},
		Timestamp: time.Now(),
	}

	// Verify event can be serialized
	eventData, err := json.Marshal(testEvent)
	require.NoError(t, err)

	var deserializedEvent plugins.StreamEvent
	err = json.Unmarshal(eventData, &deserializedEvent)
	require.NoError(t, err)

	assert.Equal(t, testEvent.Type, deserializedEvent.Type)
	assert.Equal(t, testEvent.JobID, deserializedEvent.JobID)
	assert.Equal(t, testEvent.Data, deserializedEvent.Data)
}

func TestStreamClientManagement(t *testing.T) {
	// Test basic streaming event creation and JSON handling
	testEvent := plugins.StreamEvent{
		Type:      "BATCH_START",
		JobID:     testJobID,
		Data:      map[string]interface{}{"keyword": "test", "total_expected": nil},
		Timestamp: time.Now(),
	}

	// Test that we can create and marshal events
	jsonData, err := json.Marshal(testEvent)
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), "BATCH_START")
	assert.Contains(t, string(jsonData), testJobID)
}
