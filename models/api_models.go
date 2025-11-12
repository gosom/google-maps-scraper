package models

import "time"

type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// response for scrape create
type ApiScrapeResponse struct {
	ID string `json:"id"`
}

// response for credits balance
type CreditBalanceResponse struct {
	UserID                string `json:"user_id"`
	CreditBalance         string `json:"credit_balance"`
	TotalCreditsPurchased string `json:"total_credits_purchased"`
}

type CheckoutSessionRequest struct {
	Credits  string `json:"credits"`
	Currency string `json:"currency"`
}

// request for reconciling a session
type ReconcileRequest struct {
	SessionID string `json:"session_id"`
}

// represents a single scraped result
type Result struct {
	ID          int       `json:"id"`
	UserID      string    `json:"user_id"`
	JobID       string    `json:"job_id"`
	InputID     string    `json:"input_id"`
	Link        string    `json:"link"`
	Cid         string    `json:"cid"`
	Title       string    `json:"title"`
	Categories  string    `json:"categories"`
	Category    string    `json:"category"`
	Address     string    `json:"address"`
	Website     string    `json:"website"`
	Phone       string    `json:"phone"`
	PlusCode    string    `json:"plus_code"`
	ReviewCount int       `json:"review_count"`
	Rating      float64   `json:"rating"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	Status      string    `json:"status"`
	Description string    `json:"description"`
	ReviewsLink string    `json:"reviews_link"`
	Thumbnail   string    `json:"thumbnail"`
	Timezone    string    `json:"timezone"`
	PriceRange  string    `json:"price_range"`
	DataID      string    `json:"data_id"`
	Emails      string    `json:"emails"`
	CreatedAt   time.Time `json:"created_at"`
}

// epresents a single scraped result with all rich data
type EnhancedResult struct {
	ID                  int                      `json:"id"`
	UserID              string                   `json:"user_id"`
	JobID               string                   `json:"job_id"`
	InputID             string                   `json:"input_id"`
	Link                string                   `json:"link"`
	Cid                 string                   `json:"cid"`
	Title               string                   `json:"title"`
	Categories          string                   `json:"categories"`
	Category            string                   `json:"category"`
	Address             string                   `json:"address"`
	OpenHours           map[string][]string      `json:"open_hours,omitempty"`
	PopularTimes        map[string]map[int]int   `json:"popular_times,omitempty"`
	Website             string                   `json:"website"`
	Phone               string                   `json:"phone"`
	PlusCode            string                   `json:"plus_code"`
	ReviewCount         int                      `json:"review_count"`
	Rating              float64                  `json:"rating"`
	ReviewsPerRating    map[int]int              `json:"reviews_per_rating,omitempty"`
	Latitude            float64                  `json:"latitude"`
	Longitude           float64                  `json:"longitude"`
	Status              string                   `json:"status"`
	Description         string                   `json:"description"`
	ReviewsLink         string                   `json:"reviews_link"`
	Thumbnail           string                   `json:"thumbnail"`
	Timezone            string                   `json:"timezone"`
	PriceRange          string                   `json:"price_range"`
	DataID              string                   `json:"data_id"`
	Images              []map[string]interface{} `json:"images,omitempty"`
	Reservations        []map[string]interface{} `json:"reservations,omitempty"`
	OrderOnline         []map[string]interface{} `json:"order_online,omitempty"`
	Menu                map[string]interface{}   `json:"menu,omitempty"`
	Owner               map[string]interface{}   `json:"owner,omitempty"`
	CompleteAddress     map[string]interface{}   `json:"complete_address,omitempty"`
	About               []map[string]interface{} `json:"about,omitempty"`
	UserReviews         []map[string]interface{} `json:"user_reviews,omitempty"`
	UserReviewsExtended []map[string]interface{} `json:"user_reviews_extended,omitempty"`
	Emails              string                   `json:"emails"`
	CreatedAt           time.Time                `json:"created_at"`
}

// a paginated response for job results
type PaginatedResultsResponse struct {
	Results    []EnhancedResult `json:"results"`
	TotalCount int              `json:"total_count"`
	Page       int              `json:"page"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
	TotalPages int              `json:"total_pages"`
	HasNext    bool             `json:"has_next"`
	HasPrev    bool             `json:"has_prev"`
}

// JobCostBreakdownItem represents per-event cost aggregation for a job.
type JobCostBreakdownItem struct {
	EventType   string `json:"event_type"`
	Quantity    int64  `json:"quantity"`
	CostCredits string `json:"cost_credits"`
}

// JobCostResponse is the API payload for job cost details and totals.
type JobCostResponse struct {
	JobID        string                 `json:"job_id"`
	Items        []JobCostBreakdownItem `json:"items"`
	TotalCredits string                 `json:"total_credits"`
	TotalRounded int                    `json:"total_rounded"`
}
