// Package plugins provides common types and interfaces for streaming duplicate filter plugin.
package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

const (
	DefaultReviewLimit = 10 // Default limit for review filtering
)

// StreamEvent represents a structured event emitted during the scraping process.
type StreamEvent struct {
	Type      string                 `json:"type"`
	Timestamp time.Time              `json:"timestamp"`
	JobID     string                 `json:"job_id"`
	Data      map[string]interface{} `json:"data"`
}

// ProcessingStats tracks statistics for the current scraping session.
type ProcessingStats struct {
	TotalProcessed  int `json:"total_processed"`
	NewCompanies    int `json:"new_companies"`
	DuplicatesFound int `json:"duplicates_found"`
	Errors          int `json:"errors"`
}

// StreamingDuplicateFilter defines the interface for the streaming duplicate filter.
type StreamingDuplicateFilter interface {
	scrapemate.ResultWriter
	SetExistingCIDs(cids []string)
	SetReviewLimit(limit int)
	GetEventChannel() <-chan StreamEvent
	GetStats() ProcessingStats
}

// StreamingDuplicateFilterWriter is the internal implementation of the plugin.
type StreamingDuplicateFilterWriter struct {
	existingCIDs      map[string]bool
	eventChan         chan StreamEvent
	stats             *ProcessingStats
	mu                *sync.RWMutex
	currentJobID      string
	closed            int32 // atomic
	batchStartEmitted bool
	reviewLimit       int
}

// NewStreamingDuplicateFilterWriter creates a new instance of the streaming duplicate filter.
func NewStreamingDuplicateFilterWriter() *StreamingDuplicateFilterWriter {
	return &StreamingDuplicateFilterWriter{
		existingCIDs: make(map[string]bool),
		eventChan:    make(chan StreamEvent, 100),
		stats:        &ProcessingStats{},
		mu:           &sync.RWMutex{},
		reviewLimit:  DefaultReviewLimit,
	}
}

// Run implements the scrapemate.ResultWriter interface.
// It processes results from the scraping channel and emits real-time events.
func (w *StreamingDuplicateFilterWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	log.Println("🚀 Starting streaming duplicate filter plugin")

	defer func() {
		w.printFinalStats()
		w.Close()
	}()

	// Initialize existing CIDs from job data if available
	w.initializeExistingCIDs(ctx, in)

	for {
		select {
		case result, ok := <-in:
			if !ok {
				w.emitBatchEndEvent()
				log.Println("✅ Channel closed, plugin finished")

				return nil
			}

			if err := w.processResult(ctx, result); err != nil {
				log.Printf("❌ Error processing result: %v", err)
				w.incrementErrorCount()
			}

		case <-ctx.Done():
			log.Println("🛑 Context cancelled, stopping plugin")
			w.emitBatchEndEvent()

			return ctx.Err()
		}
	}
}

// initializeExistingCIDs extracts existing CIDs from the job data and sets up the filter.
func (w *StreamingDuplicateFilterWriter) initializeExistingCIDs(_ context.Context, _ <-chan scrapemate.Result) {
	// In a real implementation, we would extract job data from the first result
	// For now, we'll initialize with an empty set and add CIDs as needed
	log.Println("📋 Initializing duplicate filter")
}

// processResult processes a single scraping result and emits appropriate events.
func (w *StreamingDuplicateFilterWriter) processResult(_ context.Context, result scrapemate.Result) error {
	// Extract job information
	job := result.Job
	w.currentJobID = job.GetID()

	// Emit BATCH_START on first result
	if !w.batchStartEmitted {
		w.batchStartEmitted = true
		// Extract keyword from job URL if available
		keyword := ""

		if job.GetURL() != "" {
			// Try to extract search query from URL parameters
			if idx := strings.Index(job.GetURL(), "q:"); idx != -1 {
				endIdx := strings.Index(job.GetURL()[idx:], " ")
				if endIdx != -1 {
					keyword = job.GetURL()[idx+2 : idx+endIdx]
				} else {
					keyword = job.GetURL()[idx+2:]
				}
			}
		}

		w.emitBatchStartEvent(keyword)
	}

	// Convert result data to business entries
	entries, err := w.convertToBusinessEntries(result.Data)
	if err != nil {
		return fmt.Errorf("failed to convert result data: %w", err)
	}

	// Process each business entry
	for _, entry := range entries {
		w.processBusinessEntry(entry)
	}

	return nil
}

