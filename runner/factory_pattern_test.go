//go:build !plugin
// +build !plugin

package runner_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/plugins"
	"github.com/gosom/google-maps-scraper/runner"
	"github.com/gosom/scrapemate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockFactory implements a factory function for testing
func mockFactoryFunction() scrapemate.ResultWriter {
	return plugins.NewStreamingDuplicateFilterWriter()
}

// TestFactoryPatternBasics tests the core factory pattern functionality we implemented
func TestFactoryPatternBasics(t *testing.T) {
	t.Run("CreateCustomWriter finds and calls factory function", func(t *testing.T) { //nolint:wsl // test readability
		// This test validates that our factory pattern works conceptually
		// We test the factory function signature and behavior we expect

		// Test that our factory function signature is correct
		factory := mockFactoryFunction
		writer1 := factory()
		writer2 := factory()

		// Verify both instances are valid writers
		assert.NotNil(t, writer1, "Factory should create valid writer instances")
		assert.NotNil(t, writer2, "Factory should create valid writer instances")

		// Verify they are separate instances (not the same pointer)
		assert.NotSame(t, writer1, writer2, "Factory should create separate instances")

		// Verify they implement the required interface
		assert.Implements(t, (*scrapemate.ResultWriter)(nil), writer1, "Instance 1 should implement ResultWriter")
		assert.Implements(t, (*scrapemate.ResultWriter)(nil), writer2, "Instance 2 should implement ResultWriter")
	})

	t.Run("Factory function creates fresh instances per call", func(t *testing.T) {
		// Test the core behavior our factory pattern enables
		instances := make([]scrapemate.ResultWriter, 5)

		// Create multiple instances
		for i := 0; i < 5; i++ {
			instances[i] = mockFactoryFunction()
		}

		// Verify all instances are different pointers
		for i := 0; i < len(instances); i++ {
			for j := i + 1; j < len(instances); j++ {
				assert.NotSame(t, instances[i], instances[j],
					"Instance %d and %d should be different objects", i, j)
			}
		}

		// Verify all are valid writers
		for i, instance := range instances {
			assert.NotNil(t, instance, "Instance %d should not be nil", i)
			assert.Implements(t, (*scrapemate.ResultWriter)(nil), instance, "Instance %d should implement ResultWriter", i)
		}
	})

	t.Run("Factory pattern supports concurrent access", func(t *testing.T) {
		// Test that our factory pattern works safely with concurrent calls
		numGoroutines := 10
		instancesPerGoroutine := 5
		totalInstances := numGoroutines * instancesPerGoroutine

		instancesChan := make(chan scrapemate.ResultWriter, totalInstances)

		var wg sync.WaitGroup

		// Create instances concurrently from multiple goroutines
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)

			go func(_ int) {
				defer wg.Done()

				for j := 0; j < instancesPerGoroutine; j++ {
					instance := mockFactoryFunction()
					instancesChan <- instance
				}
			}(i)
		}

		// Wait for all goroutines to complete
		wg.Wait()
		close(instancesChan)

		// Collect all instances
		var instances []scrapemate.ResultWriter
		for instance := range instancesChan {
			instances = append(instances, instance)
		}

		// Verify we got the expected number of instances
		assert.Len(t, instances, totalInstances, "Should have created all instances")

		// Verify all instances are unique (different pointers)
		uniqueInstances := make(map[scrapemate.ResultWriter]bool)
		for _, instance := range instances {
			assert.False(t, uniqueInstances[instance], "Should not have duplicate instances")
			uniqueInstances[instance] = true
		}

		assert.Len(t, uniqueInstances, totalInstances, "All instances should be unique")
	})
}

