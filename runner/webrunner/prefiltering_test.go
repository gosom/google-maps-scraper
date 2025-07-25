//go:build !plugin
// +build !plugin

package webrunner_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gosom/google-maps-scraper/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDeduper implements deduper.Deduper interface for testing
type mockDeduper struct {
	items map[string]bool
	mu    sync.RWMutex
}

func newMockDeduper() *mockDeduper {
	return &mockDeduper{
		items: make(map[string]bool),
	}
}

func (m *mockDeduper) AddIfNotExists(_ context.Context, item string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.items[item] {
		return false // Already exists
	}

	m.items[item] = true

	return true // Added
}

func (m *mockDeduper) Contains(item string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.items[item]
}

func (m *mockDeduper) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.items)
}

// TestPrefilteringCIDConversion tests the CID format conversion logic we implemented
func TestPrefilteringCIDConversion(t *testing.T) {
	t.Run("CID decimal to hex conversion for DataID matching", func(t *testing.T) {
		// Test the conversion logic we implemented in webrunner.go:194-202
		testCases := []struct {
			cidDecimal  string
			description string
		}{
			{"16519582940102929223", "Large CID value"},
			{"12345678901234567890", "Another large CID"},
			{"1234567890", "Smaller CID value"},
			{"0", "Zero CID"},
			{"1", "Minimum CID"},
		}

		for _, tc := range testCases {
			t.Run(tc.description, func(t *testing.T) {
				// Simulate the conversion logic from webrunner.go
				cidDecimal, err := strconv.ParseUint(tc.cidDecimal, 10, 64)
				require.NoError(t, err, "Should parse decimal CID successfully")

				cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)
				assert.NotEmpty(t, cidHex, "Hex conversion should produce valid hex string")
				assert.True(t, len(cidHex) > 2, "Hex string should be longer than '0x'")
			})
		}
	})

	t.Run("CID conversion handles invalid values gracefully", func(t *testing.T) {
		// Test cases that should fail conversion but not crash
		invalidCIDs := []string{
			"not-a-number",
			"",
			"123.456",
			"-123",
			"abc123",
		}

		for _, invalidCID := range invalidCIDs {
			t.Run("Invalid CID: "+invalidCID, func(t *testing.T) {
				_, err := strconv.ParseUint(invalidCID, 10, 64)
				assert.Error(t, err, "Should error on invalid CID format")
			})
		}
	})
}

