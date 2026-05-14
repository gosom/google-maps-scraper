package services

import (
	"context"
	"log/slog"
	"math"
	"testing"
)

// After the Cafe Schöneberg bug (May 2026), max_images is interpreted as
// per-place — every place is allowed up to N images, charged per image.
//
// The estimate must therefore compute imagesCost = places × N × priceImage,
// not the old min(places × Avg, N) per-job-total math which silently
// under-quoted any job where N > Avg per place.

func TestEstimateJobCost_ImagesPerPlace_ScalesByPlaces(t *testing.T) {
	t.Parallel()

	svc := NewEstimationService(nil, nil, slog.Default())
	ctx := context.Background()

	// 1 keyword, depth 5 → 40 places (matches the bug-scenario row already
	// covered by TestEstimateJobCost). Per-place cap of 10 images.
	maxImages := 10
	est, err := svc.EstimateJobCost(ctx, []string{"Cafe"}, 5, nil, false, intPtr(0), &maxImages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if est.Places != 40 {
		t.Fatalf("Places = %d, want 40 (test depends on this baseline)", est.Places)
	}

	// Per-place math: 40 places × 10 images × $0.0005 = 0.20
	// Plus job_start (0.007) + places (40 × 0.003 = 0.12) = 0.127 base
	// Total: 0.327
	want := 0.007 + 40*0.003 + 40*10*0.0005
	if math.Abs(est.Total-want) > 0.01 {
		t.Errorf("Total = %.4f, want ~%.4f (per-place math: 40 places × 10 images × 0.0005)",
			est.Total, want)
	}

	if est.Breakdown.ImagesCost != 40*10*0.0005 {
		t.Errorf("ImagesCost = %.4f, want %.4f", est.Breakdown.ImagesCost, 40*10*0.0005)
	}
}

func TestEstimateJobCost_ImagesPerPlace_HighCapNoArtificialCeiling(t *testing.T) {
	t.Parallel()

	svc := NewEstimationService(nil, nil, slog.Default())
	ctx := context.Background()

	// 40 places × 500 cap = 20,000 estimated images. Under the old per-job
	// total semantics, estImages got clipped to min(40*30, 500) = 500.
	// Under per-place semantics, it must be 40 × 500 = 20 000.
	maxImages := 500
	est, err := svc.EstimateJobCost(ctx, []string{"Cafe"}, 5, nil, false, intPtr(0), &maxImages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantImagesCost := float64(40*500) * 0.0005 // 10.0
	if math.Abs(est.Breakdown.ImagesCost-wantImagesCost) > 0.01 {
		t.Errorf("ImagesCost = %.4f, want %.4f (per-place math, no per-job ceiling)",
			est.Breakdown.ImagesCost, wantImagesCost)
	}
}

func TestEstimateJobCost_ImagesNil_NoLimit_UsesAverage(t *testing.T) {
	t.Parallel()

	svc := NewEstimationService(nil, nil, slog.Default())
	ctx := context.Background()

	// "No cap" (nil) preserves the per-place average fallback. This must
	// keep working — the estimate endpoint sends nil when the user toggles
	// "no limit" in the UI.
	est, err := svc.EstimateJobCost(ctx, []string{"Cafe"}, 5, nil, false, intPtr(0), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantImagesCost := float64(40*AvgImagesPerPlace) * 0.0005 // 40 × 30 × 0.0005 = 0.60
	if math.Abs(est.Breakdown.ImagesCost-wantImagesCost) > 0.01 {
		t.Errorf("ImagesCost = %.4f, want %.4f (avg fallback when maxImages is nil)",
			est.Breakdown.ImagesCost, wantImagesCost)
	}
}

func TestEstimateJobCost_ImagesZero_NoCharge(t *testing.T) {
	t.Parallel()

	svc := NewEstimationService(nil, nil, slog.Default())
	ctx := context.Background()

	// max_images=0 (toggle off) → no image cost contribution.
	est, err := svc.EstimateJobCost(ctx, []string{"Cafe"}, 5, nil, false, intPtr(0), intPtr(0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if est.Breakdown.ImagesCost != 0 {
		t.Errorf("ImagesCost = %.4f, want 0 (max_images=0 means skip images)",
			est.Breakdown.ImagesCost)
	}
}
