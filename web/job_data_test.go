//go:build !plugin
// +build !plugin

package web_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobDataExistingCIDsField tests the ExistingCIDs field we added
func TestJobDataExistingCIDsField(t *testing.T) {
	t.Run("ExistingCIDs field accepts valid CID values", func(t *testing.T) {
		// Test various valid CID formats
		testCases := []struct {
			name string
			cids []string
		}{
			{
				name: "Empty CIDs list",
				cids: []string{},
			},
			{
				name: "Single CID",
				cids: []string{"1651958294010292922"},
			},
			{
				name: "Multiple CIDs",
				cids: []string{"1651958294010292922", "1234567890123456789", "9876543210987654321"},
			},
			{
				name: "Large CID list",
				cids: []string{
					"1651958294010292922", "1234567890123456789", "9876543210987654321",
					"1111111111111111111", "2222222222222222222", "3333333333333333333",
					"4444444444444444444", "5555555555555555555", "6666666666666666666",
					"7777777777777777777",
				},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				jobData := web.JobData{
					Keywords:     []string{"test restaurant"},
					Lang:         "en",
					Depth:        1,
					MaxTime:      5 * time.Minute,
					ExistingCIDs: tc.cids,
					ReviewLimit:  10,
				}

				// Verify validation passes
				err := jobData.Validate()
				assert.NoError(t, err, "JobData with %d CIDs should validate", len(tc.cids))

				// Verify CIDs are stored correctly
				assert.Equal(t, len(tc.cids), len(jobData.ExistingCIDs), "CID count should match")
				assert.Equal(t, tc.cids, jobData.ExistingCIDs, "CIDs should match exactly")
			})
		}
	})

	t.Run("ExistingCIDs field is optional (omitempty)", func(t *testing.T) {
		// Test JobData without ExistingCIDs field
		jobData := web.JobData{
			Keywords: []string{"test"},
			Lang:     "en",
			Depth:    1,
			MaxTime:  5 * time.Minute,
			// ExistingCIDs omitted intentionally
		}

		// Should validate without ExistingCIDs
		err := jobData.Validate()
		assert.NoError(t, err, "JobData should validate without ExistingCIDs")
		assert.Len(t, jobData.ExistingCIDs, 0, "ExistingCIDs should be empty slice")
	})

	t.Run("ExistingCIDs field handles nil vs empty slice", func(t *testing.T) {
		// Test difference between nil and empty slice
		jobDataNil := web.JobData{
			Keywords:     []string{"test"},
			Lang:         "en",
			Depth:        1,
			MaxTime:      5 * time.Minute,
			ExistingCIDs: nil, // Explicitly nil
		}

		jobDataEmpty := web.JobData{
			Keywords:     []string{"test"},
			Lang:         "en",
			Depth:        1,
			MaxTime:      5 * time.Minute,
			ExistingCIDs: []string{}, // Explicitly empty
		}

		// Both should validate
		assert.NoError(t, jobDataNil.Validate(), "JobData with nil ExistingCIDs should validate")
		assert.NoError(t, jobDataEmpty.Validate(), "JobData with empty ExistingCIDs should validate")

		// Both should have length 0
		assert.Len(t, jobDataNil.ExistingCIDs, 0, "Nil ExistingCIDs should have length 0")
		assert.Len(t, jobDataEmpty.ExistingCIDs, 0, "Empty ExistingCIDs should have length 0")
	})

	t.Run("ExistingCIDs field JSON serialization/deserialization", func(t *testing.T) {
		// Test JSON marshaling and unmarshaling
		originalJobData := web.JobData{
			Keywords:     []string{"restaurant", "cafe"},
			Lang:         "en",
			Depth:        1,
			MaxTime:      5 * time.Minute,
			ExistingCIDs: []string{"1651958294010292922", "1234567890123456789"},
			ReviewLimit:  15,
		}

		// Marshal to JSON
		jsonBytes, err := json.Marshal(originalJobData)
		require.NoError(t, err, "Should marshal JobData to JSON")

		// Verify JSON contains our fields
		jsonStr := string(jsonBytes)
		assert.Contains(t, jsonStr, "existing_cids", "JSON should contain existing_cids field")
		assert.Contains(t, jsonStr, "review_limit", "JSON should contain review_limit field")
		assert.Contains(t, jsonStr, "1651958294010292922", "JSON should contain first CID")
		assert.Contains(t, jsonStr, "1234567890123456789", "JSON should contain second CID")

		// Unmarshal from JSON
		var unmarshaledJobData web.JobData
		err = json.Unmarshal(jsonBytes, &unmarshaledJobData)
		require.NoError(t, err, "Should unmarshal JobData from JSON")

		// Verify fields match
		assert.Equal(t, originalJobData.Keywords, unmarshaledJobData.Keywords, "Keywords should match")
		assert.Equal(t, originalJobData.ExistingCIDs, unmarshaledJobData.ExistingCIDs, "ExistingCIDs should match")
		assert.Equal(t, originalJobData.ReviewLimit, unmarshaledJobData.ReviewLimit, "ReviewLimit should match")
	})

	t.Run("ExistingCIDs field omitempty behavior", func(t *testing.T) {
		// Test omitempty behavior for ExistingCIDs
		jobDataWithoutCIDs := web.JobData{
			Keywords: []string{"test"},
			Lang:     "en",
			Depth:    1,
			MaxTime:  5 * time.Minute,
			// ExistingCIDs not set (will be nil/empty)
		}

		jobDataWithEmptyCIDs := web.JobData{
			Keywords:     []string{"test"},
			Lang:         "en",
			Depth:        1,
			MaxTime:      5 * time.Minute,
			ExistingCIDs: []string{}, // Explicitly empty
		}

		// Marshal both to JSON
		jsonWithoutCIDs, err := json.Marshal(jobDataWithoutCIDs)
		require.NoError(t, err)

		jsonWithEmptyCIDs, err := json.Marshal(jobDataWithEmptyCIDs)
		require.NoError(t, err)

		// Both should omit the existing_cids field due to omitempty
		jsonStrWithout := string(jsonWithoutCIDs)
		jsonStrWithEmpty := string(jsonWithEmptyCIDs)

		// The field should be omitted when empty due to omitempty tag
		assert.NotContains(t, jsonStrWithout, "existing_cids", "Should omit existing_cids when nil")
		assert.NotContains(t, jsonStrWithEmpty, "existing_cids", "Should omit existing_cids when empty")
	})
}

