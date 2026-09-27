package leads

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// IngestCSV folds one finished Google Maps job's CSV into the lead list and records the job as
// ingested. A job whose CSV is partial (it failed or ran out of time) is ingested for what it has.
// Returns how many rows became (or refreshed) leads.
func (s *Store) IngestCSV(ctx context.Context, jobID, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	rows, err := parseMapsCSV(f)
	if err != nil {
		return 0, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}

	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()

	for i := range rows {
		if err := upsert(ctx, tx, &rows[i], jobID, now); err != nil {
			return 0, fmt.Errorf("lead %q: %w", rows[i].Name, err)
		}
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO ingested_jobs (job_id, rows, at) VALUES (?, ?, ?)
		ON CONFLICT (job_id) DO UPDATE SET rows = excluded.rows, at = excluded.at`, jobID, len(rows), now.Unix())
	if err != nil {
		return 0, err
	}

	return len(rows), tx.Commit()
}

// csvTable reads the Google Maps scraper's CSV (gmaps.Entry.CsvHeaders) by column name, so a
// column added upstream does not shift anything.
type csvTable struct {
	cr      *csv.Reader
	col     map[string]int
	columns int
}

// newCSVTable reads the header; nil (and no error) for an empty file.
func newCSVTable(r io.Reader) (*csvTable, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true

	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}

	return &csvTable{cr: cr, col: col, columns: len(header)}, nil
}

// has is whether the file has a column.
func (m *csvTable) has(name string) bool {
	_, ok := m.col[name]

	return ok
}

// next is the next complete row, or io.EOF. A row cut off by a job's time limit (a parse error,
// or fewer fields than the header) ends the file or is skipped: what came before it is kept.
func (m *csvTable) next() ([]string, error) {
	for {
		rec, err := m.cr.Read()
		if err != nil {
			var perr *csv.ParseError
			if errors.As(err, &perr) {
				return nil, io.EOF
			}

			return nil, err
		}

		if len(rec) >= m.columns {
			return rec, nil
		}
	}
}

// get is a row's value in a column ("" when the file has no such column).
func (m *csvTable) get(rec []string, name string) string {
	i, ok := m.col[name]
	if !ok || i >= len(rec) {
		return ""
	}

	return strings.TrimSpace(rec[i])
}

// parseMapsCSV reads the scraper's CSV into leads.
func parseMapsCSV(r io.Reader) ([]Lead, error) {
	m, err := newCSVTable(r)
	if m == nil || err != nil {
		return nil, err
	}

	get := m.get

	var out []Lead

	for {
		rec, err := m.next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, err
		}

		name := get(rec, "title")
		sourceID := get(rec, "place_id")

		if sourceID == "" {
			sourceID = get(rec, "link")
		}

		if name == "" || sourceID == "" {
			continue
		}

		l := Lead{
			Source:   SourceGoogleMaps,
			SourceID: sourceID,
			Name:     name,
			Category: get(rec, "category"),
			Phone:    get(rec, "phone"),
			Emails:   joinEmails(get(rec, "emails")),
			Website:  get(rec, "website"),
			Address:  get(rec, "address"),
			Link:     get(rec, "link"),
			Query:    searchOf(get(rec, "input_id")),
		}

		l.Rating, _ = strconv.ParseFloat(get(rec, "review_rating"), 64)
		l.Reviews, _ = strconv.Atoi(get(rec, "review_count"))
		l.City, l.State, l.Country = addressParts(get(rec, "complete_address"))

		out = append(out, l)
	}

	return out, nil
}

// joinEmails turns the scraper's ", "-joined addresses into the lead list's "; " form.
func joinEmails(raw string) string {
	var out []string

	for _, e := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' }) {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}

	return strings.Join(out, "; ")
}

// addressParts reads city, state and country from the scraper's complete_address JSON.
func addressParts(raw string) (city, state, country string) {
	if raw == "" {
		return "", "", ""
	}

	var a struct {
		City    string `json:"city"`
		State   string `json:"state"`
		Country string `json:"country"`
	}

	if json.Unmarshal([]byte(raw), &a) != nil {
		return "", "", ""
	}

	return a.City, a.State, a.Country
}

// gridSuffix is what a map-grid job adds to each search's id: "-" and the square's UUID.
var gridSuffix = regexp.MustCompile(`-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// searchOf is the search a listing came from, without a map-grid square's id.
func searchOf(inputID string) string {
	return gridSuffix.ReplaceAllString(inputID, "")
}