// processBusinessEntry processes a single business entry and checks for duplicates.
func (w *StreamingDuplicateFilterWriter) processBusinessEntry(entry *gmaps.Entry) {
	w.mu.Lock()
	w.stats.TotalProcessed++
	w.mu.Unlock()

	// Check if this business is a duplicate
	isDuplicate, reason := w.isDuplicateBusiness(entry)

	if isDuplicate {
		w.incrementDuplicateCount()
		w.emitCompanyScrapedEvent(entry, "duplicate", reason)
		log.Printf("🔄 Duplicate found: %s (reason: %s)", entry.Title, reason)

		return
	}

	// This is a new business
	w.incrementNewCompanyCount()
	w.addCIDToFilter(entry.Cid)
	w.emitCompanyScrapedEvent(entry, "new", "")
	log.Printf("✨ New business found: %s (CID: %s)", entry.Title, entry.Cid)
}

// convertToBusinessEntries converts scrapemate result data to gmaps.Entry slice.
func (w *StreamingDuplicateFilterWriter) convertToBusinessEntries(data interface{}) ([]*gmaps.Entry, error) {
	switch v := data.(type) {
	case []*gmaps.Entry:
		return v, nil
	case *gmaps.Entry:
		return []*gmaps.Entry{v}, nil
	case []interface{}:
		entries := make([]*gmaps.Entry, 0, len(v))

		for _, item := range v {
			if entry, ok := item.(*gmaps.Entry); ok {
				entries = append(entries, entry)
			}
		}

		return entries, nil
	default:
		return nil, fmt.Errorf("unsupported data type: %T", data)
	}
}

// isDuplicateBusiness checks if a business is a duplicate based on multiple criteria.
func (w *StreamingDuplicateFilterWriter) isDuplicateBusiness(entry *gmaps.Entry) (isDuplicate bool, reason string) {
	// Check by CID (most reliable)
	if entry.Cid != "" && w.cidExists(entry.Cid) {
		return true, "cid_match"
	}

	// Check by title and address combination (fallback)
	if entry.Title != "" && entry.Address != "" {
		key := w.generateBusinessKey(entry.Title, entry.Address)
		if w.businessKeyExists(key) {
			return true, "address_match"
		}
	}

	// Check by phone number (if available)
	if entry.Phone != "" && w.phoneExists(entry.Phone) {
		return true, "phone_match"
	}

	return false, ""
}

// cidExists checks if a CID already exists in the filter.
func (w *StreamingDuplicateFilterWriter) cidExists(cid string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.existingCIDs[cid]
}

// addCIDToFilter adds a new CID to the duplicate filter.
func (w *StreamingDuplicateFilterWriter) addCIDToFilter(cid string) {
	if cid == "" {
		return
	}

	w.mu.Lock()
	w.existingCIDs[cid] = true
	w.mu.Unlock()
}

// generateBusinessKey creates a normalized key for business identification.
func (w *StreamingDuplicateFilterWriter) generateBusinessKey(title, address string) string {
	normalizedTitle := strings.TrimSpace(strings.ToLower(title))
	normalizedAddress := strings.TrimSpace(strings.ToLower(address))

	return fmt.Sprintf("%s|%s", normalizedTitle, normalizedAddress)
}

// businessKeyExists checks if a business key already exists (placeholder implementation).
func (w *StreamingDuplicateFilterWriter) businessKeyExists(_ string) bool {
	// In a real implementation, this would check against a database or cache
	// For now, we'll return false to allow all business key matches
	return false
}

// phoneExists checks if a phone number already exists (placeholder implementation).
func (w *StreamingDuplicateFilterWriter) phoneExists(_ string) bool {
	// In a real implementation, this would check against a database or cache
	// For now, we'll return false to allow all phone matches
	return false
}

// Event emission methods

