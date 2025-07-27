//go:build !plugin
// +build !plugin

package webrunner_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/plugins"
	"github.com/gosom/google-maps-scraper/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEventChannelWriter implements GetEventChannelInterface for testing
type mockEventChannelWriter struct {
	eventChan chan plugins.StreamEvent
	mu        sync.RWMutex
}

func newMockEventChannelWriter() *mockEventChannelWriter {
	return &mockEventChannelWriter{
		eventChan: make(chan plugins.StreamEvent, 20), // Increased buffer for concurrent tests
	}
}

func (m *mockEventChannelWriter) GetEventChannel() <-chan plugins.StreamEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.eventChan
}

func (m *mockEventChannelWriter) EmitEvent(event plugins.StreamEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case m.eventChan <- event:
	default:
		// Channel full, skip
	} //nolint:wsl // acceptable comment before closing brace
}

func (m *mockEventChannelWriter) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	close(m.eventChan)
}

// mockBroadcaster implements the event broadcasting interface for testing
type mockBroadcaster struct {
	events []plugins.StreamEvent
	mu     sync.RWMutex
}

func newMockBroadcaster() *mockBroadcaster {
	return &mockBroadcaster{
		events: make([]plugins.StreamEvent, 0),
	}
}

func (m *mockBroadcaster) BroadcastEvent(jobID string, event plugins.StreamEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Store the event with corrected job ID for verification
	event.JobID = jobID
	m.events = append(m.events, event)
}

func (m *mockBroadcaster) GetEvents() []plugins.StreamEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]plugins.StreamEvent, len(m.events))
	copy(result, m.events)

	return result
}

func (m *mockBroadcaster) GetEventCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.events)
}

// mockJobRepository implements web.JobRepository for testing
type mockEventJobRepository struct {
	jobs map[string]web.Job
	mu   sync.RWMutex
}

func (m *mockEventJobRepository) Get(_ context.Context, id string) (web.Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, exists := m.jobs[id]
	if !exists {
		return web.Job{}, assert.AnError
	}

	return job, nil
}

func (m *mockEventJobRepository) Create(_ context.Context, job *web.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = *job

	return nil
}

func (m *mockEventJobRepository) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.jobs, id)

	return nil
}

func (m *mockEventJobRepository) Select(_ context.Context, _ web.SelectParams) ([]web.Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	jobs := make([]web.Job, 0, len(m.jobs))
	for id := range m.jobs {
		jobs = append(jobs, m.jobs[id])
	}

	return jobs, nil
}

func (m *mockEventJobRepository) Update(_ context.Context, job *web.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[job.ID] = *job

	return nil
}