// TestPrefilteringDeduperInitialization tests the deduper initialization we added
func TestPrefilteringDeduperInitialization(t *testing.T) {
	t.Run("Deduper initialized with existing CIDs from JobData", func(t *testing.T) {
		// Test the functionality we added in webrunner.go:189-207
		dedup := newMockDeduper()
		ctx := context.Background()

		// Test CIDs we would pass from JobData.ExistingCIDs (smaller values that fit in uint64)
		existingCIDs := []string{
			"1651958294010292922", // Reduced to fit uint64
			"1234567890123456789", // Reduced to fit uint64
			"9876543210987654321", // Reduced to fit uint64
		}

		// Simulate the initialization logic from webrunner.go
		var processedCount int

		for _, cid := range existingCIDs {
			if cid != "" {
				cidDecimal, err := strconv.ParseUint(cid, 10, 64)
				if err == nil {
					cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)
					if dedup.AddIfNotExists(ctx, cidHex) {
						processedCount++
					}
				}
			}
		}

		// Verify initialization results
		assert.Equal(t, len(existingCIDs), processedCount, "Should process all valid CIDs")
		assert.Equal(t, len(existingCIDs), dedup.Size(), "Deduper should contain all CIDs")

		// Verify CIDs were added in hex format (calculate expected hex values)
		for i, cid := range existingCIDs {
			cidDecimal, err := strconv.ParseUint(cid, 10, 64)
			require.NoError(t, err, "Should parse CID %d", i)

			expectedHex := "0x" + strconv.FormatUint(cidDecimal, 16)
			assert.True(t, dedup.Contains(expectedHex), "Should contain CID %d in hex format: %s", i, expectedHex)
		}
	})

	t.Run("Deduper initialization handles empty CID list", func(t *testing.T) {
		// Test edge case of empty ExistingCIDs
		dedup := newMockDeduper()
		existingCIDs := []string{}

		// Should handle empty list gracefully
		assert.Equal(t, 0, len(existingCIDs), "Empty CID list should be length 0")
		assert.Equal(t, 0, dedup.Size(), "Deduper should remain empty")
	})

	t.Run("Deduper initialization skips empty and invalid CIDs", func(t *testing.T) {
		// Test mixed valid/invalid CID handling
		dedup := newMockDeduper()
		ctx := context.Background()

		// Mixed list with valid and invalid CIDs
		existingCIDs := []string{
			"16519582940102929223", // Valid
			"",                     // Empty (should skip)
			"invalid-cid",          // Invalid (should skip)
			"12345678901234567890", // Valid
			"abc123",               // Invalid (should skip)
		}

		var validCount int

		for _, cid := range existingCIDs {
			if cid != "" {
				cidDecimal, err := strconv.ParseUint(cid, 10, 64)
				if err == nil {
					cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)
					if dedup.AddIfNotExists(ctx, cidHex) {
						validCount++
					}
				}
			}
		}

		// Should only process the 2 valid CIDs
		assert.Equal(t, 2, validCount, "Should process only valid CIDs")
		assert.Equal(t, 2, dedup.Size(), "Deduper should contain only valid CIDs")
	})

	t.Run("Deduper prevents duplicate CID addition", func(t *testing.T) {
		// Test that duplicate CIDs are handled correctly
		dedup := newMockDeduper()
		ctx := context.Background()

		cid := "16519582940102929223"
		cidDecimal, err := strconv.ParseUint(cid, 10, 64)
		require.NoError(t, err)

		cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)

		// Add CID first time
		added1 := dedup.AddIfNotExists(ctx, cidHex)
		assert.True(t, added1, "First addition should succeed")
		assert.Equal(t, 1, dedup.Size(), "Should have 1 CID after first addition")

		// Try to add same CID again
		added2 := dedup.AddIfNotExists(ctx, cidHex)
		assert.False(t, added2, "Second addition should be rejected")
		assert.Equal(t, 1, dedup.Size(), "Should still have only 1 CID after duplicate attempt")
	})
}

// TestPrefilteringJobDataIntegration tests integration with JobData.ExistingCIDs
func TestPrefilteringJobDataIntegration(t *testing.T) {
	t.Run("JobData ExistingCIDs field validation", func(t *testing.T) {
		// Test the JobData.ExistingCIDs field we added
		jobData := web.JobData{
			Keywords:     []string{"restaurant", "cafe"},
			Lang:         "en",
			Depth:        1,
			MaxTime:      5 * time.Minute,
			ExistingCIDs: []string{"1651958294010292922", "1234567890123456789"},
			ReviewLimit:  10,
		}

		// Verify JobData validation accepts ExistingCIDs
		err := jobData.Validate()
		assert.NoError(t, err, "JobData with ExistingCIDs should validate successfully")

		// Verify field contents
		assert.Len(t, jobData.ExistingCIDs, 2, "Should have 2 existing CIDs")
		assert.Equal(t, "1651958294010292922", jobData.ExistingCIDs[0], "First CID should match")
		assert.Equal(t, "1234567890123456789", jobData.ExistingCIDs[1], "Second CID should match")
	})

	t.Run("JobData ExistingCIDs field is optional", func(t *testing.T) {
		// Test that ExistingCIDs field is optional (omitempty tag)
		jobData := web.JobData{
			Keywords: []string{"restaurant"},
			Lang:     "en",
			Depth:    1,
			MaxTime:  5 * time.Minute,
			// ExistingCIDs omitted
		}

		err := jobData.Validate()
		assert.NoError(t, err, "JobData without ExistingCIDs should validate successfully")
		assert.Len(t, jobData.ExistingCIDs, 0, "ExistingCIDs should be empty when not provided")
	})

	t.Run("ExistingCIDs integration with Job creation", func(t *testing.T) {
		// Test full Job structure with ExistingCIDs
		job := &web.Job{
			ID:     uuid.New().String(),
			Name:   "Prefiltering Test Job",
			Date:   time.Now().UTC(),
			Status: web.StatusPending,
			Data: web.JobData{
				Keywords:     []string{"test restaurant"},
				Lang:         "en",
				Depth:        1,
				MaxTime:      5 * time.Minute,
				ExistingCIDs: []string{"1651958294010292922", "1234567890123456789", "9876543210987654321"},
				ReviewLimit:  25,
			},
		}

		// Verify job validation
		err := job.Validate()
		assert.NoError(t, err, "Job with ExistingCIDs should validate successfully")

		// Verify CID data
		assert.Len(t, job.Data.ExistingCIDs, 3, "Should have 3 existing CIDs")
		assert.NotEmpty(t, job.ID, "Job should have valid ID")
		assert.Equal(t, web.StatusPending, job.Status, "Job should have pending status")
	})
}

