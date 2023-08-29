package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/gosom/scrapemate"
	"github.com/shopspring/decimal"

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
		(title, category, address, openhours, website, phone, pluscode, review_count, rating,
		latitude, longitude)
		VALUES
		($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT DO NOTHING
		`

	rating := decimal.NewFromFloat(entry.ReviewRating)

	_, err := r.db.ExecContext(ctx, q,
		entry.Title, entry.Category, entry.Address, entry.OpenHours, entry.WebSite,
		entry.Phone, entry.PlusCode, entry.ReviewCount, rating, entry.Latitude, entry.Longtitude,
	)

	return err
}