// TestJobDataReviewLimitField tests the ReviewLimit field we added
func TestJobDataReviewLimitField(t *testing.T) {
	t.Run("ReviewLimit field accepts valid values", func(t *testing.T) {
		// Test various valid review limit values
		testCases := []struct {
			name  string
			limit int
		}{
			{"Zero reviews", 0},
			{"Minimum reviews", 1},
			{"Standard limit", 5},
			{"Medium limit", 10},
			{"High limit", 25},
			{"Maximum limit", 100},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				jobData := web.JobData{
					Keywords:    []string{"test restaurant"},
					Lang:        "en",
					Depth:       1,
					MaxTime:     5 * time.Minute,
					ReviewLimit: tc.limit,
				}

				// Verify validation passes
				err := jobData.Validate()
				assert.NoError(t, err, "JobData with ReviewLimit %d should validate", tc.limit)

				// Verify limit is stored correctly
				assert.Equal(t, tc.limit, jobData.ReviewLimit, "ReviewLimit should match")
			})
		}
	})

	t.Run("ReviewLimit field defaults to zero", func(t *testing.T) {
		// Test default value behavior
		jobData := web.JobData{
			Keywords: []string{"test"},
			Lang:     "en",
			Depth:    1,
			MaxTime:  5 * time.Minute,
			// ReviewLimit not set - should default to 0
		}

		err := jobData.Validate()
		assert.NoError(t, err, "JobData should validate with default ReviewLimit")
		assert.Equal(t, 0, jobData.ReviewLimit, "ReviewLimit should default to 0")
	})

	t.Run("ReviewLimit field JSON serialization", func(t *testing.T) {
		// Test JSON handling for ReviewLimit
		testCases := []struct {
			name  string
			limit int
		}{
			{"Zero limit", 0},
			{"Positive limit", 25},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				jobData := web.JobData{
					Keywords:    []string{"test"},
					Lang:        "en",
					Depth:       1,
					MaxTime:     5 * time.Minute,
					ReviewLimit: tc.limit,
				}

				// Marshal to JSON
				jsonBytes, err := json.Marshal(jobData)
				require.NoError(t, err, "Should marshal JobData with ReviewLimit")

				// Verify JSON contains ReviewLimit
				jsonStr := string(jsonBytes)
				assert.Contains(t, jsonStr, "review_limit", "JSON should contain review_limit field")

				// Unmarshal from JSON
				var unmarshaledJobData web.JobData
				err = json.Unmarshal(jsonBytes, &unmarshaledJobData)
				require.NoError(t, err, "Should unmarshal JobData from JSON")

				// Verify ReviewLimit matches
				assert.Equal(t, tc.limit, unmarshaledJobData.ReviewLimit, "ReviewLimit should match after JSON round-trip")
			})
		}
	})
}

