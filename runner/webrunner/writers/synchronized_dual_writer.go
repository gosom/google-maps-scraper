package writers

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gosom/google-maps-scraper/exiter"
	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/scrapemate"
)

// mustMarshalJSON marshals v to JSON, logging a warning and returning "null" on error.
func mustMarshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Warn("json_marshal_failed", slog.String("type", fmt.Sprintf("%T", v)), slog.Any("error", err))
		return []byte("null")
	}
	return b
}

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
			slog.Debug("synchronized_dual_writer_stopped_context_cancelled",
				slog.Int("results_written", resultCount),
			)
			// Flush CSV before returning to avoid leaving buffered data unwritten.
			// Close() on the underlying file does NOT flush encoding/csv's internal buffer.
			w.csvWriter.Flush()
			if err := w.csvWriter.Error(); err != nil {
				return fmt.Errorf("csv flush error on context cancellation: %w", err)
			}
			return ctx.Err()
		default:
		}

		// Validate result
		var entries []*gmaps.Entry
		switch v := result.Data.(type) {
		case *gmaps.Entry:
			entries = []*gmaps.Entry{v}
		case []*gmaps.Entry:
			entries = v
		default:
			return errors.New("invalid data type")
		}

		for _, entry := range entries {
			if entry == nil {
				continue
			}

			// Check for cancellation between batched entries.
			select {
			case <-ctx.Done():
				w.csvWriter.Flush()
				if err := w.csvWriter.Error(); err != nil {
					return fmt.Errorf("csv flush error on context cancellation: %w", err)
				}
				return ctx.Err()
			default:
			}

			// Write CSV headers on first result
			if !w.headersWritten {
				if err := w.csvWriter.Write(entry.CsvHeaders()); err != nil {
					return fmt.Errorf("failed to write CSV headers: %w", err)
				}
				w.csvWriter.Flush()
				if err := w.csvWriter.Error(); err != nil {
					return fmt.Errorf("failed to flush CSV headers: %w", err)
				}
				w.headersWritten = true
				slog.Debug("synchronized_dual_writer_csv_headers_written")
			}

			// Log what we're processing
			if entry.Title != "" {
				slog.Debug("synchronized_dual_writer_processing_result",
					slog.Int("result_number", resultCount+1),
					slog.String("title", entry.Title),
				)
			} else {
				slog.Debug("synchronized_dual_writer_processing_result_empty_title",
					slog.Int("result_number", resultCount+1),
				)
			}

			// Guard: skip write if cancellation already triggered (max results reached)
			if w.exitMonitor != nil && w.exitMonitor.IsCancellationTriggered() {
				return nil
			}

			// Write to BOTH destinations atomically
			inserted, err := w.writeToPostgreSQL(ctx, entry)
			if err != nil {
				slog.Error("synchronized_dual_writer_postgres_write_failed",
					slog.String("title", entry.Title),
					slog.Any("error", err),
				)
				return fmt.Errorf("PostgreSQL write failed: %w", err)
			}

			if !inserted {
				slog.Debug("synchronized_dual_writer_duplicate_skipped",
					slog.String("cid", entry.Cid),
					slog.String("title", entry.Title),
				)
				continue
			}

			if err := w.writeToCSV(entry); err != nil {
				slog.Error("synchronized_dual_writer_csv_write_failed",
					slog.String("title", entry.Title),
					slog.Any("error", err),
				)
				return fmt.Errorf("CSV write failed: %w", err)
			}

			// Both writes succeeded, increment counter
			resultCount++

			// Notify exit monitor
			if w.exitMonitor != nil {
				w.exitMonitor.IncrResultsWritten(1)
				slog.Debug("synchronized_dual_writer_result_written",
					slog.Int("result_number", resultCount),
				)
			}

			// Note: per-row flushing happens inside writeToCSV to protect against
			// forced shutdown paths that may close the underlying file early.
		}
	}

	// Final flush
	w.csvWriter.Flush()
	if err := w.csvWriter.Error(); err != nil {
		return fmt.Errorf("final CSV flush error: %w", err)
	}

	slog.Info("synchronized_dual_writer_completed",
		slog.Int("results_written", resultCount),
	)
	return nil
}

func (w *SynchronizedDualWriter) writeToPostgreSQL(ctx context.Context, entry *gmaps.Entry) (bool, error) {
	// Use a timeout context for database operations
	dbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Serialize JSON fields
	openHoursJSON := mustMarshalJSON(entry.OpenHours)
	popularTimesJSON := mustMarshalJSON(entry.PopularTimes)
	reviewsPerRatingJSON := mustMarshalJSON(entry.ReviewsPerRating)
	imagesJSON := mustMarshalJSON(entry.Images)
	reservationsJSON := mustMarshalJSON(entry.Reservations)
	orderOnlineJSON := mustMarshalJSON(entry.OrderOnline)
	menuJSON := mustMarshalJSON(entry.Menu)
	ownerJSON := mustMarshalJSON(entry.Owner)
	completeAddressJSON := mustMarshalJSON(entry.CompleteAddress)
	aboutJSON := mustMarshalJSON(entry.About)
	userReviewsJSON := mustMarshalJSON(entry.UserReviews)
	userReviewsExtendedJSON := mustMarshalJSON(entry.UserReviewsExtended)

	// Convert slices to strings
	categoriesStr := strings.Join(entry.Categories, ", ")
	emailsStr := strings.Join(entry.Emails, ", ")

	q := `INSERT INTO results (
		user_id, job_id, input_id, link, cid, title, categories, category, address,
		openhours, popular_times, website, phone, pluscode, review_count, rating,
		reviews_per_rating, latitude, longitude, status_info, description,
		reviews_link, thumbnail, timezone, price_range, data_id, images,
		reservations, order_online, menu, owner, complete_address, about,
		user_reviews, user_reviews_extended, emails, created_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		$11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
		$21, $22, $23, $24, $25, $26, $27, $28, $29, $30,
		$31, $32, $33, $34, $35, $36, $37
	) ON CONFLICT (cid, job_id) DO NOTHING`

	res, err := w.db.ExecContext(dbCtx, q,
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
		time.Now(),              // 37
	)

	if err != nil {
		return false, err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected > 0 {
		_, err = w.db.ExecContext(dbCtx,
			"UPDATE jobs SET result_count = result_count + 1 WHERE id = $1",
			w.jobID)
		if err != nil {
			slog.Error("result_count_update_failed",
				slog.Any("error", err), slog.String("job_id", w.jobID))
			return false, fmt.Errorf("failed to update result count: %w", err)
		}
	}
	return rowsAffected > 0, nil
}

func (w *SynchronizedDualWriter) writeToCSV(entry *gmaps.Entry) error {
	// Use the Entry's own CsvRow() method which properly formats ALL fields
	// including JSON serialization of complex types
	if err := w.csvWriter.Write(entry.CsvRow()); err != nil {
		return err
	}

	// Flush after each row so that even if the job is force-completed and the
	// underlying file is closed early, we don't lose buffered data mid-record.
	w.csvWriter.Flush()
	if err := w.csvWriter.Error(); err != nil {
		return fmt.Errorf("csv flush error: %w", err)
	}
	return nil
}
