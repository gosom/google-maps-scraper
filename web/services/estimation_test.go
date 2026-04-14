package services

import (
	"context"
	"math"
	"testing"
)

// intPtr is a helper for creating *int values in test tables.
func intPtr(v int) *int { return &v }

func TestEstimateJobCost(t *testing.T) {
	t.Parallel()

	// Use a nil-DB service — EstimateJobCost only needs pricing, which
	// falls back to hardcoded defaults when DB is nil.
	svc := NewEstimationService(nil, nil)
	ctx := context.Background()

	cases := []struct {
		name       string
		keywords   []string
		depth      int
		maxResults *int
		email      bool
		reviewsMax int
		imagesMax  int
		// Assertions on place estimates
		wantPrimary int
		wantMin     int
		wantMax     int
		// Assertion: total cost should be approximately this (±0.01)
		wantCostApprox float64
	}{
		{
			name:           "bug scenario: 1 keyword depth 5 no cap",
			keywords:       []string{"Cafe Mitte Berlin"},
			depth:          5,
			maxResults:     nil,
			wantPrimary:    40, // 11.17 * 5^0.7925 ≈ 40
			wantMin:        20,
			wantMax:        48,
			wantCostApprox: 0.127, // 0.007 + 40*0.003
		},
		{
			name:           "3 keywords depth 5 no cap",
			keywords:       []string{"Cafe", "Restaurant", "Bar"},
			depth:          5,
			maxResults:     nil,
			wantPrimary:    120, // 3 * 40
			wantMin:        60,
			wantMax:        144,
			wantCostApprox: 0.367, // 0.007 + 120*0.003
		},
		{
			name:           "1 keyword depth 5 user cap 30",
			keywords:       []string{"Cafe"},
			depth:          5,
			maxResults:     intPtr(30),
			wantPrimary:    30,
			wantMin:        15,
			wantMax:        36,
			wantCostApprox: 0.097, // 0.007 + 30*0.003
		},
		{
			name:           "5 keywords depth 20 hits cap 500",
			keywords:       []string{"A", "B", "C", "D", "E"},
			depth:          20,
			maxResults:     nil,
			wantPrimary:    500, // 5 * ~120 = ~600, capped at 500
			wantMin:        250,
			wantMax:        500,   // 600 would be capped to 500
			wantCostApprox: 1.507, // 0.007 + 500*0.003
		},
		{
			name:           "1 keyword depth 1",
			keywords:       []string{"Test"},
			depth:          1,
			maxResults:     nil,
			wantPrimary:    11,   // 11.17 * 1^0.7925 ≈ 11
			wantMin:        5,    // 11/2 = 5
			wantMax:        13,   // 11*6/5 = 13
			wantCostApprox: 0.04, // 0.007 + 11*0.003
		},
		{
			name:           "1 keyword depth 5 with email",
			keywords:       []string{"Cafe"},
			depth:          5,
			maxResults:     nil,
			email:          true,
			wantPrimary:    40,
			wantMin:        20,
			wantMax:        48,
			wantCostApprox: 0.207, // 0.007 + 40*0.003 + 40*0.002
		},
		{
			name:           "1 keyword depth 5 with reviews",
			keywords:       []string{"Cafe"},
			depth:          5,
			maxResults:     nil,
			reviewsMax:     10,
			wantPrimary:    40,
			wantMin:        20,
			wantMax:        48,
			wantCostApprox: 0.327, // 0.007 + 40*0.003 + 40*10*0.0005
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			est, err := svc.EstimateJobCost(ctx, tc.keywords, tc.depth, tc.maxResults, tc.email, tc.reviewsMax, tc.imagesMax)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if est.Places != tc.wantPrimary {
				t.Errorf("Places = %d, want %d", est.Places, tc.wantPrimary)
			}
			if est.PlacesMin != tc.wantMin {
				t.Errorf("PlacesMin = %d, want %d", est.PlacesMin, tc.wantMin)
			}
			if est.PlacesMax != tc.wantMax {
				t.Errorf("PlacesMax = %d, want %d", est.PlacesMax, tc.wantMax)
			}

			if math.Abs(est.Total-tc.wantCostApprox) > 0.01 {
				t.Errorf("Total = %.4f, want ~%.4f", est.Total, tc.wantCostApprox)
			}

			// Invariants
			if est.TotalMin > est.Total {
				t.Errorf("TotalMin (%.4f) > Total (%.4f)", est.TotalMin, est.Total)
			}
			if est.TotalMax < est.Total {
				t.Errorf("TotalMax (%.4f) < Total (%.4f)", est.TotalMax, est.Total)
			}
			if est.KeywordCount != len(tc.keywords) {
				t.Errorf("KeywordCount = %d, want %d", est.KeywordCount, len(tc.keywords))
			}
			if est.MaxResultsProvided != (tc.maxResults != nil) {
				t.Errorf("MaxResultsProvided = %v, want %v", est.MaxResultsProvided, tc.maxResults != nil)
			}

			// Unit prices must be populated
			if len(est.UnitPrices) == 0 {
				t.Error("UnitPrices is empty, expected pricing data")
			}
			if _, ok := est.UnitPrices["place_scraped"]; !ok {
				t.Error("UnitPrices missing 'place_scraped' key")
			}

			// Description must not be empty
			if est.Description == "" {
				t.Error("Description is empty")
			}
		})
	}
}

// TestEstimate_BugRegression verifies the exact bug scenario:
// "Cafe Mitte Berlin" at depth 5, no max_results should NOT produce 1.507 credits.
func TestEstimate_BugRegression(t *testing.T) {
	t.Parallel()

	svc := NewEstimationService(nil, nil)
	est, err := svc.EstimateJobCost(
		context.Background(),
		[]string{"Cafe Mitte Berlin"},
		5,     // depth
		nil,   // no max_results
		false, // email
		0,     // reviewsMax
		0,     // imagesMax
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The old bug: frontend sent max_results=500, producing 1.507 credits.
	// Depth-based estimation should yield: ~40 places → ~0.127 credits.
	if est.Total > 0.5 {
		t.Errorf("Total = %.4f — still inflated! Should be ~0.127, not 1.507", est.Total)
	}
	if est.Places > 100 {
		t.Errorf("Places = %d — still inflated! Should be ~40, not 500", est.Places)
	}
}

// TestEstimate_NoteContent verifies dynamic notes.
func TestEstimate_NoteContent(t *testing.T) {
	t.Parallel()

	svc := NewEstimationService(nil, nil)
	ctx := context.Background()

	// Single keyword, no cap
	est, _ := svc.EstimateJobCost(ctx, []string{"Test"}, 5, nil, false, 0, 0)
	if est.Description == "" {
		t.Error("Note should not be empty for single keyword")
	}

	// With explicit cap
	est, _ = svc.EstimateJobCost(ctx, []string{"Test"}, 5, intPtr(30), false, 0, 0)
	if est.Description == "" {
		t.Error("Note should not be empty with explicit cap")
	}

	// Multiple keywords
	est, _ = svc.EstimateJobCost(ctx, []string{"A", "B", "C"}, 5, nil, false, 0, 0)
	if est.Description == "" {
		t.Error("Note should not be empty for multiple keywords")
	}
}