// TestJobDataBackwardsCompatibility tests backwards compatibility of our additions
func TestJobDataBackwardsCompatibility(t *testing.T) {
	t.Run("Legacy JobData without new fields still validates", func(t *testing.T) {
		// Test that old JobData structures without our new fields still work
		legacyJobData := web.JobData{
			Keywords: []string{"restaurant"},
			Lang:     "en",
			Depth:    1,
			MaxTime:  5 * time.Minute,
			// No ExistingCIDs or ReviewLimit fields
		}

		err := legacyJobData.Validate()
		assert.NoError(t, err, "Legacy JobData should still validate")

		// Verify default values
		assert.Len(t, legacyJobData.ExistingCIDs, 0, "ExistingCIDs should default to empty")
		assert.Equal(t, 0, legacyJobData.ReviewLimit, "ReviewLimit should default to 0")
	})

	t.Run("Legacy JSON without new fields can be unmarshaled", func(t *testing.T) {
		// Test unmarshaling old JSON that doesn't have our new fields
		legacyJSON := `{
			"keywords": ["restaurant", "cafe"],
			"lang": "en",
			"depth": 1,
			"max_time": 300000000000,
			"proxies": []
		}`

		var jobData web.JobData
		err := json.Unmarshal([]byte(legacyJSON), &jobData)
		require.NoError(t, err, "Should unmarshal legacy JSON")

		// Verify it validates
		err = jobData.Validate()
		assert.NoError(t, err, "Unmarshaled legacy JobData should validate")

		// Verify our new fields have default values
		assert.Len(t, jobData.ExistingCIDs, 0, "ExistingCIDs should be empty for legacy JSON")
		assert.Equal(t, 0, jobData.ReviewLimit, "ReviewLimit should be 0 for legacy JSON")

		// Verify existing fields are correct
		assert.Equal(t, []string{"restaurant", "cafe"}, jobData.Keywords, "Keywords should be correct")
		assert.Equal(t, "en", jobData.Lang, "Lang should be correct")
		assert.Equal(t, 1, jobData.Depth, "Depth should be correct")
	})

	t.Run("New fields can be added to existing JobData", func(t *testing.T) {
		// Test that we can add our new fields to existing JobData
		existingJobData := web.JobData{
			Keywords: []string{"hotel"},
			Lang:     "fr",
			Depth:    2,
			MaxTime:  10 * time.Minute,
		}

		// Add our new fields
		existingJobData.ExistingCIDs = []string{"1651958294010292922", "1234567890123456789"}
		existingJobData.ReviewLimit = 15

		// Should validate with new fields added
		err := existingJobData.Validate()
		assert.NoError(t, err, "JobData with added new fields should validate")

		// Verify all fields are correct
		assert.Equal(t, []string{"hotel"}, existingJobData.Keywords, "Original fields should be preserved")
		assert.Equal(t, "fr", existingJobData.Lang, "Original lang should be preserved")
		assert.Len(t, existingJobData.ExistingCIDs, 2, "Should have 2 ExistingCIDs")
		assert.Equal(t, []string{"1651958294010292922", "1234567890123456789"}, existingJobData.ExistingCIDs, "New ExistingCIDs should be set")
		assert.Equal(t, 15, existingJobData.ReviewLimit, "New ReviewLimit should be set")
	})
}

