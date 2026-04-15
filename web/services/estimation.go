package services

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gosom/google-maps-scraper/models"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
	webutils "github.com/gosom/google-maps-scraper/web/utils"
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

func NewEstimationService(db *sql.DB, priceRepo models.PricingRuleRepository) *EstimationService {
	return &EstimationService{
		db:        db,
		log:       pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "estimation"),
		priceRepo: priceRepo,
	}
}

// getPriceMicro returns the price for an event type in micro-credits,
// reading from a cached copy of the pricing_rules table. Falls back to
// hardcoded defaults on DB error or missing rule.
func (s *EstimationService) getPriceMicro(ctx context.Context, eventType string) int64 {
	prices := s.loadPrices(ctx)
	if p, ok := prices[eventType]; ok {
		return p
	}
	if p, ok := defaultPricesMicro[eventType]; ok {
		return p
	}
	return 0
}

// loadPrices returns the cached pricing map (micro-credits), refreshing from DB if stale.
func (s *EstimationService) loadPrices(ctx context.Context) map[string]int64 {
	priceCacheMu.RLock()
	if priceCacheData != nil && time.Since(priceCacheTime) < priceCacheTTL {
		defer priceCacheMu.RUnlock()
		return priceCacheData
	}
	priceCacheMu.RUnlock()

	priceCacheMu.Lock()
	defer priceCacheMu.Unlock()

	// Double-check after acquiring write lock.
	if priceCacheData != nil && time.Since(priceCacheTime) < priceCacheTTL {
		return priceCacheData
	}

	if s.priceRepo == nil {
		return defaultPricesMicro
	}

	prices, err := s.priceRepo.GetActiveDefaultPrices(ctx)
	if err != nil {
		s.log.Warn("pricing_rules_load_failed", slog.Any("error", err))
		return defaultPricesMicro
	}
	if len(prices) == 0 {
		s.log.Warn("no active pricing rules found in DB, using defaults")
		return defaultPricesMicro
	}

	priceCacheData = prices
	priceCacheTime = time.Now()
	s.log.Debug("pricing_rules_refreshed", slog.Int("count", len(prices)))
	return priceCacheData
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
	imagesMax int,
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

	// Load unit prices (cached, from DB or defaults).
	priceJobStart := s.getPriceMicro(ctx, "job_start")
	pricePlaceScraped := s.getPriceMicro(ctx, "place_scraped")
	priceContactDetails := s.getPriceMicro(ctx, "contact_details")
	priceReview := s.getPriceMicro(ctx, "review")
	priceImage := s.getPriceMicro(ctx, "image")

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
			reviewsMicro = int64(places*reviewsPerPlace) * priceReview
		} else if !maxReviewsProvided {
			// "No limit" — estimate at realistic average
			reviewsMicro = int64(places*AvgReviewsPerPlace) * priceReview
		}
		if imagesMax > 0 {
			estImages := places * AvgImagesPerPlace
			if estImages > imagesMax {
				estImages = imagesMax
			}
			imagesMicro = int64(estImages) * priceImage
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
	if imagesMax > 0 {
		estimatedImages = primaryEstimate * AvgImagesPerPlace
		if estimatedImages > imagesMax {
			estimatedImages = imagesMax
		}
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

// CheckSufficientBalance verifies if a user has enough credits for a job.
// Uses the minimum estimate so users are not blocked unnecessarily.
func (s *EstimationService) CheckSufficientBalance(ctx context.Context, userID string, estimate *CostEstimate) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	var balanceStr string
	const query = `SELECT COALESCE(credit_balance, 0)::text FROM users WHERE id = $1`
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&balanceStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("user not found")
		}
		return fmt.Errorf("failed to retrieve credit balance: %w", err)
	}

	balanceFloat, err := strconv.ParseFloat(balanceStr, 64)
	if err != nil {
		return fmt.Errorf("failed to parse credit balance: %w", err)
	}
	balanceMicro := creditsToMicro(balanceFloat)

	if balanceMicro < estimate.MinTotalMicro() {
		creditBalance := microToCredits(balanceMicro)
		s.log.Warn("insufficient_credits",
			slog.String("user_id", userID),
			slog.Float64("balance", creditBalance),
			slog.Float64("required", estimate.Total),
			slog.Int("places", estimate.Places),
		)
		return fmt.Errorf(
			"insufficient credits: you have %.4f credits but this job requires a minimum of %.4f credits to start (estimated cost for %d places). Please purchase more credits to continue",
			creditBalance,
			estimate.TotalMin,
			estimate.Places,
		)
	}

	return nil
}
