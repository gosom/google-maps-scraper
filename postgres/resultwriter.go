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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gosom/google-maps-scraper/gmaps"
	"github.com/gosom/google-maps-scraper/internal/jsonbsanitize"
	"github.com/gosom/google-maps-scraper/log"
)

func NewResultWriter(db *sql.DB) scrapemate.ResultWriter {
	return &resultWriter{db: db}
}

type resultWriter struct {
	db *sql.DB
}

func (r *resultWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	const maxBatchSize = 50

	buff := make([]*gmaps.Entry, 0, 50)
	lastSave := time.Now().UTC()

	for result := range in {
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

func (r *resultWriter) batchSave(ctx context.Context, entries []*gmaps.Entry) error {
	if len(entries) == 0 {
		return nil
	}

	q := `INSERT INTO results
		(data)
		VALUES
		`
	elements := make([]string, 0, len(entries))
	args := make([]interface{}, 0, len(entries))

	for i, entry := range entries {
		data, err := marshalEntry(entry)
		if err != nil {
			return err
		}

		elements = append(elements, fmt.Sprintf("($%d)", i+1))
		args = append(args, data)
	}

	q += strings.Join(elements, ", ")
	q += " ON CONFLICT DO NOTHING"

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.ExecContext(ctx, q, args...)
	if err != nil {
		if isJSONBNULByteError(err) {
			_ = tx.Rollback()
			return r.saveRowsOneByOne(ctx, entries)
		}

		return err
	}

	err = tx.Commit()
	if err == nil {
		committed = true
	}

	return err
}

func marshalEntry(entry *gmaps.Entry) ([]byte, error) {
	jsonbsanitize.StripNULFromEntry(entry)

	return json.Marshal(entry)
}

func (r *resultWriter) saveRowsOneByOne(ctx context.Context, entries []*gmaps.Entry) error {
	const q = `INSERT INTO results
		(data)
		VALUES
		($1) ON CONFLICT DO NOTHING`

	skipped := 0

	for _, entry := range entries {
		data, err := marshalEntry(entry)
		if err != nil {
			return err
		}

		_, err = r.db.ExecContext(ctx, q, data)
		if err == nil {
			continue
		}

		if isJSONBNULByteError(err) {
			skipped++

			log.Warn("skipping invalid result row during database insert",
				"id", entry.ID,
				"title", entry.Title,
				"error", err,
			)

			continue
		}

		return err
	}

	if skipped > 0 {
		log.Warn("skipped result rows due to invalid jsonb payload",
			"count", skipped,
		)
	}

	return nil
}

func isJSONBNULByteError(err error) bool {
	if err == nil {
		return false
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code != "22P05" {
			return false
		}

		msg := strings.ToLower(pgErr.Message)
		detail := strings.ToLower(pgErr.Detail)

		return strings.Contains(msg, "unsupported unicode escape sequence") &&
			strings.Contains(detail, "cannot be converted to text")
	}

	return false
}
