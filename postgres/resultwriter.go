package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/gosom/scrapemate"

	"github.com/gosom/google-maps-scraper/gmaps"
)

func NewResultWriter(db *sql.DB) scrapemate.ResultWriter {
	return &resultWriter{db: db}
}

type resultWriter struct {
	db *sql.DB
}

func (r *resultWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error {
	for result := range in {
		entry, ok := result.Data.(*gmaps.Entry)

		if !ok {
			return errors.New("invalid data type")
		}

		if err := r.saveEntry(ctx, entry); err != nil {
			return err
		}
	}

	return nil
}

func (r *resultWriter) saveEntry(ctx context.Context, entry *gmaps.Entry) error {
	q := `INSERT INTO results
		(data)
		VALUES
		($1) ON CONFLICT DO NOTHING
		`

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, q, data)

	return err
}