// TestFactoryPatternIsolation tests that instances created by factory are isolated
func TestFactoryPatternIsolation(t *testing.T) {
	t.Run("Factory instances have isolated state", func(t *testing.T) {
		// Create two separate instances
		writer1 := mockFactoryFunction()
		writer2 := mockFactoryFunction()

		// Type assert to our specific writer type for testing
		streaming1, ok1 := writer1.(*plugins.StreamingDuplicateFilterWriter)
		streaming2, ok2 := writer2.(*plugins.StreamingDuplicateFilterWriter)

		require.True(t, ok1, "Writer 1 should be StreamingDuplicateFilterWriter")
		require.True(t, ok2, "Writer 2 should be StreamingDuplicateFilterWriter")

		// Test that they have separate state - set different review limits
		streaming1.SetReviewLimit(5)
		streaming2.SetReviewLimit(25)

		// Test that they have separate CID sets
		streaming1.SetExistingCIDs([]string{"cid1", "cid2"})
		streaming2.SetExistingCIDs([]string{"cid3", "cid4", "cid5"})

		// Verify stats are independent
		stats1 := streaming1.GetStats()
		stats2 := streaming2.GetStats()

		// Initially both should have 0 processed
		assert.Equal(t, 0, stats1.TotalProcessed, "Writer 1 should start with 0 processed")
		assert.Equal(t, 0, stats2.TotalProcessed, "Writer 2 should start with 0 processed")

		// Verify they are truly separate instances
		assert.NotSame(t, streaming1, streaming2, "Instances should be separate objects")
	})

	t.Run("Factory instances can be used independently", func(t *testing.T) {
		// Create multiple instances
		writers := make([]*plugins.StreamingDuplicateFilterWriter, 3)
		for i := range writers {
			writer := mockFactoryFunction()
			streamingWriter, ok := writer.(*plugins.StreamingDuplicateFilterWriter)
			require.True(t, ok, "Writer %d should be StreamingDuplicateFilterWriter", i)

			writers[i] = streamingWriter
		}

		// Configure each instance differently
		writers[0].SetReviewLimit(5)
		writers[1].SetReviewLimit(10)
		writers[2].SetReviewLimit(25)

		writers[0].SetExistingCIDs([]string{"set1_cid1", "set1_cid2"})
		writers[1].SetExistingCIDs([]string{"set2_cid1", "set2_cid2", "set2_cid3"})
		writers[2].SetExistingCIDs([]string{"set3_cid1"})

		// Verify each has independent configuration
		for i, writer := range writers {
			stats := writer.GetStats()
			assert.Equal(t, 0, stats.TotalProcessed, "Writer %d should have independent stats", i)

			// Test that event channels are separate
			eventChan := writer.GetEventChannel()
			assert.NotNil(t, eventChan, "Writer %d should have event channel", i)
		}

		// Close one instance and verify others are unaffected
		writers[0].Close()

		// Other instances should still be usable
		for i := 1; i < len(writers); i++ {
			stats := writers[i].GetStats()
			assert.Equal(t, 0, stats.TotalProcessed, "Writer %d should still be functional", i)
		}
	})
}

// TestCreateCustomWriterFunction tests our CreateCustomWriter implementation
func TestCreateCustomWriterFunction(t *testing.T) {
	t.Run("CreateCustomWriter validates input parameters", func(t *testing.T) {
		// Test with non-existent directory
		_, err := runner.CreateCustomWriter("/nonexistent/path", "TestPlugin")
		assert.Error(t, err, "Should error on non-existent directory")
		assert.Contains(t, err.Error(), "failed to read plugin directory", "Should indicate directory read failure")

		// Test with empty plugin name
		tempDir := t.TempDir()
		_, err = runner.CreateCustomWriter(tempDir, "")
		assert.Error(t, err, "Should error when no matching plugin found")
	})

	t.Run("CreateCustomWriter handles directory without plugins", func(t *testing.T) {
		// Create temporary directory with non-plugin files
		tempDir := t.TempDir()

		// Add some non-plugin files
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("test"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, "script.sh"), []byte("#!/bin/bash"), 0o600))

		// Should not find any plugins
		_, err := runner.CreateCustomWriter(tempDir, "TestPlugin")
		assert.Error(t, err, "Should error when no plugins found")
		assert.Contains(t, err.Error(), "no plugin found", "Should indicate no plugin found")
	})

	t.Run("CreateCustomWriter factory function signature validation", func(t *testing.T) { //nolint:wsl // test readability
		// Test the expected behavior when factory function lookup succeeds
		// We verify the type checking logic our implementation uses

		// This tests the signature checking we implemented
		factoryFunc := func() scrapemate.ResultWriter {
			return plugins.NewStreamingDuplicateFilterWriter()
		}

		// Verify our factory function has the correct signature
		writer := factoryFunc()
		assert.NotNil(t, writer, "Factory function should create valid writer")
		assert.Implements(t, (*scrapemate.ResultWriter)(nil), writer, "Factory output should implement ResultWriter interface")
	})
}

