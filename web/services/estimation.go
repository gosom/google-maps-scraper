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
)

// EstimationService provides job cost estimation functionality
type EstimationService struct {
	db        *sql.DB
	log       *slog.Logger
	priceRepo models.PricingRuleRepository
}

// Estimation constants - average values based on typical Google Maps data
const (
	AvgReviewsPerPlace          = 25
	AvgImagesPerPlace           = 30
	UnlimitedReviewsThreshold = 1000

	// Default pricing fallbacks (used when DB has no active rules)
	defaultPriceActorStart        = 0.007
	defaultPricePlaceScraped      = 0.004
	defaultPriceFiltersApplied    = 0.001
	defaultPriceAdditionalDetails = 0.002
	defaultPriceContactDetails    = 0.002
	defaultPriceReview            = 0.0005
	defaultPriceImage             = 0.0005

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
	"actor_start":              creditsToMicro(defaultPriceActorStart),
	"place_scraped":            creditsToMicro(defaultPricePlaceScraped),
	"filters_applied":          creditsToMicro(defaultPriceFiltersApplied),
	"additional_place_details": creditsToMicro(defaultPriceAdditionalDetails),
	"contact_details":          creditsToMicro(defaultPriceContactDetails),
	"review":                   creditsToMicro(defaultPriceReview),
	"image":                    creditsToMicro(defaultPriceImage),
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

// CostEstimate represents the estimated cost breakdown for a job
type CostEstimate struct {
	ActorStartCost      float64 `json:"actor_start_cost"`
	PlacesCost          float64 `json:"places_cost"`
	ContactDetailsCost  float64 `json:"contact_details_cost"`
	ReviewsCost         float64 `json:"reviews_cost"`
	ImagesCost          float64 `json:"images_cost"`
	TotalEstimatedCost  float64 `json:"total_estimated_cost"`
	EstimatedPlaces     int     `json:"estimated_places"`
	EstimatedReviews    int     `json:"estimated_reviews"`
	EstimatedImages     int     `json:"estimated_images"`
	IncludesEmailScrape bool    `json:"includes_email_scrape"`
	Note                string  `json:"note"`

	// totalMicro is the authoritative total in micro-credits (int64).
	// Used internally for precise comparisons; not serialised to JSON.
	totalMicro int64
}

// TotalMicro returns the total estimated cost in micro-credits for precise
// integer comparison (e.g. balance checks). This avoids IEEE 754 rounding
// errors that occur when comparing float64 monetary values.
func (c *CostEstimate) TotalMicro() int64 {
	return c.totalMicro
}

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
		s.log.Warn("failed to load pricing rules from DB, using defaults", slog.String("error", err.Error()))
		return defaultPricesMicro
	}
	if len(prices) == 0 {
		s.log.Warn("no active pricing rules found in DB, using defaults")
		return defaultPricesMicro
	}

	priceCacheData = prices
	priceCacheTime = time.Now()
	s.log.Debug("pricing rules refreshed from DB", slog.Int("count", len(prices)))
	return priceCacheData
}

// EstimateJobCost calculates the estimated cost for a job based on its parameters.
// All arithmetic is performed in integer micro-credits to avoid IEEE 754 rounding
// errors. The returned CostEstimate contains float64 fields for JSON compatibility.
func (s *EstimationService) EstimateJobCost(ctx context.Context, jobData *models.JobData) (*CostEstimate, error) {
	estimate := &CostEstimate{}

	priceActorStart := s.getPriceMicro(ctx, "actor_start")
	pricePlaceScraped := s.getPriceMicro(ctx, "place_scraped")
	priceContactDetails := s.getPriceMicro(ctx, "contact_details")
	priceReview := s.getPriceMicro(ctx, "review")
	priceImage := s.getPriceMicro(ctx, "image")

	// All intermediate values are int64 micro-credits.
	var actorStartMicro, placesMicro, contactMicro, reviewsMicro, imagesMicro int64

	// 1. Actor start cost (flat fee per job)
	actorStartMicro = priceActorStart

	// 2. Determine estimated number of places
	estimatedPlaces := s.estimatePlaceCount(jobData)
	estimate.EstimatedPlaces = estimatedPlaces

	// 3. Calculate places cost
	placesMicro = int64(estimatedPlaces) * pricePlaceScraped

	// 4. Calculate contact details cost (if email scraping is enabled)
	if jobData.Email {
		estimate.IncludesEmailScrape = true
		contactMicro = int64(estimatedPlaces) * priceContactDetails
	}

	// 5. Calculate reviews cost (ONLY if reviews are explicitly requested)
	if jobData.ReviewsMax > 0 {
		estimatedReviews := s.estimateReviewCount(jobData, estimatedPlaces)
		estimate.EstimatedReviews = estimatedReviews
		reviewsMicro = int64(estimatedReviews) * priceReview
	}

	// 6. Calculate images cost (if images are requested)
	if jobData.Images {
		estimatedImages := s.estimateImageCount(jobData, estimatedPlaces)
		estimate.EstimatedImages = estimatedImages
		imagesMicro = int64(estimatedImages) * priceImage
	}

	// 7. Calculate total in micro-credits (exact integer arithmetic)
	totalMicro := actorStartMicro + placesMicro + contactMicro + reviewsMicro + imagesMicro

	// 8. Convert micro-credits to float64 for JSON-compatible struct fields
	estimate.ActorStartCost = microToCredits(actorStartMicro)
	estimate.PlacesCost = microToCredits(placesMicro)
	estimate.ContactDetailsCost = microToCredits(contactMicro)
	estimate.ReviewsCost = microToCredits(reviewsMicro)
	estimate.ImagesCost = microToCredits(imagesMicro)
	estimate.TotalEstimatedCost = microToCredits(totalMicro)
	estimate.totalMicro = totalMicro

	// 9. Add note about estimation
	estimate.Note = s.generateEstimationNote(jobData)

	s.log.Debug("job_cost_estimated",
		slog.Int("estimated_places", estimate.EstimatedPlaces),
		slog.Int("estimated_reviews", estimate.EstimatedReviews),
		slog.Int("estimated_images", estimate.EstimatedImages),
		slog.Bool("email_scrape", estimate.IncludesEmailScrape),
		slog.Float64("total_estimated_cost", estimate.TotalEstimatedCost),
	)

	return estimate, nil
}

