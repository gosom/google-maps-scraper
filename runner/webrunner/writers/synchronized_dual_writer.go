package writers

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

// SynchronizedDualWriter writes to both PostgreSQL and CSV in a synchronized way
// ensuring both destinations receive exactly the same results
type SynchronizedDualWriter struct {
	db             *sql.DB
	csvWriter      *csv.Writer
	userID         string
	jobID          string
	exitMonitor    exiter.Exiter
	headersWritten bool
}

// NewSynchronizedDualWriter creates a writer that writes to both PostgreSQL and CSV
func NewSynchronizedDualWriter(
	db *sql.DB,
	csvWriter *csv.Writer,
	userID string,
	jobID string,
	exitMonitor exiter.Exiter,
) scrapemate.ResultWriter {
	return &SynchronizedDualWriter{
		db:             db,
		csvWriter:      csvWriter,
		userID:         userID,
		jobID:          jobID,
		exitMonitor:    exitMonitor,
		headersWritten: false,
	}
}

func (w *SynchronizedDualWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	resultCount := 0

	for result := range in {
		// Check for cancellation
		select {
		case <-ctx.Done():
			fmt.Printf("DEBUG: SynchronizedDualWriter stopped due to cancellation (wrote %d results)\n", resultCount)
			// Flush CSV before returning
			w.csvWriter.Flush()
			return ctx.Err()
		default:
		}

		// Validate result
		entry, ok := result.Data.(*gmaps.Entry)
		if !ok {
			return errors.New("invalid data type")
		}

		// Write CSV headers on first result
		if !w.headersWritten {
			if err := w.csvWriter.Write(entry.CsvHeaders()); err != nil {
				return fmt.Errorf("failed to write CSV headers: %w", err)
			}
			w.headersWritten = true
			fmt.Println("DEBUG: SynchronizedDualWriter - CSV headers written")
		}

		// Log what we're processing
		if entry.Title != "" {
			fmt.Printf("DEBUG: SynchronizedDualWriter - Processing result #%d: %s\n", resultCount+1, entry.Title)
		} else {
			fmt.Printf("DEBUG: SynchronizedDualWriter - Processing result #%d: (empty title)\n", resultCount+1)
		}

		// Write to BOTH destinations atomically
		if err := w.writeToPostgreSQL(ctx, entry); err != nil {
			fmt.Printf("ERROR: SynchronizedDualWriter - PostgreSQL write failed for %s: %v\n", entry.Title, err)
			return fmt.Errorf("PostgreSQL write failed: %w", err)
		}

		if err := w.writeToCSV(entry); err != nil {
			fmt.Printf("ERROR: SynchronizedDualWriter - CSV write failed for %s: %v\n", entry.Title, err)
			return fmt.Errorf("CSV write failed: %w", err)
		}

		// Both writes succeeded, increment counter
		resultCount++

		// Notify exit monitor
		if w.exitMonitor != nil {
			w.exitMonitor.IncrResultsWritten(1)
			fmt.Printf("DEBUG: SynchronizedDualWriter - Successfully wrote result #%d to both destinations\n", resultCount)
		}

		// Flush CSV periodically to ensure data is written
		if resultCount%10 == 0 {
			w.csvWriter.Flush()
			if err := w.csvWriter.Error(); err != nil {
				return fmt.Errorf("CSV flush error: %w", err)
			}
		}
	}

	// Final flush
	w.csvWriter.Flush()
	if err := w.csvWriter.Error(); err != nil {
		return fmt.Errorf("final CSV flush error: %w", err)
	}

	fmt.Printf("DEBUG: SynchronizedDualWriter - Completed. Total results written to both destinations: %d\n", resultCount)
	return nil
}

func (w *SynchronizedDualWriter) writeToPostgreSQL(ctx context.Context, entry *gmaps.Entry) error {
	// Use a timeout context for database operations
	dbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

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

	q := `INSERT INTO results (
		user_id, job_id, input_id, link, cid, title, categories, category, address,
		openhours, popular_times, website, phone, pluscode, review_count, rating,
		reviews_per_rating, latitude, longitude, status_info, description,
		reviews_link, thumbnail, timezone, price_range, data_id, images,
		reservations, order_online, menu, owner, complete_address, about,
		user_reviews, user_reviews_extended, emails, data, created_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		$11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
		$21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
		$31, $32, $33, $34, $35, $36, $37, $38
	)`

	_, err := w.db.ExecContext(dbCtx, q,
		w.userID,                // 1
		w.jobID,                 // 2
		entry.ID,                // 3
		entry.Link,              // 4
		entry.Cid,               // 5
		entry.Title,             // 6
		categoriesStr,           // 7
		entry.Category,          // 8
		entry.Address,           // 9
		openHoursJSON,           // 10
		popularTimesJSON,        // 11
		entry.WebSite,           // 12
		entry.Phone,             // 13
		entry.PlusCode,          // 14
		entry.ReviewCount,       // 15
		entry.ReviewRating,      // 16
		reviewsPerRatingJSON,    // 17
		entry.Latitude,          // 18
		entry.Longtitude,        // 19
		entry.Status,            // 20
		entry.Description,       // 21
		entry.ReviewsLink,       // 22
		entry.Thumbnail,         // 23
		entry.Timezone,          // 24
		entry.PriceRange,        // 25
		entry.DataID,            // 26
		imagesJSON,              // 27
		reservationsJSON,        // 28
		orderOnlineJSON,         // 29
		menuJSON,                // 30
		ownerJSON,               // 31
		completeAddressJSON,     // 32
		aboutJSON,               // 33
		userReviewsJSON,         // 34
		userReviewsExtendedJSON, // 35
		emailsStr,               // 36
		dataJSON,                // 37
		time.Now(),              // 38
	)

	return err
}

func (w *SynchronizedDualWriter) writeToCSV(entry *gmaps.Entry) error {
	// Use the Entry's own CsvRow() method which properly formats ALL fields
	// including JSON serialization of complex types
	return w.csvWriter.Write(entry.CsvRow())
}
