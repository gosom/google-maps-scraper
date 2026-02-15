package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
			slog.Debug("postgres_result_writer_stopped_context_cancelled")
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
					slog.Debug("postgres_result_writer_limit_reached",
						slog.Int("current_count", currentCount),
						slog.Int("max_results", maxResults),
						slog.String("job_id", r.jobID),
					)
					return nil
				}
			}
		}

		// Only validate for logging purposes - don't count yet
		isValidResult := entry.Title != ""

		// DEBUG: Log detailed result validation
		if !isValidResult {
			slog.Debug("postgres_result_writer_skipping_invalid_result",
				slog.String("title", entry.Title),
			)
		} else {
			slog.Debug("postgres_result_writer_valid_result_received",
				slog.String("title", entry.Title),
				slog.String("link", entry.Link),
				slog.String("cid", entry.Cid),
			)
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
				slog.Debug("postgres_result_writer_batch_inserted",
					slog.Int("inserted_count", insertedCount),
				)
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
			slog.Debug("postgres_result_writer_final_batch_inserted",
				slog.Int("inserted_count", insertedCount),
			)
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

	slog.Debug("postgres_enhanced_writer_insert_attempt",
		slog.Int("entry_count", len(entries)),
		slog.String("user_id", r.userID),
		slog.String("job_id", r.jobID),
	)

	tx, err := r.db.BeginTx(dbCtx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	_, err = tx.ExecContext(dbCtx, q, args...)
	if err != nil {
		queryPreview := q
		if len(queryPreview) > 200 {
			queryPreview = queryPreview[:200] + "..."
		}
		slog.Error("postgres_enhanced_writer_insert_exec_failed",
			slog.Any("error", err),
			slog.String("query_preview", queryPreview),
		)
		return fmt.Errorf("failed to insert results: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		slog.Error("postgres_enhanced_writer_commit_failed", slog.Any("error", err))
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	slog.Debug("postgres_enhanced_writer_insert_success",
		slog.Int("entry_count", len(entries)),
		slog.String("user_id", r.userID),
		slog.String("job_id", r.jobID),
	)
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

	slog.Debug("postgres_exiter_writer_insert_attempt",
		slog.Int("entry_count", len(entries)),
		slog.String("user_id", r.userID),
		slog.String("job_id", r.jobID),
	)

	tx, err := r.db.BeginTx(dbCtx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	result, err := tx.ExecContext(dbCtx, q, args...)
	if err != nil {
		slog.Error("postgres_exiter_writer_insert_exec_failed", slog.Any("error", err))
		return 0, fmt.Errorf("failed to insert results: %w", err)
	}

	err = tx.Commit()
	if err != nil {
		slog.Error("postgres_exiter_writer_commit_failed", slog.Any("error", err))
		return 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Get the number of rows affected (inserted)
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		slog.Warn("postgres_exiter_writer_rows_affected_failed", slog.Any("error", err))
		// Assume all entries were inserted if we can't get the count
		rowsAffected = int64(len(entries))
	}

	insertedCount := int(rowsAffected)
	slog.Debug("postgres_exiter_writer_insert_success",
		slog.Int("inserted_count", insertedCount),
		slog.String("user_id", r.userID),
		slog.String("job_id", r.jobID),
	)

	return insertedCount, nil
}
