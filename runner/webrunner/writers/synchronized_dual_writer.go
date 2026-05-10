package writers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
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
	"github.com/jackc/pgx/v5/pgconn"
)

// nulEscape is the 6-character JSON escape `\u0000` that json.Marshal emits
// for a NUL byte (0x00). Valid per the JSON spec but rejected by Postgres'
// json/jsonb input parser. Google Maps scraped strings (review text, image
// alt text) occasionally carry NUL bytes, so we strip the escape before
// handing the bytes to pgx.
//
// Defence-in-depth, not the prod fix. The May 2026 prod incident that
// motivated this strip turned out to have a different root cause: pgx
// running under PgBouncer transaction-mode forces simple_protocol, which
// encodes a Go []byte as bytea — not jsonb — and fails with SQLSTATE 22P02
// before any NUL escape could matter (see PR #63 / jackc/pgx#2231,
// resolved by passing the marshaled bytes as `string`). The strip is kept
// because (a) PR #60 already proved scraped data does contain NUL bytes,
// and (b) the cost is one bytes.Contains call per row.
var nulEscape = []byte{'\\', 'u', '0', '0', '0', '0'}

// jsonDiagnosticBytesCap caps the per-column base64 dump emitted on a row
// failure. 4 KB is enough to read the offending region without exploding
// log volume on review/menu blobs that can be hundreds of KB.
const jsonDiagnosticBytesCap = 4096

// mustMarshalJSON marshals v to JSON, logging a warning and returning
// "null" on error. Strips the JSON-escaped NUL sequence from the output so
// the result is safe to insert into a Postgres json/jsonb column.
func mustMarshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Warn("json_marshal_failed", slog.String("type", fmt.Sprintf("%T", v)), slog.Any("error", err))
		return []byte("null")
	}
	if bytes.Contains(b, nulEscape) {
		b = bytes.ReplaceAll(b, nulEscape, nil)
	}
	return b
}