// estimatePlaceCount determines how many places will likely be scraped.
// When no max_results cap is set, the estimate is derived from search depth
// using a power-law curve calibrated to real scraper output:
// depth 5 ≈ 40 places, depth 20 ≈ 120 places.
func (s *EstimationService) estimatePlaceCount(jobData *models.JobData) int {
	if jobData.MaxResults > 0 {
		return jobData.MaxResults
	}
	return estimatePlacesFromDepth(jobData.Depth)
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

// estimateReviewCount determines how many reviews will likely be scraped
func (s *EstimationService) estimateReviewCount(jobData *models.JobData, estimatedPlaces int) int {
	reviewsPerPlace := jobData.ReviewsMax
	if reviewsPerPlace <= 0 || reviewsPerPlace >= UnlimitedReviewsThreshold {
		reviewsPerPlace = AvgReviewsPerPlace
	}
	return estimatedPlaces * reviewsPerPlace
}

// estimateImageCount determines how many images will likely be scraped
func (s *EstimationService) estimateImageCount(jobData *models.JobData, estimatedPlaces int) int {
	return estimatedPlaces * AvgImagesPerPlace
}

// generateEstimationNote creates a helpful note explaining the estimate
func (s *EstimationService) generateEstimationNote(jobData *models.JobData) string {
	var notes []string

	if jobData.MaxResults == 0 {
		notes = append(notes, fmt.Sprintf(
			"Estimate based on search depth %d (~%d places). Set a max results cap to control costs precisely.",
			jobData.Depth, estimatePlacesFromDepth(jobData.Depth),
		))
	} else {
		notes = append(notes, "Estimate based on your specified max_results limit.")
	}

	if jobData.ReviewsMax >= UnlimitedReviewsThreshold {
		notes = append(notes, fmt.Sprintf(
			"Note: reviews_max (%d) treated as unlimited - using average of %d reviews per place for estimation.",
			jobData.ReviewsMax, AvgReviewsPerPlace,
		))
	}

	note := notes[0]
	if len(notes) > 1 {
		note = note + " " + notes[1]
	}

	note = note + " Set max_results and reviews_max to control costs precisely."
	return note
}

// CheckSufficientBalance verifies if a user has enough credits for a job.
// The credit_balance is scanned as a string from the database and parsed to
// micro-credits for precise integer comparison, avoiding IEEE 754 errors.
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

	if balanceMicro < estimate.TotalMicro() {
		creditBalance := microToCredits(balanceMicro)
		s.log.Warn("insufficient_credits",
			slog.String("user_id", userID),
			slog.Float64("balance", creditBalance),
			slog.Float64("required", estimate.TotalEstimatedCost),
			slog.Int("estimated_places", estimate.EstimatedPlaces),
		)
		return fmt.Errorf(
			"insufficient credits: you have %.4f credits but this job requires a minimum of %.4f credits to start (estimated cost for %d places). Please purchase more credits to continue",
			creditBalance,
			estimate.TotalEstimatedCost,
			estimate.EstimatedPlaces,
		)
	}

	return nil
}