// TestJobDataAPIParameterPassing tests parameter passing to plugin
func TestJobDataAPIParameterPassing(t *testing.T) {
	t.Run("JobData with new fields can be converted to API requests", func(t *testing.T) {
		// Test that our new fields work in the context of API requests
		jobData := web.JobData{
			Keywords:     []string{"test restaurant", "cafe"},
			Lang:         "en",
			Depth:        1,
			MaxTime:      5 * time.Minute,
			ExistingCIDs: []string{"1651958294010292922", "1234567890123456789", "9876543210987654321"},
			ReviewLimit:  25,
		}

		// Create a Job with this JobData
		job := web.Job{
			ID:     "test-job-123",
			Name:   "API Parameter Test",
			Date:   time.Now(),
			Status: web.StatusPending,
			Data:   jobData,
		}

		// Verify job validates
		err := job.Validate()
		assert.NoError(t, err, "Job with new JobData fields should validate")

		// Verify our fields are accessible via job.Data
		assert.Len(t, job.Data.ExistingCIDs, 3, "Should have 3 existing CIDs")
		assert.Equal(t, 25, job.Data.ReviewLimit, "Should have review limit of 25")

		// Verify the data can be used for plugin configuration
		if len(job.Data.ExistingCIDs) > 0 {
			// This simulates how webrunner.go uses the ExistingCIDs
			assert.NotEmpty(t, job.Data.ExistingCIDs[0], "First CID should not be empty")
			assert.Equal(t, "1651958294010292922", job.Data.ExistingCIDs[0], "First CID should match")
		}

		if job.Data.ReviewLimit > 0 {
			// This simulates how the plugin uses ReviewLimit
			assert.Greater(t, job.Data.ReviewLimit, 0, "Review limit should be positive")
			assert.LessOrEqual(t, job.Data.ReviewLimit, 100, "Review limit should be reasonable")
		}
	})

	t.Run("JobData fields are preserved through Job lifecycle", func(t *testing.T) {
		// Test that our fields persist through job creation, storage, and retrieval
		originalJobData := web.JobData{
			Keywords:     []string{"lifecycle test"},
			Lang:         "en",
			Depth:        1,
			MaxTime:      5 * time.Minute,
			ExistingCIDs: []string{"1111111111111111111", "2222222222222222222"},
			ReviewLimit:  10,
		}

		// Create job
		job := web.Job{
			ID:     "lifecycle-test-456",
			Name:   "Lifecycle Test",
			Date:   time.Now(),
			Status: web.StatusPending,
			Data:   originalJobData,
		}

		// Simulate JSON round-trip (as would happen in storage/retrieval)
		jsonBytes, err := json.Marshal(job)
		require.NoError(t, err, "Should marshal job to JSON")

		var retrievedJob web.Job
		err = json.Unmarshal(jsonBytes, &retrievedJob)
		require.NoError(t, err, "Should unmarshal job from JSON")

		// Verify our fields are preserved
		assert.Equal(t, originalJobData.ExistingCIDs, retrievedJob.Data.ExistingCIDs, "ExistingCIDs should be preserved")
		assert.Equal(t, originalJobData.ReviewLimit, retrievedJob.Data.ReviewLimit, "ReviewLimit should be preserved")

		// Verify job still validates after round-trip
		err = retrievedJob.Validate()
		assert.NoError(t, err, "Retrieved job should still validate")
	})

	t.Run("JobData with edge case values handles correctly", func(t *testing.T) {
		// Test edge cases for our new fields
		testCases := []struct {
			name         string
			existingCIDs []string
			reviewLimit  int
			shouldPass   bool
		}{
			{
				name:         "Empty CIDs, zero limit",
				existingCIDs: []string{},
				reviewLimit:  0,
				shouldPass:   true,
			},
			{
				name:         "Nil CIDs, positive limit",
				existingCIDs: nil,
				reviewLimit:  5,
				shouldPass:   true,
			},
			{
				name:         "Many CIDs, high limit",
				existingCIDs: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"},
				reviewLimit:  100,
				shouldPass:   true,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				jobData := web.JobData{
					Keywords:     []string{"edge case test"},
					Lang:         "en",
					Depth:        1,
					MaxTime:      5 * time.Minute,
					ExistingCIDs: tc.existingCIDs,
					ReviewLimit:  tc.reviewLimit,
				}

				err := jobData.Validate()
				if tc.shouldPass {
					assert.NoError(t, err, "Edge case should pass validation")
				} else {
					assert.Error(t, err, "Edge case should fail validation")
				}
			})
		}
	})
}
