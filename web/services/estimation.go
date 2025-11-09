package services

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/gosom/google-maps-scraper/models"
)

// EstimationService provides job cost estimation functionality
type EstimationService struct {
	db *sql.DB
}

// Cost estimation constants - average values based on typical Google Maps data
const (
	// Average number of reviews per place (Google Maps businesses typically have 10-50 reviews)
	AvgReviewsPerPlace = 25

	// Average number of images per place (typical business has 5-15 images)
	AvgImagesPerPlace = 30

	// Default max results when user specifies 0 (unlimited) - use conservative estimate
	DefaultEstimateForUnlimited = 50

	// Threshold for detecting "unlimited" reviews_max (treat values >= this as unlimited)
	// Frontend may send 9999 or 10000 to simulate unlimited
	UnlimitedReviewsThreshold = 1000

	// Pricing from billing_event_types and pricing_rules (migration 000017)
	PriceActorStart        = 0.007  // Flat fee per job
	PricePlaceScraped      = 0.004  // Per place
	PriceFiltersApplied    = 0.001  // Per filter per place (not yet implemented)
	PriceAdditionalDetails = 0.002  // Per place with extra details (not yet implemented)
	PriceContactDetails    = 0.002  // Per place when email scraping enabled
	PriceReview            = 0.0005 // Per review
	PriceImage             = 0.0005 // Per image
)

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

func NewEstimationService(db *sql.DB) *EstimationService {
	return &EstimationService{db: db}
}

// EstimateJobCost calculates the estimated cost for a job based on its parameters
// Uses conservative estimates when user doesn't specify limits
func (s *EstimationService) EstimateJobCost(ctx context.Context, jobData *models.JobData) (*CostEstimate, error) {
	estimate := &CostEstimate{}

	// 1. Actor start cost (flat fee per job)
	estimate.ActorStartCost = PriceActorStart

	// 2. Determine estimated number of places
	estimatedPlaces := s.estimatePlaceCount(jobData)
	estimate.EstimatedPlaces = estimatedPlaces

	// 3. Calculate places cost
	estimate.PlacesCost = float64(estimatedPlaces) * PricePlaceScraped

	// 4. Calculate contact details cost (if email scraping is enabled)
	if jobData.Email {
		estimate.IncludesEmailScrape = true
		estimate.ContactDetailsCost = float64(estimatedPlaces) * PriceContactDetails
	}

	// 5. Calculate reviews cost (ONLY if reviews are explicitly requested)
	// Note: ReviewsMax = 0 means no reviews, NOT unlimited
	if jobData.ReviewsMax > 0 {
		estimatedReviews := s.estimateReviewCount(jobData, estimatedPlaces)
		estimate.EstimatedReviews = estimatedReviews
		estimate.ReviewsCost = float64(estimatedReviews) * PriceReview
	}

	// 6. Calculate images cost (if images are requested)
	if jobData.Images {
		estimatedImages := s.estimateImageCount(jobData, estimatedPlaces)
		estimate.EstimatedImages = estimatedImages
		estimate.ImagesCost = float64(estimatedImages) * PriceImage
	}

	// 7. Calculate total
	estimate.TotalEstimatedCost = estimate.ActorStartCost +
		estimate.PlacesCost +
		estimate.ContactDetailsCost +
		estimate.ReviewsCost +
		estimate.ImagesCost

	// 8. Add note about estimation
	estimate.Note = s.generateEstimationNote(jobData)

	return estimate, nil
}

// estimatePlaceCount determines how many places will likely be scraped
func (s *EstimationService) estimatePlaceCount(jobData *models.JobData) int {
	// IMPORTANT: max_results = 0 means UNLIMITED scraping, not zero results!
	// We must handle this specially to avoid underestimating costs

	if jobData.MaxResults > 0 {
		// User specified a limit, use that exact value
		return jobData.MaxResults
	}

	// max_results = 0 or not specified means UNLIMITED scraping
	// Use a conservative estimate to warn user about minimum expected cost
	// This is a MINIMUM - actual cost could be much higher!
	return DefaultEstimateForUnlimited
}

// estimateReviewCount determines how many reviews will likely be scraped
func (s *EstimationService) estimateReviewCount(jobData *models.JobData, estimatedPlaces int) int {
	reviewsPerPlace := jobData.ReviewsMax

	// Treat unrealistically high values (>=1000) as "unlimited" and use average
	// Frontend may send 9999 or 10000 to simulate unlimited reviews
	if reviewsPerPlace <= 0 || reviewsPerPlace >= UnlimitedReviewsThreshold {
		reviewsPerPlace = AvgReviewsPerPlace
	}

	return estimatedPlaces * reviewsPerPlace
}

// estimateImageCount determines how many images will likely be scraped
func (s *EstimationService) estimateImageCount(jobData *models.JobData, estimatedPlaces int) int {
	// Google Maps typically returns 5-15 images per place
	// Use conservative average
	return estimatedPlaces * AvgImagesPerPlace
}

// generateEstimationNote creates a helpful note explaining the estimate
func (s *EstimationService) generateEstimationNote(jobData *models.JobData) string {
	var notes []string

	// Check if max_results is unlimited
	if jobData.MaxResults == 0 {
		notes = append(notes, fmt.Sprintf(
			"WARNING: max_results is set to unlimited (0). This estimate is for a MINIMUM of %d places. "+
				"Actual cost could be significantly higher.",
			DefaultEstimateForUnlimited,
		))
	} else {
		notes = append(notes, "Estimate based on your specified max_results limit")
	}

	// Check if reviews_max is treated as unlimited
	if jobData.ReviewsMax >= UnlimitedReviewsThreshold {
		notes = append(notes, fmt.Sprintf(
			"Note: reviews_max (%d) treated as unlimited - using average of %d reviews per place for estimation.",
			jobData.ReviewsMax, AvgReviewsPerPlace,
		))
	}

	// Combine notes
	note := notes[0]
	if len(notes) > 1 {
		note = note + " " + notes[1]
	}

	note = note + " Set max_results and reviews_max to control costs precisely."
	return note
}

// CheckSufficientBalance verifies if a user has enough credits for a job
// Returns an error if balance is insufficient
func (s *EstimationService) CheckSufficientBalance(ctx context.Context, userID string, estimate *CostEstimate) error {
	if s.db == nil {
		return fmt.Errorf("database not available")
	}

	// Get user's current credit balance
	var creditBalance float64
	const query = `SELECT COALESCE(credit_balance, 0) FROM users WHERE id = $1`
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&creditBalance)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("user not found")
		}
		return fmt.Errorf("failed to retrieve credit balance: %w", err)
	}

	// Check if balance is sufficient
	if creditBalance < estimate.TotalEstimatedCost {
		return fmt.Errorf(
			"insufficient credits: you have %.4f credits but this job requires a minimum of %.4f credits to start (estimated cost for %d places). Please purchase more credits to continue",
			creditBalance,
			estimate.TotalEstimatedCost,
			estimate.EstimatedPlaces,
		)
	}

	return nil
}
