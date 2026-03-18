package services

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
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
	DefaultEstimateForUnlimited = 50
	UnlimitedReviewsThreshold   = 1000

	// Default pricing fallbacks (used when DB has no active rules)
	defaultPriceActorStart        = 0.007
	defaultPricePlaceScraped      = 0.004
	defaultPriceFiltersApplied    = 0.001
	defaultPriceAdditionalDetails = 0.002
	defaultPriceContactDetails    = 0.002
	defaultPriceReview            = 0.0005
	defaultPriceImage             = 0.0005

	priceCacheTTL = 60 * time.Second
)

// Package-level pricing cache shared across per-request EstimationService instances.
var (
	priceCacheMu   sync.RWMutex
	priceCacheData map[string]float64
	priceCacheTime time.Time
)

var defaultPrices = map[string]float64{
	"actor_start":              defaultPriceActorStart,
	"place_scraped":            defaultPricePlaceScraped,
	"filters_applied":          defaultPriceFiltersApplied,
	"additional_place_details": defaultPriceAdditionalDetails,
	"contact_details":          defaultPriceContactDetails,
	"review":                   defaultPriceReview,
	"image":                    defaultPriceImage,
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
}

func NewEstimationService(db *sql.DB, priceRepo models.PricingRuleRepository) *EstimationService {
	return &EstimationService{
		db:        db,
		log:       pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "estimation"),
		priceRepo: priceRepo,
	}
}

// getPrice returns the price for an event type, reading from a cached copy of the
// pricing_rules table. Falls back to hardcoded defaults on DB error or missing rule.
func (s *EstimationService) getPrice(ctx context.Context, eventType string) float64 {
	prices := s.loadPrices(ctx)
	if p, ok := prices[eventType]; ok {
		return p
	}
	if p, ok := defaultPrices[eventType]; ok {
		return p
	}
	return 0
}

// loadPrices returns the cached pricing map, refreshing from DB if stale.
func (s *EstimationService) loadPrices(ctx context.Context) map[string]float64 {
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
		return defaultPrices
	}

	prices, err := s.priceRepo.GetActiveDefaultPrices(ctx)
	if err != nil {
		s.log.Warn("failed to load pricing rules from DB, using defaults", slog.String("error", err.Error()))
		return defaultPrices
	}
	if len(prices) == 0 {
		s.log.Warn("no active pricing rules found in DB, using defaults")
		return defaultPrices
	}

	priceCacheData = prices
	priceCacheTime = time.Now()
	s.log.Debug("pricing rules refreshed from DB", slog.Int("count", len(prices)))
	return priceCacheData
}

// EstimateJobCost calculates the estimated cost for a job based on its parameters.
func (s *EstimationService) EstimateJobCost(ctx context.Context, jobData *models.JobData) (*CostEstimate, error) {
	estimate := &CostEstimate{}

	priceActorStart := s.getPrice(ctx, "actor_start")
	pricePlaceScraped := s.getPrice(ctx, "place_scraped")
	priceContactDetails := s.getPrice(ctx, "contact_details")
	priceReview := s.getPrice(ctx, "review")
	priceImage := s.getPrice(ctx, "image")

	// 1. Actor start cost (flat fee per job)
	estimate.ActorStartCost = priceActorStart

	// 2. Determine estimated number of places
	estimatedPlaces := s.estimatePlaceCount(jobData)
	estimate.EstimatedPlaces = estimatedPlaces

	// 3. Calculate places cost
	estimate.PlacesCost = float64(estimatedPlaces) * pricePlaceScraped

	// 4. Calculate contact details cost (if email scraping is enabled)
	if jobData.Email {
		estimate.IncludesEmailScrape = true
		estimate.ContactDetailsCost = float64(estimatedPlaces) * priceContactDetails
	}

	// 5. Calculate reviews cost (ONLY if reviews are explicitly requested)
	if jobData.ReviewsMax > 0 {
		estimatedReviews := s.estimateReviewCount(jobData, estimatedPlaces)
		estimate.EstimatedReviews = estimatedReviews
		estimate.ReviewsCost = float64(estimatedReviews) * priceReview
	}

	// 6. Calculate images cost (if images are requested)
	if jobData.Images {
		estimatedImages := s.estimateImageCount(jobData, estimatedPlaces)
		estimate.EstimatedImages = estimatedImages
		estimate.ImagesCost = float64(estimatedImages) * priceImage
	}

	// 7. Calculate total
	estimate.TotalEstimatedCost = estimate.ActorStartCost +
		estimate.PlacesCost +
		estimate.ContactDetailsCost +
		estimate.ReviewsCost +
		estimate.ImagesCost

	// 8. Add note about estimation
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

// estimatePlaceCount determines how many places will likely be scraped
func (s *EstimationService) estimatePlaceCount(jobData *models.JobData) int {
	if jobData.MaxResults > 0 {
		return jobData.MaxResults
	}
	return DefaultEstimateForUnlimited
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
			"WARNING: max_results is set to unlimited (0). This estimate is for a MINIMUM of %d places. "+
				"Actual cost could be significantly higher.",
			DefaultEstimateForUnlimited,
		))
	} else {
		notes = append(notes, "Estimate based on your specified max_results limit")
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

// CheckSufficientBalance verifies if a user has enough credits for a job
func (s *EstimationService) CheckSufficientBalance(ctx context.Context, userID string, estimate *CostEstimate) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	var creditBalance float64
	const query = `SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1`
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&creditBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("user not found")
		}
		return fmt.Errorf("failed to retrieve credit balance: %w", err)
	}

	if creditBalance < estimate.TotalEstimatedCost {
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
