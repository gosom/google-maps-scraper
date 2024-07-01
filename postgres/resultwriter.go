package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gosom/kit/logging"
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
			logging.Error("resultWriter saveEntry %v", err)
			return err
		}
	}

	return nil
}

func (r *resultWriter) saveEntry(ctx context.Context, entry *gmaps.Entry) error {
	q := `INSERT INTO results (title, category, address, openhours, website, phone, pluscode, review_count, rating, latitude, longitude, geom, data)
		VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
		ST_GeomFromText(ST_AsText(ST_MakePoint($12, $13)), 4326), 
		$14

		) ON CONFLICT DO NOTHING;
		`
	openHoursString := ""
	for day, hours := range entry.OpenHours {
		if len(hours) > 0 {
			openHoursString += fmt.Sprintf("%s: %s\n", day, strings.Join(hours, ", "))
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		logging.Error("resultWriter Marshal %v", err)
		return err
	}

	_, err = r.db.ExecContext(ctx, q,
		entry.Title, entry.Category, entry.Address, openHoursString, entry.WebSite, entry.Phone, entry.PlusCode,
		entry.ReviewCount, entry.ReviewRating, entry.Latitude, entry.Longtitude,
		entry.Longtitude, entry.Latitude, // Note the order: Longitude, Latitude
		data,
	)
	if err != nil {
		logging.Error("resultWriter ExecContext %v", err)
		return err
	}
	return err
}
