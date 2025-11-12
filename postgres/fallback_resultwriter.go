package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/gosom/scrapemate"
	"log"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
)

// NewFallbackResultWriter creates a result writer that inserts without conflict resolution
// Use this if the enhanced writer with ON CONFLICT is causing issues
func NewFallbackResultWriter(db *sql.DB, userID, jobID string) scrapemate.ResultWriter {
	return &fallbackResultWriter{
		db:     db,
		userID: userID,
		jobID:  jobID,
	}
}

type fallbackResultWriter struct {
	db     *sql.DB
	userID string
	jobID  string
}

func (r *fallbackResultWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	const maxBatchSize = 50

	buff := make([]*gmaps.Entry, 0, 50)
	lastSave := time.Now().UTC()

	// Use background context for database operations to avoid cancellation issues
	dbCtx := context.Background()

	for result := range in {
		entry, ok := result.Data.(*gmaps.Entry)

		if !ok {
			return fmt.Errorf("invalid data type")
		}

		buff = append(buff, entry)

		if len(buff) >= maxBatchSize || time.Now().UTC().Sub(lastSave) >= time.Minute {
			err := r.batchSaveFallback(dbCtx, buff)
			if err != nil {
				log.Printf("Error saving batch: %v", err)
				// Continue processing instead of failing completely
			}

			buff = buff[:0]
			lastSave = time.Now().UTC()
		}
	}

	if len(buff) > 0 {
		err := r.batchSaveFallback(dbCtx, buff)
		if err != nil {
			log.Printf("Error saving final batch: %v", err)
		}
	}

	return nil
}

func (r *fallbackResultWriter) batchSaveFallback(ctx context.Context, entries []*gmaps.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	log.Printf("[Fallback Writer] Attempting to insert %d entries for user %s, job %s", len(entries), r.userID, r.jobID)

	// Insert entries one by one to handle duplicates gracefully
	successCount := 0
	for _, entry := range entries {
		err := r.insertSingleEntry(ctx, entry)
		if err != nil {
			log.Printf("[Fallback Writer] Failed to insert entry %s: %v", entry.Title, err)
			// Continue with next entry
		} else {
			successCount++
		}
	}

	log.Printf("[Fallback Writer] Successfully inserted %d/%d entries for user %s, job %s", successCount, len(entries), r.userID, r.jobID)
	return nil
}

func (r *fallbackResultWriter) insertSingleEntry(ctx context.Context, entry *gmaps.Entry) error {
	// Use a timeout context to prevent hanging but avoid immediate cancellation
	dbCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check if entry with this cid already exists
	if entry.Cid != "" {
		var count int
		err := r.db.QueryRowContext(dbCtx, "SELECT COUNT(*) FROM results WHERE cid = $1", entry.Cid).Scan(&count)
		if err == nil && count > 0 {
			// Entry already exists, skip
			return nil
		}
	}

	// Serialize JSON fields
	openHoursJSON, _ := json.Marshal(entry.OpenHours)
	popularTimesJSON, _ := json.Marshal(entry.PopularTimes)
	reviewsPerRatingJSON, _ := json.Marshal(entry.ReviewsPerRating)
	imagesJSON, _ := json.Marshal(entry.Images)
	reservationsJSON, _ := json.Marshal(entry.Reservations)
	orderOnlineJSON, _ := json.Marshal(entry.OrderOnline)
	menuJSON, _ := json.Marshal(entry.Menu)
	ownerJSON, _ := json.Marshal(entry.Owner)
	completeAddressJSON, _ := json.Marshal(entry.CompleteAddress)
	aboutJSON, _ := json.Marshal(entry.About)
	userReviewsJSON, _ := json.Marshal(entry.UserReviews)
	userReviewsExtendedJSON, _ := json.Marshal(entry.UserReviewsExtended)
	dataJSON, _ := json.Marshal(entry)

	// Convert slices to strings
	categoriesStr := strings.Join(entry.Categories, ", ")
	emailsStr := strings.Join(entry.Emails, ", ")

	query := `INSERT INTO results (
		user_id, job_id, input_id, link, cid, title, categories, category, address,
		openhours, popular_times, website, phone, pluscode, review_count, rating,
		reviews_per_rating, latitude, longitude, status_info, description,
		reviews_link, thumbnail, timezone, price_range, data_id, images,
		reservations, order_online, menu, owner, complete_address, about,
		user_reviews, user_reviews_extended, emails, data, created_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
		$17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
		$31, $32, $33, $34, $35, $36, $37, $38
	)`

	_, err := r.db.ExecContext(dbCtx, query,
		r.userID,                // $1 user_id
		r.jobID,                 // $2 job_id
		entry.ID,                // $3 input_id
		entry.Link,              // $4 link
		entry.Cid,               // $5 cid
		entry.Title,             // $6 title
		categoriesStr,           // $7 categories
		entry.Category,          // $8 category
		entry.Address,           // $9 address
		openHoursJSON,           // $10 openhours
		popularTimesJSON,        // $11 popular_times
		entry.WebSite,           // $12 website
		entry.Phone,             // $13 phone
		entry.PlusCode,          // $14 pluscode
		entry.ReviewCount,       // $15 review_count
		entry.ReviewRating,      // $16 rating
		reviewsPerRatingJSON,    // $17 reviews_per_rating
		entry.Latitude,          // $18 latitude
		entry.Longtitude,        // $19 longitude
		entry.Status,            // $20 status_info
		entry.Description,       // $21 description
		entry.ReviewsLink,       // $22 reviews_link
		entry.Thumbnail,         // $23 thumbnail
		entry.Timezone,          // $24 timezone
		entry.PriceRange,        // $25 price_range
		entry.DataID,            // $26 data_id
		imagesJSON,              // $27 images
		reservationsJSON,        // $28 reservations
		orderOnlineJSON,         // $29 order_online
		menuJSON,                // $30 menu
		ownerJSON,               // $31 owner
		completeAddressJSON,     // $32 complete_address
		aboutJSON,               // $33 about
		userReviewsJSON,         // $34 user_reviews
		userReviewsExtendedJSON, // $35 user_reviews_extended
		emailsStr,               // $36 emails
		dataJSON,                // $37 data
		time.Now(),              // $38 created_at
	)

	return err
}