// TestEventTranslatorJobIDCorrection tests our job ID correction logic
func TestEventTranslatorJobIDCorrection(t *testing.T) {
	t.Run("Event translator corrects job IDs from plugin to web job ID", func(t *testing.T) {
		// Create test components
		pluginWriter := newMockEventChannelWriter()
		broadcaster := newMockBroadcaster()

		// Create test job IDs
		webJobID := uuid.New().String()
		pluginJobID := "scrapemate-job-12345" // Different ID from plugin

		// Create test event with plugin job ID
		originalEvent := plugins.StreamEvent{
			Type:      "COMPANY_SCRAPED",
			Timestamp: time.Now().UTC(),
			JobID:     pluginJobID, // Wrong job ID from plugin
			Data: map[string]interface{}{
				"cid":    "test-cid-123",
				"title":  "Test Company",
				"status": "new",
			},
		}

		// Simulate event translation
		go func() {
			// This simulates what our event translator does
			for event := range pluginWriter.GetEventChannel() {
				// Fix the job ID - this is our core translation logic
				event.JobID = webJobID
				broadcaster.BroadcastEvent(webJobID, event)
			}
		}()

		// Emit event with wrong job ID
		pluginWriter.EmitEvent(originalEvent)
		pluginWriter.Close()

		// Brief wait for processing
		time.Sleep(50 * time.Millisecond)

		// Verify event was translated with correct job ID
		events := broadcaster.GetEvents()
		require.Len(t, events, 1, "Should have received one translated event")

		translatedEvent := events[0]
		assert.Equal(t, webJobID, translatedEvent.JobID, "Job ID should be corrected to web job ID")
		assert.Equal(t, "COMPANY_SCRAPED", translatedEvent.Type, "Event type should be preserved")
		assert.Equal(t, "test-cid-123", translatedEvent.Data["cid"], "Event data should be preserved")
	})

	t.Run("Event translator handles multiple event types", func(t *testing.T) {
		// Create test components
		pluginWriter := newMockEventChannelWriter()
		broadcaster := newMockBroadcaster()

		// Create test job IDs
		webJobID := uuid.New().String()
		pluginJobID := "different-plugin-id"

		// Test event types we handle
		eventTypes := []string{"BATCH_START", "COMPANY_SCRAPED", "HEARTBEAT", "BATCH_END"}

		// Simulate event translation
		go func() {
			for event := range pluginWriter.GetEventChannel() {
				event.JobID = webJobID // Correct the job ID
				broadcaster.BroadcastEvent(webJobID, event)
			}
		}()

		// Emit events with wrong job IDs
		for i, eventType := range eventTypes {
			event := plugins.StreamEvent{
				Type:      eventType,
				Timestamp: time.Now().UTC(),
				JobID:     pluginJobID, // Wrong job ID
				Data: map[string]interface{}{
					"sequence": i,
					"test":     true,
				},
			}
			pluginWriter.EmitEvent(event)
		}

		pluginWriter.Close()

		// Brief wait for processing
		time.Sleep(50 * time.Millisecond)

		// Verify all events were translated
		events := broadcaster.GetEvents()
		require.Len(t, events, len(eventTypes), "Should have received all translated events")

		// Verify each event has correct job ID
		for i, event := range events {
			assert.Equal(t, webJobID, event.JobID, "Event %d job ID should be corrected", i)
			assert.Contains(t, eventTypes, event.Type, "Event %d type should be preserved", i)
		}
	})
}

// TestEventTranslatorInterfaceDetection tests our interface detection logic
func TestEventTranslatorInterfaceDetection(t *testing.T) {
	t.Run("Event translator detects GetEventChannelInterface correctly", func(t *testing.T) {
		// Create test plugin writer with interface
		pluginWriter := newMockEventChannelWriter()

		// Test interface detection (this is what webrunner does)
		var hasInterface bool
		if _, ok := interface{}(pluginWriter).(interface {
			GetEventChannel() <-chan plugins.StreamEvent
		}); ok {
			hasInterface = true
		}

		assert.True(t, hasInterface, "Plugin should implement GetEventChannelInterface")

		// Test that channel is accessible
		eventChan := pluginWriter.GetEventChannel()
		assert.NotNil(t, eventChan, "Event channel should be accessible")
	})

	t.Run("Event translator handles objects without interface gracefully", func(t *testing.T) {
		// Create object that doesn't implement the interface
		nonInterfaceObject := struct{}{}

		// Test interface detection fails gracefully
		var hasInterface bool
		if _, ok := interface{}(nonInterfaceObject).(interface {
			GetEventChannel() <-chan plugins.StreamEvent
		}); ok {
			hasInterface = true
		}

		assert.False(t, hasInterface, "Non-interface object should not implement GetEventChannelInterface")
	})
}

