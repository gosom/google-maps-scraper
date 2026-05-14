package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/gosom/google-maps-scraper/models"
	webutils "github.com/gosom/google-maps-scraper/web/utils"
	"github.com/shopspring/decimal"
)

// EstimationService provides job cost estimation functionality
type EstimationService struct {
	db        *sql.DB
	log       *slog.Logger
	priceRepo models.PricingRuleRepository
}

// Estimation constants - average values based on typical Google Maps data
const (
	AvgReviewsPerPlace = 50
	AvgImagesPerPlace  = 30

	// Default pricing fallbacks (used when DB has no active rules)
	defaultPriceJobStart       = 0.007
	defaultPricePlaceScraped   = 0.003
	defaultPriceContactDetails = 0.002
	defaultPriceReview         = 0.0005
	defaultPriceImage          = 0.0005

	priceCacheTTL = 60 * time.Second

	// microUnit is a package-level alias for models.MicroUnit for brevity.
	microUnit = models.MicroUnit
)

// Package-level pricing cache shared across per-request EstimationService instances.
var (
	priceCacheMu   sync.RWMutex
	priceCacheData map[string]int64 // micro-credits
	priceCacheTime time.Time
)

// defaultPricesMicro stores default prices as micro-credits (int64).
var defaultPricesMicro = map[string]int64{
	"job_start":       creditsToMicro(defaultPriceJobStart),
	"place_scraped":   creditsToMicro(defaultPricePlaceScraped),
	"contact_details": creditsToMicro(defaultPriceContactDetails),
	"review":          creditsToMicro(defaultPriceReview),
	"image":           creditsToMicro(defaultPriceImage),
}

// creditsToMicro converts a float64 credit value to integer micro-credits.
// The rounding eliminates IEEE 754 representation errors.
func creditsToMicro(credits float64) int64 {
	return int64(math.Round(credits * microUnit))
}

// microToCredits converts micro-credits back to a float64 credit value.
func microToCredits(micro int64) float64 {
	return float64(micro) / microUnit
}

// CostEstimate provides a range-based cost estimate with per-keyword
// breakdown and full unit-price transparency.
// CostBreakdown itemises the cost components of an estimate.
type CostBreakdown struct {
	JobStartCost       float64 `json:"job_start_cost"`
	PlacesCost         float64 `json:"places_cost"`
	ContactDetailsCost float64 `json:"contact_details_cost"`
	ReviewsCost        float64 `json:"reviews_cost"`
	ImagesCost         float64 `json:"images_cost"`
}

// CostEstimate is the top-level estimate object returned by EstimateJobCost.
type CostEstimate struct {
	Object   string `json:"object"`   // always "job_estimate"
	Currency string `json:"currency"` // always "credits"

	// Place estimates
	Places    int `json:"places"`
	PlacesMin int `json:"places_min"`
	PlacesMax int `json:"places_max"`

	// Per-keyword info
	KeywordCount     int `json:"keyword_count"`
	PlacesPerKeyword int `json:"places_per_keyword"`

	// Costs
	Total    float64 `json:"total"`
	TotalMin float64 `json:"total_min"`
	TotalMax float64 `json:"total_max"`

	Breakdown CostBreakdown `json:"breakdown"`

	// Secondary resources
	Reviews        int  `json:"reviews"`
	Images         int  `json:"images"`
	IncludesEmails bool `json:"includes_emails"`

	// Transparency
	UnitPrices         map[string]float64 `json:"unit_prices"`
	MaxResultsProvided bool               `json:"max_results_provided"`
	Description        string             `json:"description"`

	// TTL — estimate is valid until pricing cache expires
	ExpiresAt int64 `json:"expires_at"` // Unix timestamp

	// Internal (not serialised to JSON)
	totalMicro    int64
	minTotalMicro int64
}

// TotalMicro returns the total estimated cost in micro-credits for precise
// integer comparison (e.g. balance checks).
func (c *CostEstimate) TotalMicro() int64 { return c.totalMicro }