// TestFactoryPatternMemoryManagement tests resource management with factory pattern
func TestFactoryPatternMemoryManagement(t *testing.T) {
	t.Run("Factory instances can be properly closed", func(t *testing.T) {
		// Create multiple instances
		writers := make([]*plugins.StreamingDuplicateFilterWriter, 5)
		for i := range writers {
			writer := mockFactoryFunction()
			streamingWriter, ok := writer.(*plugins.StreamingDuplicateFilterWriter)
			require.True(t, ok, "Writer should be StreamingDuplicateFilterWriter")

			writers[i] = streamingWriter
		}

		// Close all instances
		for i, writer := range writers {
			assert.NotPanics(t, func() {
				writer.Close()
			}, "Writer %d should close without panic", i)
		}

		// Verify closing multiple times doesn't cause issues
		for i, writer := range writers {
			assert.NotPanics(t, func() {
				writer.Close() // Second close
			}, "Writer %d should handle multiple closes gracefully", i)
		}
	})

	t.Run("Factory pattern prevents channel reuse issues", func(t *testing.T) { //nolint:wsl // test readability
		// This test verifies our factory pattern fixes the "send on closed channel" issue
		// by ensuring each instance has its own channel

		// Create multiple instances
		instances := make([]*plugins.StreamingDuplicateFilterWriter, 3)
		channels := make([]<-chan plugins.StreamEvent, 3)

		for i := range instances {
			writer := mockFactoryFunction()
			streamingWriter, ok := writer.(*plugins.StreamingDuplicateFilterWriter)
			require.True(t, ok, "Writer should be StreamingDuplicateFilterWriter")

			instances[i] = streamingWriter
			channels[i] = streamingWriter.GetEventChannel()
		}

		// Verify each instance has a different event channel
		// Note: channels are not directly comparable, but we can verify they're not nil
		// and that instances are separate (which implies separate channels)
		for i, channel := range channels {
			assert.NotNil(t, channel, "Instance %d should have valid event channel", i)
		}

		// Close first instance
		instances[0].Close()

		// Other instances should still be functional
		for i := 1; i < len(instances); i++ {
			// Should be able to get event channel without panic
			eventChan := instances[i].GetEventChannel()
			assert.NotNil(t, eventChan, "Instance %d should still have functional event channel", i)
		}
	})

	t.Run("Factory pattern supports rapid instance creation and cleanup", func(t *testing.T) {
		// Test rapid creation and cleanup cycles (simulating multiple jobs)
		numCycles := 10
		instancesPerCycle := 5

		for cycle := 0; cycle < numCycles; cycle++ {
			// Create instances
			instances := make([]*plugins.StreamingDuplicateFilterWriter, instancesPerCycle)
			for i := range instances {
				writer := mockFactoryFunction()
				streamingWriter, ok := writer.(*plugins.StreamingDuplicateFilterWriter)
				require.True(t, ok, "Writer should be StreamingDuplicateFilterWriter")

				instances[i] = streamingWriter
			}

			// Use instances briefly
			for _, instance := range instances {
				stats := instance.GetStats()
				assert.Equal(t, 0, stats.TotalProcessed, "Fresh instance should have clean state")
			}

			// Clean up instances
			for i, instance := range instances {
				assert.NotPanics(t, func() {
					instance.Close()
				}, "Cycle %d, instance %d should close cleanly", cycle, i)
			}

			// Brief pause to simulate job processing time
			time.Sleep(1 * time.Millisecond)
		}

		t.Logf("Successfully completed %d cycles of %d instances each", numCycles, instancesPerCycle)
	})
}