// emitBatchStartEvent emits a BATCH_START event.
func (w *StreamingDuplicateFilterWriter) emitBatchStartEvent(keyword string) {
	event := StreamEvent{
		Type:      "BATCH_START",
		Timestamp: time.Now().UTC(),
		JobID:     w.currentJobID,
		Data: map[string]interface{}{
			"keyword":        keyword,
			"total_expected": nil,
		},
	}
	w.emitEvent(event)
}

// emitCompanyScrapedEvent emits a COMPANY_SCRAPED event.
func (w *StreamingDuplicateFilterWriter) emitCompanyScrapedEvent(entry *gmaps.Entry, status, duplicateReason string) {
	event := StreamEvent{
		Type:      "COMPANY_SCRAPED",
		Timestamp: time.Now().UTC(),
		JobID:     w.currentJobID,
		Data: map[string]interface{}{
			// Core identification
			"cid":              entry.Cid,
			"title":            entry.Title,
			"status":           status,
			"duplicate_reason": duplicateReason,

			// Contact information
			"address": entry.Address,
			"phone":   entry.Phone,
			"website": entry.WebSite,
			"emails":  entry.Emails,

			// Business details
			"categories":  entry.Categories,
			"category":    entry.Category,
			"description": entry.Description,
			"price_range": entry.PriceRange,

			// Location data
			"latitude":  entry.Latitude,
			"longitude": entry.Longtitude,
			"plus_code": entry.PlusCode,

			// Reviews and ratings
			"review_count":       entry.ReviewCount,
			"review_rating":      entry.ReviewRating,
			"reviews_per_rating": entry.ReviewsPerRating,
			"reviews": func() interface{} {
				// Use extended reviews if available, otherwise basic reviews
				var reviews []gmaps.Review
				if len(entry.UserReviewsExtended) > 0 {
					reviews = entry.UserReviewsExtended
				} else {
					reviews = entry.UserReviews
				}
				// Apply filtering and sorting
				return w.filterAndSortReviews(reviews)
			}(),

			// Operational data
			"open_hours":      entry.OpenHours,
			"popular_times":   entry.PopularTimes,
			"business_status": entry.Status,

			// Media and links
			"thumbnail":    entry.Thumbnail,
			"images":       entry.Images,
			"reviews_link": entry.ReviewsLink,

			// Additional data
			"owner":            entry.Owner,
			"complete_address": entry.CompleteAddress,
			"about":            entry.About,
			"timezone":         entry.Timezone,
			"data_id":          entry.DataID,
			"link":             entry.Link,
			"reservations":     entry.Reservations,
			"order_online":     entry.OrderOnline,
			"menu":             entry.Menu,
		},
	}
	w.emitEvent(event)
}

// emitBatchEndEvent emits a BATCH_END event with final statistics.
func (w *StreamingDuplicateFilterWriter) emitBatchEndEvent() {
	log.Println("📤 Emitting BATCH_END event")
	w.mu.RLock()
	stats := *w.stats
	w.mu.RUnlock()

	event := StreamEvent{
		Type:      "BATCH_END",
		Timestamp: time.Now().UTC(),
		JobID:     w.currentJobID,
		Data: map[string]interface{}{
			"keyword":          "",
			"total_scraped":    stats.TotalProcessed,
			"new_companies":    stats.NewCompanies,
			"duplicates_found": stats.DuplicatesFound,
			"errors":           stats.Errors,
		},
	}
	w.emitEvent(event)
}

// emitEvent sends an event to the event channel.
func (w *StreamingDuplicateFilterWriter) emitEvent(event StreamEvent) {
	// Send to internal channel for compatibility
	select {
	case w.eventChan <- event:
		// Event sent successfully
	default:
		log.Printf("⚠️ Event channel full, dropping event: %s", event.Type)
	}
}

// Close safely closes the event channel using atomic operations
func (w *StreamingDuplicateFilterWriter) Close() {
	if atomic.CompareAndSwapInt32(&w.closed, 0, 1) {
		close(w.eventChan)
	}
}

// GetEventChannel returns the event channel for external consumption.
func (w *StreamingDuplicateFilterWriter) GetEventChannel() <-chan StreamEvent {
	return w.eventChan
}

// Statistics methods

// incrementNewCompanyCount safely increments the new company counter.
func (w *StreamingDuplicateFilterWriter) incrementNewCompanyCount() {
	w.mu.Lock()
	w.stats.NewCompanies++
	w.mu.Unlock()
}