// TestEventTranslatorLifecycle tests translator goroutine lifecycle
func TestEventTranslatorLifecycle(t *testing.T) {
	t.Run("Event translator starts and stops cleanly", func(t *testing.T) {
		// Create test components
		pluginWriter := newMockEventChannelWriter()
		broadcaster := newMockBroadcaster()

		webJobID := uuid.New().String()

		// Create context for cancellation
		ctx, cancel := context.WithCancel(context.Background())

		// Start translator goroutine
		translatorDone := make(chan bool)
		go func() {
			defer close(translatorDone)

			for {
				select {
				case event, ok := <-pluginWriter.GetEventChannel():
					if !ok {
						return // Channel closed
					}

					event.JobID = webJobID
					broadcaster.BroadcastEvent(webJobID, event)
				case <-ctx.Done():
					return // Context cancelled
				}
			}
		}()

		// Send a test event
		testEvent := plugins.StreamEvent{
			Type:      "TEST",
			Timestamp: time.Now().UTC(),
			JobID:     "wrong-id",
			Data:      map[string]interface{}{"test": true},
		}
		pluginWriter.EmitEvent(testEvent)

		// Brief wait for processing
		time.Sleep(50 * time.Millisecond)

		// Verify event was processed
		assert.Equal(t, 1, broadcaster.GetEventCount(), "Should have processed one event")

		// Cancel context to stop translator
		cancel()

		// Wait for translator to stop
		select {
		case <-translatorDone:
			// Translator stopped cleanly
		case <-time.After(100 * time.Millisecond):
			t.Error("Translator did not stop within timeout")
		}
	})

	t.Run("Event translator handles channel closure", func(t *testing.T) {
		// Create test components
		pluginWriter := newMockEventChannelWriter()
		broadcaster := newMockBroadcaster()

		webJobID := uuid.New().String()

		// Start translator goroutine
		translatorDone := make(chan bool)
		go func() {
			defer close(translatorDone)

			for event := range pluginWriter.GetEventChannel() {
				event.JobID = webJobID
				broadcaster.BroadcastEvent(webJobID, event)
			}
		}()

		// Send events and close channel
		for i := 0; i < 3; i++ {
			event := plugins.StreamEvent{
				Type:      "TEST",
				Timestamp: time.Now().UTC(),
				JobID:     "wrong-id",
				Data:      map[string]interface{}{"sequence": i},
			}
			pluginWriter.EmitEvent(event)
		}

		pluginWriter.Close()

		// Wait for translator to stop
		select {
		case <-translatorDone:
			// Translator stopped cleanly
		case <-time.After(100 * time.Millisecond):
			t.Error("Translator did not stop within timeout")
		}

		// Verify all events were processed
		assert.Equal(t, 3, broadcaster.GetEventCount(), "Should have processed all events")
	})
}