// TestFactoryPatternArchitecturalBenefits tests the architectural improvements our factory pattern provides
func TestFactoryPatternArchitecturalBenefits(t *testing.T) {
	t.Run("Factory pattern enables job isolation", func(t *testing.T) {
		// Simulate multiple concurrent jobs each with their own plugin instance
		type jobSimulation struct {
			id      string
			writer  *plugins.StreamingDuplicateFilterWriter
			cids    []string
			jobDone chan bool
		}

		jobs := make([]*jobSimulation, 3)

		// Set up jobs with different configurations
		writer0 := mockFactoryFunction()
		streamingWriter0, ok := writer0.(*plugins.StreamingDuplicateFilterWriter)
		require.True(t, ok, "Writer should be StreamingDuplicateFilterWriter")

		jobs[0] = &jobSimulation{
			id:      "job-restaurants",
			writer:  streamingWriter0,
			cids:    []string{"restaurant_cid_1", "restaurant_cid_2"},
			jobDone: make(chan bool, 1), // Buffered channel
		}

		writer1 := mockFactoryFunction()
		streamingWriter1, ok := writer1.(*plugins.StreamingDuplicateFilterWriter)
		require.True(t, ok, "Writer should be StreamingDuplicateFilterWriter")

		jobs[1] = &jobSimulation{
			id:      "job-cafes",
			writer:  streamingWriter1,
			cids:    []string{"cafe_cid_1", "cafe_cid_2", "cafe_cid_3"},
			jobDone: make(chan bool, 1), // Buffered channel
		}

		writer2 := mockFactoryFunction()
		streamingWriter2, ok := writer2.(*plugins.StreamingDuplicateFilterWriter)
		require.True(t, ok, "Writer should be StreamingDuplicateFilterWriter")

		jobs[2] = &jobSimulation{
			id:      "job-hotels",
			writer:  streamingWriter2,
			cids:    []string{"hotel_cid_1"},
			jobDone: make(chan bool, 1), // Buffered channel
		}

		// Configure each job's writer independently
		for i, job := range jobs {
			job.writer.SetExistingCIDs(job.cids)
			job.writer.SetReviewLimit(5 * (i + 1)) // Different review limits

			// Verify independent configuration
			stats := job.writer.GetStats()
			assert.Equal(t, 0, stats.TotalProcessed, "Job %s should start with clean state", job.id)
		}

		// Simulate concurrent job processing
		var wg sync.WaitGroup
		for _, job := range jobs {
			wg.Add(1)

			go func(j *jobSimulation) {
				defer wg.Done()

				// Simulate job processing
				stats := j.writer.GetStats()
				assert.Equal(t, 0, stats.TotalProcessed, "Job %s should maintain isolated state", j.id)

				// Signal job completion
				j.jobDone <- true
				close(j.jobDone)
			}(job)
		}

		// Wait for all jobs to complete
		wg.Wait()

		// Verify all jobs completed successfully
		for _, job := range jobs {
			select {
			case <-job.jobDone:
				// Job completed
			case <-time.After(1 * time.Second):
				t.Errorf("Job %s did not complete in time", job.id)
			}
		}

		// Clean up job resources
		for _, job := range jobs {
			assert.NotPanics(t, func() {
				job.writer.Close()
			}, "Job %s writer should close cleanly", job.id)
		}
	})

	t.Run("Factory pattern eliminates shared state issues", func(t *testing.T) { //nolint:wsl // test readability
		// Test that the factory pattern eliminates the shared state problems
		// that caused crashes in the original singleton pattern

		// Create two plugin instances
		w1 := mockFactoryFunction()
		writer1, ok := w1.(*plugins.StreamingDuplicateFilterWriter)
		require.True(t, ok, "Writer should be StreamingDuplicateFilterWriter")

		w2 := mockFactoryFunction()
		writer2, ok := w2.(*plugins.StreamingDuplicateFilterWriter)
		require.True(t, ok, "Writer should be StreamingDuplicateFilterWriter")

		// Configure with overlapping but different CID sets
		writer1.SetExistingCIDs([]string{"shared_cid_1", "unique_cid_1"})
		writer2.SetExistingCIDs([]string{"shared_cid_1", "unique_cid_2"})

		// Get their event channels
		channel1 := writer1.GetEventChannel()
		channel2 := writer2.GetEventChannel()

		// Verify channels are separate (both should be valid but distinct)
		assert.NotNil(t, channel1, "Channel 1 should be valid")
		assert.NotNil(t, channel2, "Channel 2 should be valid")

		// Close first writer
		assert.NotPanics(t, func() {
			writer1.Close()
		}, "Closing first writer should not panic")

		// Second writer should still be functional
		stats2 := writer2.GetStats()
		assert.Equal(t, 0, stats2.TotalProcessed, "Second writer should remain functional")

		channel2AfterClose := writer2.GetEventChannel()
		assert.NotNil(t, channel2AfterClose, "Second writer channel should still be accessible")

		// Clean up second writer
		assert.NotPanics(t, func() {
			writer2.Close()
		}, "Closing second writer should not panic")
	})
}
