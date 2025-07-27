package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
)

// NewResultWriter creates a basic result writer that only saves to the data column
func NewResultWriter(db *sql.DB) scrapemate.ResultWriter {
	return &resultWriter{db: db}
}

// NewEnhancedResultWriter creates an enhanced result writer that saves all fields
// and associates results with user and job
func NewEnhancedResultWriter(db *sql.DB, userID, jobID string) scrapemate.ResultWriter {
	return &enhancedResultWriter{
		db:     db,
		userID: userID,
		jobID:  jobID,
	}
}

// NewEnhancedResultWriterWithExiter creates an enhanced result writer that saves all fields,
// associates results with user and job, and reports results to the exiter
func NewEnhancedResultWriterWithExiter(db *sql.DB, userID, jobID string, exiter exiter.Exiter) scrapemate.ResultWriter {
	return &enhancedResultWriterWithExiter{
		db:          db,
		userID:      userID,
		jobID:       jobID,
		exitMonitor: exiter,
	}
}

type resultWriter struct {
	db *sql.DB
}

type enhancedResultWriter struct {
	db     *sql.DB
	userID string
	jobID  string
}

type enhancedResultWriterWithExiter struct {
	db          *sql.DB
	userID      string
	jobID       string
	exitMonitor exiter.Exiter
}