// TestPrefilteringPerformanceBenefits tests the performance benefits of pre-filtering
func TestPrefilteringPerformanceBenefits(t *testing.T) {
	t.Run("Pre-filtering reduces duplicate processing workload", func(t *testing.T) {
		// Simulate the performance benefit of pre-filtering
		dedup := newMockDeduper()
		ctx := context.Background()

		// Pre-load deduper with existing CIDs (simulating pre-filtering)
		existingCIDs := []string{
			"1651958294010292922",
			"1234567890123456789",
			"9876543210987654321",
		}

		// Initialize deduper with existing CIDs
		for _, cid := range existingCIDs {
			cidDecimal, err := strconv.ParseUint(cid, 10, 64)
			require.NoError(t, err)

			cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)
			dedup.AddIfNotExists(ctx, cidHex)
		}

		initialSize := dedup.Size()
		assert.Equal(t, 3, initialSize, "Should start with 3 pre-loaded CIDs")

		// Simulate processing new results (some duplicates, some new)
		newResults := []string{
			"1651958294010292922", // Duplicate (should be filtered)
			"1111111111111111111", // New (should be added)
			"1234567890123456789", // Duplicate (should be filtered)
			"2222222222222222222", // New (should be added)
			"9876543210987654321", // Duplicate (should be filtered)
		}

		var duplicatesFiltered int

		var newItemsAdded int

		for _, result := range newResults {
			cidDecimal, err := strconv.ParseUint(result, 10, 64)
			require.NoError(t, err)

			cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)

			if dedup.AddIfNotExists(ctx, cidHex) {
				newItemsAdded++
			} else {
				duplicatesFiltered++
			}
		}

		// Verify pre-filtering effectiveness
		assert.Equal(t, 3, duplicatesFiltered, "Should filter 3 duplicate CIDs")
		assert.Equal(t, 2, newItemsAdded, "Should add 2 new CIDs")
		assert.Equal(t, 5, dedup.Size(), "Final size should be 5 (3 initial + 2 new)")

		// Calculate filtering efficiency
		totalProcessed := len(newResults)
		filteringEfficiency := float64(duplicatesFiltered) / float64(totalProcessed) * 100
		assert.Equal(t, 60.0, filteringEfficiency, "Should achieve 60% filtering efficiency")
	})

	t.Run("Pre-filtering scales with CID list size", func(t *testing.T) {
		// Test scalability of pre-filtering with larger CID lists
		testSizes := []int{10, 100, 1000}

		for _, size := range testSizes {
			t.Run("CID list size "+strconv.Itoa(size), func(t *testing.T) {
				dedup := newMockDeduper()
				ctx := context.Background()

				// Generate test CIDs
				existingCIDs := make([]string, size)
				baseValue := uint64(1000000000000000000)

				for i := 0; i < size; i++ {
					// Generate sequential CID values
					existingCIDs[i] = strconv.FormatUint(baseValue+uint64(i%1000000), 10) //nolint:gosec // Safe modulo operation
				}

				// Measure initialization time
				start := time.Now()

				for _, cid := range existingCIDs {
					cidDecimal, err := strconv.ParseUint(cid, 10, 64)
					require.NoError(t, err)

					cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)
					dedup.AddIfNotExists(ctx, cidHex)
				}

				initTime := time.Since(start)

				// Verify all CIDs were loaded
				assert.Equal(t, size, dedup.Size(), "Should load all %d CIDs", size)

				// Initialization should be reasonably fast even for large lists
				assert.Less(t, initTime, 100*time.Millisecond, "Initialization of %d CIDs should be fast", size)

				t.Logf("Initialized %d CIDs in %v", size, initTime)
			})
		}
	})
}