// incrementDuplicateCount safely increments the duplicate counter.
func (w *StreamingDuplicateFilterWriter) incrementDuplicateCount() {
	w.mu.Lock()
	w.stats.DuplicatesFound++
	w.mu.Unlock()
}

// incrementErrorCount safely increments the error counter.
func (w *StreamingDuplicateFilterWriter) incrementErrorCount() {
	w.mu.Lock()
	w.stats.Errors++
	w.mu.Unlock()
}

// printFinalStats logs the final processing statistics.
func (w *StreamingDuplicateFilterWriter) printFinalStats() {
	w.mu.RLock()
	stats := *w.stats
	w.mu.RUnlock()

	log.Println("\n📊 Final Processing Statistics:")
	log.Printf("   Total Processed: %d", stats.TotalProcessed)
	log.Printf("   New Companies: %d", stats.NewCompanies)
	log.Printf("   Duplicates Found: %d", stats.DuplicatesFound)
	log.Printf("   Errors: %d", stats.Errors)

	if stats.TotalProcessed > 0 {
		duplicateRate := float64(stats.DuplicatesFound) / float64(stats.TotalProcessed) * 100
		log.Printf("   Duplicate Rate: %.1f%%", duplicateRate)
	}
}

// GetStats returns a copy of the current processing statistics.
func (w *StreamingDuplicateFilterWriter) GetStats() ProcessingStats {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return *w.stats
}

// SetExistingCIDs initializes the plugin with a list of existing CIDs to filter out.
func (w *StreamingDuplicateFilterWriter) SetExistingCIDs(cids []string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.existingCIDs = make(map[string]bool, len(cids))

	for _, cid := range cids {
		if cid != "" {
			w.existingCIDs[cid] = true
		}
	}

	log.Printf("📋 Initialized duplicate filter with %d existing CIDs", len(w.existingCIDs))
}

// filterAndSortReviews filters reviews by date and limits to the configured amount.
func (w *StreamingDuplicateFilterWriter) filterAndSortReviews(reviews []gmaps.Review) []gmaps.Review {
	if len(reviews) == 0 {
		return reviews
	}

	// Parse and filter valid reviews with dates
	type reviewWithTime struct {
		review gmaps.Review
		time   time.Time
	}

	validReviews := make([]reviewWithTime, 0, len(reviews))

	for _, review := range reviews {
		if review.When == "" {
			continue // Skip reviews with empty dates
		}

		// Parse date components from format "2024-1-15"
		parts := strings.Split(review.When, "-")
		if len(parts) != 3 {
			continue // Skip malformed dates
		}

		year, err1 := strconv.Atoi(parts[0])
		month, err2 := strconv.Atoi(parts[1])
		day, err3 := strconv.Atoi(parts[2])

		if err1 != nil || err2 != nil || err3 != nil {
			continue // Skip invalid date components
		}

		// Create time.Time object and format to ISO 8601
		reviewTime := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)

		// Update review with ISO 8601 format
		review.When = reviewTime.Format("2006-01-02")
		validReviews = append(validReviews, reviewWithTime{
			review: review,
			time:   reviewTime,
		})
	}

	// Sort by date descending (newest first)
	sort.Slice(validReviews, func(i, j int) bool {
		return validReviews[i].time.After(validReviews[j].time)
	})

	// Apply review limit
	w.mu.RLock()
	limit := w.reviewLimit
	w.mu.RUnlock()

	if len(validReviews) > limit {
		validReviews = validReviews[:limit]
	}

	// Extract sorted and limited reviews
	result := make([]gmaps.Review, len(validReviews))
	for i, rv := range validReviews {
		result[i] = rv.review
	}

	return result
}

// SetReviewLimit configures the maximum number of reviews to return per business.
func (w *StreamingDuplicateFilterWriter) SetReviewLimit(limit int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.reviewLimit = limit
	log.Printf("📊 Set review limit to %d reviews per business", limit)
}

// MarshalEventJSON converts a StreamEvent to JSON bytes.
func MarshalEventJSON(event StreamEvent) ([]byte, error) {
	return json.Marshal(event)
}