// MinTotalMicro returns the minimum estimated cost in micro-credits,
// used for the credit sufficiency gate so users are not blocked
// by an optimistic estimate.
func (c *CostEstimate) MinTotalMicro() int64 { return c.minTotalMicro }

func NewEstimationService(db *sql.DB, priceRepo models.PricingRuleRepository, logger *slog.Logger) *EstimationService {
	return &EstimationService{
		db:        db,
		log:       logger.With(slog.String("service", "estimation")),
		priceRepo: priceRepo,
	}
}

// ErrPricingUnavailable is returned by EstimateJobCost when the pricing
// rules cache is empty AND the database is unreachable. Callers should
// surface this to the user as 503 Service Unavailable, not 200 with a
// silently-defaulted estimate. Pre-fix the estimator returned hardcoded
// defaults on DB error — that "fail-open on a money path" pattern meant
// estimates and end-of-job charges could quote different prices to the
// same user during a pricing_rules outage.
var ErrPricingUnavailable = errors.New("pricing rules unavailable")

// getPriceMicro returns the price for an event type in micro-credits.
// On a clean cold-start path s.priceRepo is nil (used by unit tests),
// in which case hardcoded defaults are still returned — the production
// path goes through loadPrices and bubbles up ErrPricingUnavailable on
// real DB failures.
func (s *EstimationService) getPriceMicro(ctx context.Context, eventType string) int64 {
	prices, err := s.loadPrices(ctx)
	if err != nil {
		// Last-resort fallback only used by callers that explicitly
		// want a "best-effort" price (none in production today). The
		// estimator itself calls loadPricesStrict via EstimateJobCost
		// and surfaces the error to the HTTP handler.
		if p, ok := defaultPricesMicro[eventType]; ok {
			return p
		}
		return 0
	}
	if p, ok := prices[eventType]; ok {
		return p
	}
	if p, ok := defaultPricesMicro[eventType]; ok {
		return p
	}
	return 0
}

// loadPrices returns the cached pricing map (micro-credits), refreshing
// from DB if stale. When priceRepo is nil (unit tests) returns hardcoded
// defaults with no error. When the DB lookup fails it returns
// ErrPricingUnavailable — callers must decide whether to surface that
// to the user or fall back to defaults explicitly.
func (s *EstimationService) loadPrices(ctx context.Context) (map[string]int64, error) {
	priceCacheMu.RLock()
	if priceCacheData != nil && time.Since(priceCacheTime) < priceCacheTTL {
		defer priceCacheMu.RUnlock()
		return priceCacheData, nil
	}
	priceCacheMu.RUnlock()

	priceCacheMu.Lock()
	defer priceCacheMu.Unlock()

	// Double-check after acquiring write lock.
	if priceCacheData != nil && time.Since(priceCacheTime) < priceCacheTTL {
		return priceCacheData, nil
	}

	if s.priceRepo == nil {
		// Unit-test path — defaults are intentional, not a failure.
		return defaultPricesMicro, nil
	}

	prices, err := s.priceRepo.GetActiveDefaultPrices(ctx)
	if err != nil {
		// Real DB error: surface it. Do NOT cache — next call retries.
		// %w + %w (Go ≥ 1.20) preserves both the sentinel for errors.Is
		// matching at the HTTP layer AND the inner driver error for
		// errors.As (pgconn.PgError, etc.) at the log site, without
		// duplicating the Error here. The duplicate-log to the handler
		// is intentional: this layer carries the raw cause; the handler
		// adds user/path context.
		return nil, fmt.Errorf("%w: %w", ErrPricingUnavailable, err)
	}
	if len(prices) == 0 {
		// Empty table is a deployment / migration error, not a
		// transient failure. Same treatment: surface, do not cache.
		// No inner cause to wrap — the sentinel itself is the message.
		return nil, ErrPricingUnavailable
	}

	priceCacheData = prices
	priceCacheTime = time.Now()
	s.log.Debug("pricing_rules_refreshed", slog.Int("count", len(prices)))
	return priceCacheData, nil
}