func (r *resultWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	const maxBatchSize = 50

	buff := make([]*gmaps.Entry, 0, 50)
	lastSave := time.Now().UTC()

	// Use the provided context for cancellation support
	for result := range in {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		entry, ok := result.Data.(*gmaps.Entry)

		if !ok {
			return errors.New("invalid data type")
		}

		buff = append(buff, entry)

		if len(buff) >= maxBatchSize || time.Now().UTC().Sub(lastSave) >= time.Minute {
			err := r.batchSave(ctx, buff)
			if err != nil {
				return err
			}

			buff = buff[:0]
			lastSave = time.Now().UTC()
		}
	}

	if len(buff) > 0 {
		err := r.batchSave(ctx, buff)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *enhancedResultWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	const maxBatchSize = 50

	buff := make([]*gmaps.Entry, 0, 50)
	lastSave := time.Now().UTC()

	// Use the provided context for cancellation support
	for result := range in {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		entry, ok := result.Data.(*gmaps.Entry)

		if !ok {
			return errors.New("invalid data type")
		}

		buff = append(buff, entry)

		if len(buff) >= maxBatchSize || time.Now().UTC().Sub(lastSave) >= time.Minute {
			err := r.batchSaveEnhanced(ctx, buff)
			if err != nil {
				return err
			}

			buff = buff[:0]
			lastSave = time.Now().UTC()
		}
	}

	if len(buff) > 0 {
		err := r.batchSaveEnhanced(ctx, buff)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *enhancedResultWriterWithExiter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	const maxBatchSize = 1 // Changed from 5 to 1 for precise max results control

	buff := make([]*gmaps.Entry, 0, 1)

	// Use the provided context for cancellation support
	for result := range in {
		// Check for cancellation first
		select {
		case <-ctx.Done():
			fmt.Printf("DEBUG: Result writer stopped due to cancellation\n")
			return ctx.Err()
		default:
		}

		entry, ok := result.Data.(*gmaps.Entry)

		if !ok {
			return errors.New("invalid data type")
		}

		// SIMPLE LIMIT CHECK: Stop if we already have enough results
		if r.exitMonitor != nil {
			maxResults := r.exitMonitor.GetMaxResults()
			if maxResults > 0 {
				// Simple database count check
				var currentCount int
				err := r.db.QueryRow("SELECT COUNT(*) FROM results WHERE job_id = $1", r.jobID).Scan(&currentCount)
				if err == nil && currentCount >= maxResults {
					fmt.Printf("DEBUG: PostgreSQL Writer - already at limit (%d/%d), stopping\n", currentCount, maxResults)
					return nil
				}
			}
		}

		// Only validate for logging purposes - don't count yet
		isValidResult := entry.Title != ""

		// DEBUG: Log detailed result validation
		if !isValidResult {
			fmt.Printf("DEBUG: Skipping invalid result - Title: '%s' (empty title)\n", entry.Title)
		} else {
			fmt.Printf("DEBUG: Valid result received - %s (will count after DB save)\n", entry.Title)
			// Additional debug info
			fmt.Printf("DEBUG: Result details - Link: '%s', Cid: '%s'\n", entry.Link, entry.Cid)
		}

		buff = append(buff, entry)

		// Process immediately for precise control (batch size = 1)
		if len(buff) >= maxBatchSize {
			insertedCount, err := r.batchSaveEnhancedWithCount(ctx, buff)
			if err != nil {
				return err
			}

			// NOW count only actually inserted results
			if r.exitMonitor != nil && insertedCount > 0 {
				fmt.Printf("DEBUG: Successfully inserted %d results, notifying exit monitor\n", insertedCount)
				r.exitMonitor.IncrResultsWritten(insertedCount)
			}

			buff = buff[:0]
		}
	}

	if len(buff) > 0 {
		insertedCount, err := r.batchSaveEnhancedWithCount(ctx, buff)
		if err != nil {
			return err
		}

		// NOW count only actually inserted results
		if r.exitMonitor != nil && insertedCount > 0 {
			fmt.Printf("DEBUG: Final batch - Successfully inserted %d results, notifying exit monitor\n", insertedCount)
			r.exitMonitor.IncrResultsWritten(insertedCount)
		}
	}

	return nil
}

func (r *resultWriter) batchSave(ctx context.Context, entries []*gmaps.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	// Use a timeout context that respects cancellation but allows time for database operations
	dbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	q := `INSERT INTO results
		(data)
		VALUES
		`
	elements := make([]string, 0, len(entries))
	args := make([]interface{}, 0, len(entries))

	for i, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}

		elements = append(elements, fmt.Sprintf("($%d)", i+1))
		args = append(args, data)
	}

	q += strings.Join(elements, ", ")
	// Note: Removed ON CONFLICT clause - no unique constraint exists on cid column

	tx, err := r.db.BeginTx(dbCtx, nil)
	if err != nil {
		return err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	_, err = tx.ExecContext(dbCtx, q, args...)
	if err != nil {
		return err
	}

	err = tx.Commit()

	return err
}

func (r *enhancedResultWriter) batchSaveEnhanced(ctx context.Context, entries []*gmaps.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	// Use a timeout context that respects cancellation but allows time for database operations
	dbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Build the SQL query for all columns - use existing column names where they exist
	q := `INSERT INTO results (
		user_id, job_id, input_id, link, cid, title, categories, category, address,
		openhours, popular_times, website, phone, pluscode, review_count, rating,
		reviews_per_rating, latitude, longitude, status_info, description,
		reviews_link, thumbnail, timezone, price_range, data_id, images,
		reservations, order_online, menu, owner, complete_address, about,
		user_reviews, user_reviews_extended, emails, data, created_at
	) VALUES `

	elements := make([]string, 0, len(entries))
	args := make([]interface{}, 0, len(entries)*38) // 38 fields per entry

	for i, entry := range entries {
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

		// Convert categories slice to comma-separated string
		categoriesStr := strings.Join(entry.Categories, ", ")
		emailsStr := strings.Join(entry.Emails, ", ")

		// Create parameter placeholders for this entry
		base := i * 38
		placeholders := make([]string, 38)
		for j := 0; j < 38; j++ {
			placeholders[j] = fmt.Sprintf("$%d", base+j+1)
		}
		elements = append(elements, "("+strings.Join(placeholders, ", ")+")")

		// Add all arguments in the same order as the columns
		args = append(args,
			r.userID,                // user_id
			r.jobID,                 // job_id
			entry.ID,                // input_id
			entry.Link,              // link
			entry.Cid,               // cid
			entry.Title,             // title
			categoriesStr,           // categories
			entry.Category,          // category
			entry.Address,           // address
			openHoursJSON,           // openhours
			popularTimesJSON,        // popular_times
			entry.WebSite,           // website
			entry.Phone,             // phone
			entry.PlusCode,          // pluscode
			entry.ReviewCount,       // review_count
			entry.ReviewRating,      // rating
			reviewsPerRatingJSON,    // reviews_per_rating
			entry.Latitude,          // latitude
			entry.Longtitude,        // longitude (note: keeping typo from struct)
			entry.Status,            // status_info
			entry.Description,       // description
			entry.ReviewsLink,       // reviews_link
			entry.Thumbnail,         // thumbnail
			entry.Timezone,          // timezone
			entry.PriceRange,        // price_range
			entry.DataID,            // data_id
			imagesJSON,              // images
			reservationsJSON,        // reservations
			orderOnlineJSON,         // order_online
			menuJSON,                // menu
			ownerJSON,               // owner
			completeAddressJSON,     // complete_address
			aboutJSON,               // about
			userReviewsJSON,         // user_reviews
			userReviewsExtendedJSON, // user_reviews_extended
			emailsStr,               // emails
			dataJSON,                // data (full entry as JSON)
			time.Now(),              // created_at
		)
	}

	q += strings.Join(elements, ", ")
	// Remove ON CONFLICT clause temporarily to avoid constraint issues
	// q += " ON CONFLICT (cid, job_id) DO NOTHING" // Prevent duplicates within the same job

	// Log the operation for debugging
	fmt.Printf("[PostgreSQL Writer] Attempting to insert %d entries for user %s, job %s\n", len(entries), r.userID, r.jobID)

	tx, err := r.db.BeginTx(dbCtx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	_, err = tx.ExecContext(dbCtx, q, args...)
	if err != nil {
		fmt.Printf("[PostgreSQL Writer] ERROR: Failed to execute insert: %v\n", err)
		fmt.Printf("[PostgreSQL Writer] Query: %s\n", q[:200]+"...") // Log first 200 chars of query
		return fmt.Errorf("failed to insert results: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		fmt.Printf("[PostgreSQL Writer] ERROR: Failed to commit transaction: %v\n", err)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	fmt.Printf("[PostgreSQL Writer] Successfully inserted %d entries for user %s, job %s\n", len(entries), r.userID, r.jobID)
	return nil
}

// batchSaveEnhancedWithCount is similar to batchSaveEnhanced but returns the number of rows actually inserted
func (r *enhancedResultWriterWithExiter) batchSaveEnhancedWithCount(ctx context.Context, entries []*gmaps.Entry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	// Use a timeout context that respects cancellation but allows time for database operations
	dbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Build the SQL query for all columns - use existing column names where they exist
	q := `INSERT INTO results (
		user_id, job_id, input_id, link, cid, title, categories, category, address,
		openhours, popular_times, website, phone, pluscode, review_count, rating,
		reviews_per_rating, latitude, longitude, status_info, description,
		reviews_link, thumbnail, timezone, price_range, data_id, images,
		reservations, order_online, menu, owner, complete_address, about,
		user_reviews, user_reviews_extended, emails, data, created_at
	) VALUES `

	elements := make([]string, 0, len(entries))
	args := make([]interface{}, 0, len(entries)*38) // 38 fields per entry

	for i, entry := range entries {
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

		// Convert categories slice to comma-separated string
		categoriesStr := strings.Join(entry.Categories, ", ")
		emailsStr := strings.Join(entry.Emails, ", ")

		// Create parameter placeholders for this entry
		base := i * 38
		placeholders := make([]string, 38)
		for j := 0; j < 38; j++ {
			placeholders[j] = fmt.Sprintf("$%d", base+j+1)
		}
		elements = append(elements, "("+strings.Join(placeholders, ", ")+")")

		// Add all arguments in the same order as the columns
		args = append(args,
			r.userID,                // user_id
			r.jobID,                 // job_id
			entry.ID,                // input_id
			entry.Link,              // link
			entry.Cid,               // cid
			entry.Title,             // title
			categoriesStr,           // categories
			entry.Category,          // category
			entry.Address,           // address
			openHoursJSON,           // openhours
			popularTimesJSON,        // popular_times
			entry.WebSite,           // website
			entry.Phone,             // phone
			entry.PlusCode,          // pluscode
			entry.ReviewCount,       // review_count
			entry.ReviewRating,      // rating
			reviewsPerRatingJSON,    // reviews_per_rating
			entry.Latitude,          // latitude
			entry.Longtitude,        // longitude (note: keeping typo from struct)
			entry.Status,            // status_info
			entry.Description,       // description
			entry.ReviewsLink,       // reviews_link
			entry.Thumbnail,         // thumbnail
			entry.Timezone,          // timezone
			entry.PriceRange,        // price_range
			entry.DataID,            // data_id
			imagesJSON,              // images
			reservationsJSON,        // reservations
			orderOnlineJSON,         // order_online
			menuJSON,                // menu
			ownerJSON,               // owner
			completeAddressJSON,     // complete_address
			aboutJSON,               // about
			userReviewsJSON,         // user_reviews
			userReviewsExtendedJSON, // user_reviews_extended
			emailsStr,               // emails
			dataJSON,                // data (full entry as JSON)
			time.Now(),              // created_at
		)
	}

	q += strings.Join(elements, ", ")
	// Note: Removed ON CONFLICT clause - no unique constraint exists on cid column

	// Log the operation for debugging
	fmt.Printf("[PostgreSQL Writer WITH EXITER] Attempting to insert %d entries for user %s, job %s\n", len(entries), r.userID, r.jobID)

	tx, err := r.db.BeginTx(dbCtx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	result, err := tx.ExecContext(dbCtx, q, args...)
	if err != nil {
		fmt.Printf("[PostgreSQL Writer WITH EXITER] ERROR: Failed to execute insert: %v\n", err)
		return 0, fmt.Errorf("failed to insert results: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		fmt.Printf("[PostgreSQL Writer WITH EXITER] ERROR: Failed to commit transaction: %v\n", err)
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Get the number of rows affected (inserted)
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		fmt.Printf("[PostgreSQL Writer WITH EXITER] Warning: Could not get rows affected: %v\n", err)
		// Assume all entries were inserted if we can't get the count
		rowsAffected = int64(len(entries))
	}

	insertedCount := int(rowsAffected)
	fmt.Printf("[PostgreSQL Writer WITH EXITER] Successfully inserted %d entries for user %s, job %s\n", insertedCount, r.userID, r.jobID)

	return insertedCount, nil
}