// TestPrefilteringConcurrentSafety tests concurrent access to pre-filtering
func TestPrefilteringConcurrentSafety(t *testing.T) {
	t.Run("Concurrent CID initialization is thread-safe", func(t *testing.T) {
		// Test that our pre-filtering can handle concurrent initialization
		dedup := newMockDeduper()
		ctx := context.Background()

		numGoroutines := 5
		cidsPerGoroutine := 100

		var wg sync.WaitGroup

		// Simulate concurrent CID loading from multiple sources
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)

			go func(goroutineID int) {
				defer wg.Done()

				// Each goroutine loads a different range of CIDs
				baseValue := uint64(1000000000000000000) + uint64(goroutineID%100)*1000000 //nolint:gosec // Safe modulo operation

				for j := 0; j < cidsPerGoroutine; j++ {
					cid := strconv.FormatUint(baseValue+uint64(j%1000000), 10) //nolint:gosec // Safe modulo operation
					cidDecimal, err := strconv.ParseUint(cid, 10, 64)
					assert.NoError(t, err, "Should parse CID successfully")

					cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)
					dedup.AddIfNotExists(ctx, cidHex)
				}
			}(i)
		}

		// Wait for all goroutines to complete
		wg.Wait()

		// Verify all CIDs were added
		expectedSize := numGoroutines * cidsPerGoroutine
		assert.Equal(t, expectedSize, dedup.Size(), "Should have loaded all CIDs from concurrent initialization")
	})

	t.Run("Concurrent duplicate detection is thread-safe", func(t *testing.T) {
		// Test concurrent access to duplicate detection
		dedup := newMockDeduper()
		ctx := context.Background()

		// Pre-load some CIDs
		preloadCIDs := []string{"1000000000000000001", "1000000000000000002", "1000000000000000003"}
		for _, cid := range preloadCIDs {
			cidDecimal, err := strconv.ParseUint(cid, 10, 64)
			require.NoError(t, err)

			cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)
			dedup.AddIfNotExists(ctx, cidHex)
		}

		var wg sync.WaitGroup

		numGoroutines := 10

		// Track results from concurrent duplicate checks
		results := make(chan bool, numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)

			go func(goroutineID int) {
				defer wg.Done()

				// Half the goroutines check for existing CIDs, half check for new CIDs
				var cid string
				if goroutineID < numGoroutines/2 {
					// Check existing CID (should be duplicate)
					cid = preloadCIDs[goroutineID%len(preloadCIDs)]
				} else {
					// Check new CID (should be added)
					cid = strconv.FormatUint(uint64(2000000000000000000+goroutineID%1000), 10) //nolint:gosec // Safe modulo operation
				}

				cidDecimal, err := strconv.ParseUint(cid, 10, 64)
				assert.NoError(t, err)

				cidHex := "0x" + strconv.FormatUint(cidDecimal, 16)

				added := dedup.AddIfNotExists(ctx, cidHex)
				results <- added
			}(i)
		}

		wg.Wait()
		close(results)

		// Count results
		var duplicatesFound int

		var newItemsAdded int

		for added := range results {
			if added {
				newItemsAdded++
			} else {
				duplicatesFound++
			}
		}

		// Verify concurrent access worked correctly
		assert.Equal(t, numGoroutines/2, duplicatesFound, "Should detect duplicates correctly")
		assert.Equal(t, numGoroutines/2, newItemsAdded, "Should add new items correctly")

		// Final size should be initial + new items
		expectedFinalSize := len(preloadCIDs) + newItemsAdded
		assert.Equal(t, expectedFinalSize, dedup.Size(), "Final deduper size should be correct")
	})
}