// containsSurrogateEscape reports whether the JSON bytes carry a literal
// `\uD8xx`-`\uDFxx` escape — the lone-surrogate range that Postgres' json
// parser rejects with SQLSTATE 22P02. json.Marshal emits these when the
// source string already contained a half-pair (common in scraped UTF-16
// data round-tripped through JS). Lowercase since Go's json package
// normalises to lowercase hex.
func containsSurrogateEscape(b []byte) bool {
	for _, prefix := range [][]byte{
		[]byte("\\ud8"), []byte("\\ud9"), []byte("\\uda"), []byte("\\udb"),
		[]byte("\\udc"), []byte("\\udd"), []byte("\\ude"), []byte("\\udf"),
	} {
		if bytes.Contains(b, prefix) {
			return true
		}
	}
	return false
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
	failedCount := 0

	slog.Info("synchronized_dual_writer_run_started",
		slog.String("job_id", w.jobID),
		slog.String("user_id", w.userID),
	)

	// finishingFields builds the structured-log payload for any termination
	// path so we always emit a single, consistent run_finishing line.
	finishingFields := func(reason string, err error) []any {
		fields := []any{
			slog.String("job_id", w.jobID),
			slog.String("exit_reason", reason),
			slog.Int("results_written", resultCount),
			slog.Int("rows_failed", failedCount),
		}
		if err != nil {
			fields = append(fields, slog.Any("error", err))
		}
		return fields
	}

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
				slog.Info("synchronized_dual_writer_run_finishing",
					finishingFields("context_cancelled_csv_flush_error", err)...)
				return fmt.Errorf("csv flush error on context cancellation: %w", err)
			}
			slog.Info("synchronized_dual_writer_run_finishing",
				finishingFields("context_cancelled", ctx.Err())...)
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
			slog.Info("synchronized_dual_writer_run_finishing",
				finishingFields("invalid_data_type", nil)...)
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
					slog.Info("synchronized_dual_writer_run_finishing",
						finishingFields("context_cancelled_csv_flush_error", err)...)
					return fmt.Errorf("csv flush error on context cancellation: %w", err)
				}
				slog.Info("synchronized_dual_writer_run_finishing",
					finishingFields("context_cancelled", ctx.Err())...)
				return ctx.Err()
			default:
			}

			// Write CSV headers on first result
			if !w.headersWritten {
				if err := w.csvWriter.Write(entry.CsvHeaders()); err != nil {
					slog.Info("synchronized_dual_writer_run_finishing",
						finishingFields("csv_header_write_error", err)...)
					return fmt.Errorf("failed to write CSV headers: %w", err)
				}
				w.csvWriter.Flush()
				if err := w.csvWriter.Error(); err != nil {
					slog.Info("synchronized_dual_writer_run_finishing",
						finishingFields("csv_header_flush_error", err)...)
					return fmt.Errorf("failed to flush CSV headers: %w", err)
				}
				w.headersWritten = true
				slog.Debug("synchronized_dual_writer_csv_headers_written")
			}

			// Guard: skip write if cancellation already triggered (max results reached)
			if w.exitMonitor != nil && w.exitMonitor.IsCancellationTriggered() {
				slog.Info("synchronized_dual_writer_run_finishing",
					finishingFields("exit_monitor_cancellation", nil)...)
				return nil
			}

			slog.Debug("synchronized_dual_writer_processing_result",
				slog.String("title", entry.Title),
			)

			// Write to BOTH destinations atomically.
			//
			// Per-row failures here MUST NOT kill the writer goroutine: if
			// they did, scrapemate would keep producing results into a
			// channel nobody reads, IncrResultsWritten would never tick,
			// and the job would hang forever in "scraping" with no
			// surfaced error (May 2026 prod incident: SQLSTATE 22P02 on a
			// single row stranded the entire job). Skip the row, log
			// loudly, and continue.
			inserted, err := w.writeToPostgreSQL(ctx, entry)
			if err != nil {
				failedCount++
				slog.Error("synchronized_dual_writer_postgres_write_failed",
					slog.String("job_id", w.jobID),
					slog.String("title", entry.Title),
					slog.String("cid", entry.Cid),
					slog.String("data_id", entry.DataID),
					slog.Int("rows_failed", failedCount),
					slog.Any("error", err),
				)
				continue
			}

			if !inserted {
				slog.Debug("synchronized_dual_writer_duplicate_skipped",
					slog.String("title", entry.Title),
				)
				continue
			}

			if err := w.writeToCSV(entry); err != nil {
				// CSV row failed AFTER PG succeeded — the destinations
				// are now out of sync for this row. Logging at ERROR
				// surfaces the divergence; we continue rather than die so
				// the rest of the job still completes. CSV-export users
				// can re-export from PG if they hit this.
				failedCount++
				slog.Error("synchronized_dual_writer_csv_write_failed",
					slog.String("job_id", w.jobID),
					slog.String("title", entry.Title),
					slog.String("cid", entry.Cid),
					slog.Int("rows_failed", failedCount),
					slog.Any("error", err),
				)
				continue
			}

			// Both writes succeeded, increment counter
			slog.Debug("synchronized_dual_writer_result_written",
				slog.Int("result_number", resultCount+1),
			)
			resultCount++

			// Notify exit monitor
			if w.exitMonitor != nil {
				w.exitMonitor.IncrResultsWritten(1)
			}

			// Note: per-row flushing happens inside writeToCSV to protect against
			// forced shutdown paths that may close the underlying file early.
		}
	}

	// Final flush
	w.csvWriter.Flush()
	if err := w.csvWriter.Error(); err != nil {
		slog.Info("synchronized_dual_writer_run_finishing",
			finishingFields("final_csv_flush_error", err)...)
		return fmt.Errorf("final CSV flush error: %w", err)
	}

	slog.Info("synchronized_dual_writer_completed",
		slog.String("job_id", w.jobID),
		slog.Int("results_written", resultCount),
		slog.Int("rows_failed", failedCount),
	)
	slog.Info("synchronized_dual_writer_run_finishing",
		finishingFields("input_channel_closed", nil)...)
	return nil
}