// estimatePlacesFromDepth returns the approximate number of places for a given
// search depth using the formula: round(11.17 * depth^0.7925).
// Calibrated: depth 5 ≈ 40, depth 20 ≈ 120.
func estimatePlacesFromDepth(depth int) int {
	if depth < 1 {
		depth = 1
	}
	return int(math.Round(11.17 * math.Pow(float64(depth), 0.7925)))
}

// EstimateJobCost calculates a range-based cost estimate using depth and
// keyword count. When maxResults is nil, the estimate is derived entirely
// from the depth power-law formula per keyword.
//
// Algorithm:
//
//	placesPerKeyword = estimatePlacesFromDepth(depth)
//	rawEstimate      = len(keywords) × placesPerKeyword
//	primaryEstimate  = min(rawEstimate, *maxResults if set, CapMaxResults)
//	minEstimate      = max(1, primaryEstimate / 2)
//	maxEstimate      = min(CapMaxResults, primaryEstimate * 6/5)
func (s *EstimationService) EstimateJobCost(
	ctx context.Context,
	keywords []string,
	depth int,
	maxResults *int,
	email bool,
	maxReviews *int,
	maxImages *int,
) (*CostEstimate, error) {
	if depth < 1 {
		depth = 1
	}

	// Resolve effective reviews-per-place for estimation.
	// nil means "no limit" — estimate at the realistic average.
	// An explicit value means "the user chose this cap" — estimate honestly.
	reviewsPerPlace := 0
	maxReviewsProvided := maxReviews != nil
	if maxReviewsProvided {
		reviewsPerPlace = *maxReviews
	}

	// max_images is interpreted per-place (May 2026 — Cafe Schöneberg
	// fix; previously per-job total). nil = "no limit" → estimate at
	// the per-place average; positive value = honest per-place cap.
	imagesPerPlace := 0
	maxImagesProvided := maxImages != nil
	if maxImagesProvided {
		imagesPerPlace = *maxImages
	}

	// Load unit prices. Bubble up ErrPricingUnavailable so the HTTP
	// handler can return 503 — silently defaulting here would let an
	// estimate succeed with stale prices that disagree with the
	// end-of-job charge.
	prices, err := s.loadPrices(ctx)
	if err != nil {
		return nil, err
	}
	priceLookup := func(eventType string) int64 {
		if p, ok := prices[eventType]; ok {
			return p
		}
		// Per-event fallback for keys that exist in defaults but not in
		// the DB-backed map (forward-compat with new event types added
		// in code before the DB row exists). Logged at warn so ops sees it.
		if p, ok := defaultPricesMicro[eventType]; ok {
			s.log.Warn("pricing_event_fallback_to_default", slog.String("event_type", eventType))
			return p
		}
		return 0
	}
	priceJobStart := priceLookup("job_start")
	pricePlaceScraped := priceLookup("place_scraped")
	priceContactDetails := priceLookup("contact_details")
	priceReview := priceLookup("review")
	priceImage := priceLookup("image")

	// ── Place estimation ───────────────────────────────────────────────
	placesPerKeyword := estimatePlacesFromDepth(depth)
	rawEstimate := len(keywords) * placesPerKeyword

	maxResultsExplicit := maxResults != nil
	primaryEstimate := rawEstimate
	if maxResultsExplicit && *maxResults < primaryEstimate {
		primaryEstimate = *maxResults
	}
	if primaryEstimate > webutils.CapMaxResults {
		primaryEstimate = webutils.CapMaxResults
	}

	minEstimate := primaryEstimate / 2
	if minEstimate < 1 {
		minEstimate = 1
	}
	maxEstimate := primaryEstimate * 6 / 5
	if maxEstimate > webutils.CapMaxResults {
		maxEstimate = webutils.CapMaxResults
	}

	// ── Cost calculation for all three estimates ───────────────────────
	calcCost := func(places int) (total int64, breakdown [5]int64) {
		jobStart := priceJobStart
		placesMicro := int64(places) * pricePlaceScraped
		var contactMicro, reviewsMicro, imagesMicro int64
		if email {
			contactMicro = int64(places) * priceContactDetails
		}
		if reviewsPerPlace > 0 {
			reviewsMicro = int64(places) * int64(reviewsPerPlace) * priceReview
		} else if !maxReviewsProvided {
			// "No limit" — estimate at realistic average
			reviewsMicro = int64(places) * int64(AvgReviewsPerPlace) * priceReview
		}
		if imagesPerPlace > 0 {
			// Per-place math: every place is allowed up to N images,
			// and every image is charged. The old per-job-total cap
			// (min(places × Avg, N)) silently under-quoted any job
			// where N > Avg — the Cafe Schöneberg bug.
			//
			// Widen each factor to int64 BEFORE multiplying so a future
			// cap relaxation can't silently overflow. Pattern mirrors
			// the placesMicro / contactMicro lines above.
			imagesMicro = int64(places) * int64(imagesPerPlace) * priceImage
		} else if !maxImagesProvided {
			// "No limit" — estimate at realistic per-place average.
			imagesMicro = int64(places) * int64(AvgImagesPerPlace) * priceImage
		}
		total = jobStart + placesMicro + contactMicro + reviewsMicro + imagesMicro
		breakdown = [5]int64{jobStart, placesMicro, contactMicro, reviewsMicro, imagesMicro}
		return
	}

	primaryTotal, primaryBreakdown := calcCost(primaryEstimate)
	minTotal, _ := calcCost(minEstimate)
	maxTotal, _ := calcCost(maxEstimate)

	// ── Secondary resource counts (based on primary estimate) ─────────
	estimatedReviews := 0
	if reviewsPerPlace > 0 {
		estimatedReviews = primaryEstimate * reviewsPerPlace
	} else if !maxReviewsProvided {
		estimatedReviews = primaryEstimate * AvgReviewsPerPlace
	}
	estimatedImages := 0
	if imagesPerPlace > 0 {
		estimatedImages = primaryEstimate * imagesPerPlace
	} else if !maxImagesProvided {
		estimatedImages = primaryEstimate * AvgImagesPerPlace
	}

	// ── Build unit prices map ─────────────────────────────────────────
	unitPrices := map[string]float64{
		"job_start":       microToCredits(priceJobStart),
		"place_scraped":   microToCredits(pricePlaceScraped),
		"contact_details": microToCredits(priceContactDetails),
		"review":          microToCredits(priceReview),
		"image":           microToCredits(priceImage),
	}

	estimate := &CostEstimate{
		Object:   "job_estimate",
		Currency: "credits",

		Places:    primaryEstimate,
		PlacesMin: minEstimate,
		PlacesMax: maxEstimate,

		KeywordCount:     len(keywords),
		PlacesPerKeyword: placesPerKeyword,

		Total:    microToCredits(primaryTotal),
		TotalMin: microToCredits(minTotal),
		TotalMax: microToCredits(maxTotal),

		Breakdown: CostBreakdown{
			JobStartCost:       microToCredits(primaryBreakdown[0]),
			PlacesCost:         microToCredits(primaryBreakdown[1]),
			ContactDetailsCost: microToCredits(primaryBreakdown[2]),
			ReviewsCost:        microToCredits(primaryBreakdown[3]),
			ImagesCost:         microToCredits(primaryBreakdown[4]),
		},

		Reviews:        estimatedReviews,
		Images:         estimatedImages,
		IncludesEmails: email,

		UnitPrices:         unitPrices,
		MaxResultsProvided: maxResultsExplicit,
		Description:        generateEstimationNote(len(keywords), depth, maxResultsExplicit, primaryEstimate, minEstimate, maxEstimate),
		ExpiresAt:          time.Now().Add(priceCacheTTL).Unix(),

		totalMicro:    primaryTotal,
		minTotalMicro: minTotal,
	}

	s.log.Debug("job_cost_estimated",
		slog.Int("keyword_count", len(keywords)),
		slog.Int("depth", depth),
		slog.Int("places", primaryEstimate),
		slog.Int("places_min", minEstimate),
		slog.Int("places_max", maxEstimate),
		slog.Float64("total", estimate.Total),
		slog.Float64("total_min", estimate.TotalMin),
		slog.Float64("total_max", estimate.TotalMax),
	)

	return estimate, nil
}