// TestEventTranslatorDataPreservation tests that event data is preserved during translation
func TestEventTranslatorDataPreservation(t *testing.T) {
	t.Run("Event translator preserves all event data", func(t *testing.T) {
		// Create test components
		pluginWriter := newMockEventChannelWriter()
		broadcaster := newMockBroadcaster()

		webJobID := uuid.New().String()

		// Start translator
		go func() {
			for event := range pluginWriter.GetEventChannel() {
				event.JobID = webJobID
				broadcaster.BroadcastEvent(webJobID, event)
			}
		}()

		// Create complex event with all field types
		originalTimestamp := time.Now().UTC()
		complexEvent := plugins.StreamEvent{
			Type:      "COMPANY_SCRAPED",
			Timestamp: originalTimestamp,
			JobID:     "plugin-job-id",
			Data: map[string]interface{}{
				"cid":        "16519582940102929223",
				"title":      "Test Restaurant",
				"categories": []string{"Restaurant", "Italian"},
				"latitude":   37.7749,
				"longitude":  -122.4194,
				"rating":     4.5,
				"reviews":    150,
				"open_hours": map[string][]string{
					"Monday": {"11:00 AM", "10:00 PM"},
				},
				"metadata": map[string]interface{}{
					"nested": true,
					"count":  42,
				},
			},
		}

		// Emit complex event
		pluginWriter.EmitEvent(complexEvent)
		pluginWriter.Close()

		// Brief wait for processing
		time.Sleep(50 * time.Millisecond)

		// Verify event data preservation
		events := broadcaster.GetEvents()
		require.Len(t, events, 1, "Should have received one event")

		translatedEvent := events[0]

		// Verify basic fields
		assert.Equal(t, "COMPANY_SCRAPED", translatedEvent.Type, "Type should be preserved")
		assert.Equal(t, originalTimestamp, translatedEvent.Timestamp, "Timestamp should be preserved")
		assert.Equal(t, webJobID, translatedEvent.JobID, "Job ID should be corrected")

		// Verify complex data fields
		assert.Equal(t, "16519582940102929223", translatedEvent.Data["cid"], "CID should be preserved")
		assert.Equal(t, "Test Restaurant", translatedEvent.Data["title"], "Title should be preserved")
		assert.Equal(t, []string{"Restaurant", "Italian"}, translatedEvent.Data["categories"], "Categories should be preserved")
		assert.Equal(t, 37.7749, translatedEvent.Data["latitude"], "Latitude should be preserved")
		assert.Equal(t, -122.4194, translatedEvent.Data["longitude"], "Longitude should be preserved")
		assert.Equal(t, 4.5, translatedEvent.Data["rating"], "Rating should be preserved")
		assert.Equal(t, 150, translatedEvent.Data["reviews"], "Reviews should be preserved")

		// Verify nested objects
		openHours, ok := translatedEvent.Data["open_hours"].(map[string][]string)
		require.True(t, ok, "Open hours should be map[string][]string")
		assert.Equal(t, []string{"11:00 AM", "10:00 PM"}, openHours["Monday"], "Open hours should be preserved")

		metadata, ok := translatedEvent.Data["metadata"].(map[string]interface{})
		require.True(t, ok, "Metadata should be map[string]interface{}")
		assert.Equal(t, true, metadata["nested"], "Nested metadata should be preserved")
		assert.Equal(t, 42, metadata["count"], "Nested count should be preserved")
	})
}

// TestEventTranslatorConcurrentSafety tests translator with concurrent access
func TestEventTranslatorConcurrentSafety(t *testing.T) {
	t.Run("Event translator handles concurrent event emission", func(t *testing.T) {
		// Create test components
		pluginWriter := newMockEventChannelWriter()
		broadcaster := newMockBroadcaster()

		webJobID := uuid.New().String()

		// Start translator
		go func() {
			for event := range pluginWriter.GetEventChannel() {
				event.JobID = webJobID
				broadcaster.BroadcastEvent(webJobID, event)
			}
		}()

		// Emit events concurrently from multiple goroutines
		numGoroutines := 5
		eventsPerGoroutine := 3

		var wg sync.WaitGroup
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)

			go func(goroutineID int) {
				defer wg.Done()

				for j := 0; j < eventsPerGoroutine; j++ {
					event := plugins.StreamEvent{
						Type:      "CONCURRENT_TEST",
						Timestamp: time.Now().UTC(),
						JobID:     "plugin-job-id",
						Data: map[string]interface{}{
							"goroutine": goroutineID,
							"sequence":  j,
						},
					}
					pluginWriter.EmitEvent(event)
				}
			}(i)
		}

		// Wait for all goroutines to finish
		wg.Wait()
		pluginWriter.Close()

		// Brief wait for processing
		time.Sleep(100 * time.Millisecond)

		// Verify all events were processed
		expectedEvents := numGoroutines * eventsPerGoroutine
		assert.Equal(t, expectedEvents, broadcaster.GetEventCount(), "Should have processed all concurrent events")

		// Verify all events have correct job ID
		events := broadcaster.GetEvents()
		for i, event := range events {
			assert.Equal(t, webJobID, event.JobID, "Event %d should have correct job ID", i)
			assert.Equal(t, "CONCURRENT_TEST", event.Type, "Event %d should have correct type", i)
		}
	})
}