func (w *SynchronizedDualWriter) writeToPostgreSQL(ctx context.Context, entry *gmaps.Entry) (bool, error) {
	// Use a timeout context for database operations
	dbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Serialize JSON fields. Keep an ordered list keyed by column name so
	// that on a Postgres rejection we can dump exactly the bytes we tried
	// to insert and pinpoint the offending column from the error
	// position/column (see logRowFailureDiagnostics).
	//
	// CRITICAL: at the ExecContext boundary below, every jsonFields entry
	// is passed as `string(...)`, never as raw `[]byte`. Production talks
	// to DigitalOcean Managed Postgres via PgBouncer in transaction mode,
	// which forces pgx into simple_protocol (extended-protocol prepared
	// statements are session-level and incompatible with PgBouncer
	// transaction mode). In simple_protocol pgx infers parameter types
	// purely from the Go type: `string` → text, `[]byte` → bytea
	// (jackc/pgx#2231, maintainer comment). When `[]byte` is sent for a
	// jsonb column under simple_protocol, pgx emits a bytea hex literal
	// (`'\x5b7b...'`) and the row fails with SQLSTATE 22P02 "invalid
	// input syntax for type json" — the May 2026 prod incident.
	// Reproduced 1:1 against simple_protocol on 2026-05-10.
	jsonFields := []struct {
		column string
		bytes  []byte
	}{
		{"openhours", mustMarshalJSON(entry.OpenHours)},
		{"popular_times", mustMarshalJSON(entry.PopularTimes)},
		{"reviews_per_rating", mustMarshalJSON(entry.ReviewsPerRating)},
		{"images", mustMarshalJSON(entry.Images)},
		{"reservations", mustMarshalJSON(entry.Reservations)},
		{"order_online", mustMarshalJSON(entry.OrderOnline)},
		{"menu", mustMarshalJSON(entry.Menu)},
		{"owner", mustMarshalJSON(entry.Owner)},
		{"complete_address", mustMarshalJSON(entry.CompleteAddress)},
		{"about", mustMarshalJSON(entry.About)},
		{"user_reviews", mustMarshalJSON(entry.UserReviews)},
		{"user_reviews_extended", mustMarshalJSON(entry.UserReviewsExtended)},
	}

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
		w.userID,                     // 1
		w.jobID,                      // 2
		entry.ID,                     // 3
		entry.Link,                   // 4
		entry.Cid,                    // 5
		entry.Title,                  // 6
		categoriesStr,                // 7
		entry.Category,               // 8
		entry.Address,                // 9
		string(jsonFields[0].bytes),  // 10  openhours      — string, not []byte (simple_protocol fix)
		string(jsonFields[1].bytes),  // 11  popular_times
		entry.WebSite,                // 12
		entry.Phone,                  // 13
		entry.PlusCode,               // 14
		entry.ReviewCount,            // 15
		entry.ReviewRating,           // 16
		string(jsonFields[2].bytes),  // 17  reviews_per_rating
		entry.Latitude,               // 18
		entry.Longtitude,             // 19
		entry.Status,                 // 20
		entry.Description,            // 21
		entry.ReviewsLink,            // 22
		entry.Thumbnail,              // 23
		entry.Timezone,               // 24
		entry.PriceRange,             // 25
		entry.DataID,                 // 26
		string(jsonFields[3].bytes),  // 27  images
		string(jsonFields[4].bytes),  // 28  reservations
		string(jsonFields[5].bytes),  // 29  order_online
		string(jsonFields[6].bytes),  // 30  menu
		string(jsonFields[7].bytes),  // 31  owner
		string(jsonFields[8].bytes),  // 32  complete_address
		string(jsonFields[9].bytes),  // 33  about
		string(jsonFields[10].bytes), // 34  user_reviews
		string(jsonFields[11].bytes), // 35  user_reviews_extended
		emailsStr,                    // 36
		time.Now(),                   // 37
	)

	if err != nil {
		w.logRowFailureDiagnostics(entry, jsonFields, err)
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

// logRowFailureDiagnostics fires once per failed INSERT and emits the data
// we've been missing for two prod incidents: the full pgconn error
// (Code/Detail/Hint/Position/ColumnName) plus base64-encoded dumps of
// every JSON column we tried to insert (truncated). Base64 is used so
// binary or invalid-UTF-8 bytes survive the JSON log encoder unchanged —
// when Postgres rejects content, the offending bytes are by definition
// not safe to put inside a JSON string field.
func (w *SynchronizedDualWriter) logRowFailureDiagnostics(
	entry *gmaps.Entry,
	jsonFields []struct {
		column string
		bytes  []byte
	},
	err error,
) {
	pgFields := []any{
		slog.String("job_id", w.jobID),
		slog.String("title", entry.Title),
		slog.String("cid", entry.Cid),
		slog.String("data_id", entry.DataID),
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		pgFields = append(pgFields,
			slog.String("pg_code", pgErr.Code),
			slog.String("pg_severity", pgErr.Severity),
			slog.String("pg_message", pgErr.Message),
			slog.String("pg_detail", pgErr.Detail),
			slog.String("pg_hint", pgErr.Hint),
			slog.Int64("pg_position", int64(pgErr.Position)),
			slog.String("pg_column", pgErr.ColumnName),
			slog.String("pg_table", pgErr.TableName),
			slog.String("pg_constraint", pgErr.ConstraintName),
			slog.String("pg_routine", pgErr.Routine),
		)
	} else {
		pgFields = append(pgFields, slog.String("pg_error_unwrap", "not_a_pgconn_PgError"))
	}

	slog.Error("synchronized_dual_writer_postgres_pgerror_detail", pgFields...)

	// Per-column base64 dump. One log line per column so log aggregation
	// can index/grep them individually and so a single oversized payload
	// can't blow up a single line.
	for _, f := range jsonFields {
		dump := f.bytes
		truncated := false
		if len(dump) > jsonDiagnosticBytesCap {
			dump = dump[:jsonDiagnosticBytesCap]
			truncated = true
		}
		slog.Error("synchronized_dual_writer_postgres_row_dump",
			slog.String("job_id", w.jobID),
			slog.String("cid", entry.Cid),
			slog.String("column", f.column),
			slog.Int("byte_length", len(f.bytes)),
			slog.Bool("truncated", truncated),
			slog.Bool("contains_surrogate_escape", containsSurrogateEscape(f.bytes)),
			slog.Bool("contains_replacement_char", bytes.Contains(f.bytes, []byte("\\ufffd"))),
			slog.String("base64", base64.StdEncoding.EncodeToString(dump)),
		)
	}
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