// generateEstimationNote creates a context-aware note for the estimate.
func generateEstimationNote(keywordCount, depth int, maxResultsExplicit bool, primary, min, max int) string {
	if maxResultsExplicit {
		return fmt.Sprintf(
			"Capped at %d places (your limit). Depth %d could yield ~%d places per keyword without a cap.",
			primary, depth, estimatePlacesFromDepth(depth),
		)
	}
	if keywordCount == 1 {
		return fmt.Sprintf(
			"Estimated ~%d places for 1 keyword at depth %d (range: %d–%d).",
			primary, depth, min, max,
		)
	}
	return fmt.Sprintf(
		"Estimated ~%d places across %d keywords at depth %d (range: %d–%d).",
		primary, keywordCount, depth, min, max,
	)
}

// CheckSufficientBalance verifies that a user has enough AVAILABLE credit
// (balance minus already-reserved holds) for a job at the FULL estimated
// total — not the optimistic minimum.
//
// Two reasons we use Total here, not MinTotal:
//
//  1. Consistency with the transactional gate in
//     ConcurrentLimitService.CreateJobWithLimit: that gate compares
//     against EstimatedCost (which is Total). If this fast-fail compared
//     against MinTotal a user with balance ∈ [MinTotal, Total) would
//     pass this check and fail the next one — same click, two reads of
//     the same balance, two different verdicts. Confusing for the user
//     and an unprovable support ticket.
//
//  2. Persisted-quote semantics: Total is also what we now write to
//     jobs.estimated_cost_precise as the user's quote. The gate must
//     match the quote.
//
// "Available" = credit_balance - credit_held_precise (see migration
// 000036). Holds reserve credits for in-flight jobs so two concurrent
// submissions can't both pass against the same dollar.
func (s *EstimationService) CheckSufficientBalance(ctx context.Context, userID string, estimate *CostEstimate) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	var balanceStr, heldStr string
	const query = `SELECT COALESCE(credit_balance, 0)::text, COALESCE(credit_held_precise, 0)::text FROM users WHERE id = $1`
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&balanceStr, &heldStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Mirror CreateJobWithLimit's behaviour: an unprovisioned
			// user has zero balance, zero held. The downstream
			// affordability check will then translate that into the
			// same 402 a real-but-broke user gets, with the same
			// error message. Pre-fix this branch returned "user not
			// found" verbatim, which leaked an internal concept to
			// the client and confused support — users whose Clerk
			// row hadn't replicated yet appeared to "not exist".
			balanceStr = "0"
			heldStr = "0"
		} else {
			return fmt.Errorf("failed to retrieve credit balance: %w", err)
		}
	}

	balanceDec, err := decimal.NewFromString(balanceStr)
	if err != nil {
		return fmt.Errorf("failed to parse credit balance: %w", err)
	}
	heldDec, err := decimal.NewFromString(heldStr)
	if err != nil {
		return fmt.Errorf("failed to parse credit held: %w", err)
	}
	availableDec := balanceDec.Sub(heldDec)
	availableMicro := availableDec.Mul(decimal.NewFromInt(microUnit)).IntPart()

	if availableMicro < estimate.TotalMicro() {
		availableFloat, _ := availableDec.Float64()
		// Info, not Warn: a user with insufficient balance is a normal
		// state (free-tier user submitting a too-big job), not a system
		// problem. Warn-level routes to paging in our alerting setup.
		s.log.Info("insufficient_credits",
			slog.String("user_id", userID),
			slog.Float64("available", availableFloat),
			slog.Float64("required", estimate.Total),
			slog.Int("places", estimate.Places),
		)
		// Same typed error shape the transactional gate returns
		// (concurrent_limit.go ErrInsufficientBalance). One sentinel
		// for both code paths means handlers errors.As once and render
		// the user message via UserMessage(). Error() is a stable
		// low-cardinality string for log grouping.
		return ErrInsufficientBalance{
			Balance:        availableFloat,
			RequiredCost:   estimate.Total,
			EstimatedCount: estimate.Places,
		}
	}

	return nil
}
