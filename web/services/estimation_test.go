package services

import (
	"context"
	"log/slog"
	"math"
	"testing"
)

// intPtr is a helper for creating *int values in test tables.
func intPtr(v int) *int { return &v }

func TestEstimateJobCost(t *testing.T) {
	t.Parallel()

	// Use a nil-DB service — EstimateJobCost only needs pricing, which
	// falls back to hardcoded defaults when DB is nil.
	svc := NewEstimationService(nil, nil, slog.Default())
	ctx := context.Background()

	cases := []struct {
		name       string
		keywords   []string
		depth      int
		maxResults *int
		email      bool
		maxReviews *int
		maxImages  *int
		// Assertions on place estimates
		wantPrimary int
		wantMin     int
		wantMax     int
		// Assertion: total cost should be approximately this (±0.01)
		wantCostApprox float64
	}{
		{
			name:           "bug scenario: 1 keyword depth 5 no enrichments",
			keywords:       []string{"Cafe Mitte Berlin"},
			depth:          5,
			maxResults:     nil,
			maxReviews:     intPtr(0),
			maxImages:      intPtr(0),
			wantPrimary:    40,
			wantMin:        20,
			wantMax:        48,
			wantCostApprox: 0.127, // 0.007 + 40*0.003
		},
		{
			name:           "3 keywords depth 5 no enrichments",
			keywords:       []string{"Cafe", "Restaurant", "Bar"},
			depth:          5,
			maxResults:     nil,
			maxReviews:     intPtr(0),
			maxImages:      intPtr(0),
			wantPrimary:    120,
			wantMin:        60,
			wantMax:        144,
			wantCostApprox: 0.367, // 0.007 + 120*0.003
		},
		{
			name:           "1 keyword depth 5 user cap 30",
			keywords:       []string{"Cafe"},
			depth:          5,
			maxResults:     intPtr(30),
			maxReviews:     intPtr(0),
			maxImages:      intPtr(0),
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
			maxReviews:     intPtr(0),
			maxImages:      intPtr(0),
			wantPrimary:    500,
			wantMin:        250,
			wantMax:        500,
			wantCostApprox: 1.507, // 0.007 + 500*0.003
		},
		{
			name:           "1 keyword depth 1",
			keywords:       []string{"Test"},
			depth:          1,
			maxResults:     nil,
			maxReviews:     intPtr(0),
			maxImages:      intPtr(0),
			wantPrimary:    11,
			wantMin:        5,
			wantMax:        13,
			wantCostApprox: 0.04, // 0.007 + 11*0.003
		},
		{
			name:           "1 keyword depth 5 with email",
			keywords:       []string{"Cafe"},
			depth:          5,
			maxResults:     nil,
			email:          true,
			maxReviews:     intPtr(0),
			maxImages:      intPtr(0),
			wantPrimary:    40,
			wantMin:        20,
			wantMax:        48,
			wantCostApprox: 0.207, // 0.007 + 40*0.003 + 40*0.002
		},
		{
			name:           "1 keyword depth 5 with reviews cap 10",
			keywords:       []string{"Cafe"},
			depth:          5,
			maxResults:     nil,
			maxReviews:     intPtr(10),
			maxImages:      intPtr(0),
			wantPrimary:    40,
			wantMin:        20,
			wantMax:        48,
			wantCostApprox: 0.327, // 0.007 + 40*0.003 + 40*10*0.0005
		},
		{
			name:           "1 keyword depth 5 reviews no limit",
			keywords:       []string{"Cafe"},
			depth:          5,
			maxResults:     nil,
			maxReviews:     nil, // no limit → avg 50/place
			maxImages:      intPtr(0),
			wantPrimary:    40,
			wantMin:        20,
			wantMax:        48,
			wantCostApprox: 1.127, // 0.007 + 40*0.003 + 40*50*0.0005
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			est, err := svc.EstimateJobCost(ctx, tc.keywords, tc.depth, tc.maxResults, tc.email, tc.maxReviews, tc.maxImages)
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

// TestEstimate_CreateJobMatchesEstimateEndpoint pins the May 10 prod 402 bug.
//
// Prod scenario:
//   - Frontend sends `{depth:5, max_results:0, max_reviews:0, max_images:0,
//     include_emails:false}` for both the estimate preview and the actual
//     job create.
//   - GET /api/v1/jobs/estimates returned total=0.127 (correct: 40 places,
//     no reviews, no images, no emails).
//   - POST /api/v1/jobs returned 402 with total=1.727 because the create
//     handler converted MaxReviews=0/MaxImages=0 to nil pointers, and the
//     estimator's nil-branch applied AvgReviewsPerPlace=50 +
//     AvgImagesPerPlace=30, billing the user for 2000 reviews + 1200
//     images they did not ask for.
//
// Fix: create handler now forwards MaxReviews/MaxImages as *int(0) when
// the wire value is 0, mirroring what the estimate endpoint already does
// via its `*int` request struct. This test pins the contract at the
// service level: passing &0 for the two enrichment caps must produce a
// cost identical to the no-enrichments depth-5 run, never the inflated
// "with defaults" cost.
func TestEstimate_CreateJobMatchesEstimateEndpoint(t *testing.T) {
	t.Parallel()
	svc := NewEstimationService(nil, nil, slog.Default())
	ctx := context.Background()

	// Mirror the wire payload of both endpoints in prod.
	keywords := []string{"Cafe Mitte Berlin"}
	depth := 5
	includeEmails := false

	// Estimate-endpoint shape: max_reviews=0, max_images=0 → *int(0).
	got, err := svc.EstimateJobCost(ctx, keywords, depth, nil, includeEmails, intPtr(0), intPtr(0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The pre-fix create handler would have called this:
	//   svc.EstimateJobCost(ctx, keywords, depth, nil, includeEmails, nil, nil)
	// — i.e. nil pointers for the two enrichment caps. That triggers the
	// estimator's "nil = no limit, use averages" branches and adds 2000
	// reviews + 1200 images to the cost. Compute it explicitly so we can
	// assert the gap is meaningful (no false-positive on 0 vs 0.001).
	pre, err := svc.EstimateJobCost(ctx, keywords, depth, nil, includeEmails, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error (pre-fix shape): %v", err)
	}

	if got.Total >= pre.Total {
		t.Errorf("post-fix total %.4f is not < pre-fix total %.4f — the bug is back: nil and &0 must produce different costs",
			got.Total, pre.Total)
	}
	if got.Reviews != 0 {
		t.Errorf("Reviews = %d, expected 0 (toggle off)", got.Reviews)
	}
	if got.Images != 0 {
		t.Errorf("Images = %d, expected 0 (toggle off)", got.Images)
	}
	if got.Total > 0.5 {
		t.Errorf("Total = %.4f — still inflated. Expected ~0.127 for depth-5 with no enrichments", got.Total)
	}
}

// TestEstimate_BugRegression verifies the exact bug scenario:
// "Cafe Mitte Berlin" at depth 5, no max_results should NOT produce 1.507 credits.
func TestEstimate_BugRegression(t *testing.T) {
	t.Parallel()

	svc := NewEstimationService(nil, nil, slog.Default())
	est, err := svc.EstimateJobCost(
		context.Background(),
		[]string{"Cafe Mitte Berlin"},
		5,         // depth
		nil,       // no max_results
		false,     // email
		intPtr(0), // maxReviews (reviews off)
		intPtr(0), // maxImages (images off)
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

	svc := NewEstimationService(nil, nil, slog.Default())
	ctx := context.Background()

	// Single keyword, no cap
	est, _ := svc.EstimateJobCost(ctx, []string{"Test"}, 5, nil, false, intPtr(0), intPtr(0))
	if est.Description == "" {
		t.Error("Note should not be empty for single keyword")
	}

	// With explicit cap
	est, _ = svc.EstimateJobCost(ctx, []string{"Test"}, 5, intPtr(30), false, intPtr(0), intPtr(0))
	if est.Description == "" {
		t.Error("Note should not be empty with explicit cap")
	}

	// Multiple keywords
	est, _ = svc.EstimateJobCost(ctx, []string{"A", "B", "C"}, 5, nil, false, intPtr(0), intPtr(0))
	if est.Description == "" {
		t.Error("Note should not be empty for multiple keywords")
	}
}
