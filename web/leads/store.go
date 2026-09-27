// Package leads keeps every business a job has found as one deduplicated lead list, with the
// outreach status the operator records against each. It is the dashboard's memory: jobs come and
// go, their CSV files can be deleted, and the leads stay.
package leads

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // sqlite driver
)

// Sources a lead can come from. A lead is unique per source and source id.
const (
	SourceGoogleMaps = "google_maps"
	SourcePracto     = "practo"
)

// Statuses an operator moves a lead through. "do_not_contact" is permanent by intent: nothing
// automatic ever contacts such a lead.
var Statuses = []string{"new", "contacted", "replied", "interested", "not_interested", "do_not_contact"}

func validStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}

	return false
}

// Lead is one business (or, from Practo, one doctor's practice).
type Lead struct {
	ID        int64
	Source    string
	SourceID  string
	Name      string
	Category  string
	Phone     string
	Emails    string // "; "-separated, the business's own domain first
	Website   string
	Address   string
	City      string
	State     string
	Country   string
	Rating    float64
	Reviews   int
	Link      string
	Query     string // the search that last found it
	Status    string
	Note      string
	StatusAt  time.Time
	FirstSeen time.Time
	LastSeen  time.Time
	Jobs      int
	// SaleshandyAt is when the lead was last sent into a Saleshandy sequence; zero if never.
	SaleshandyAt time.Time
	// SaleshandySequence is the sequence it was sent into.
	SaleshandySequence string
	// WAOptInAt is when the business agreed to WhatsApp messages, and WAOptInSource how (typed by
	// the operator: "replied yes to email, 25 Sep"). Zero: never asked, or opted out since.
	WAOptInAt     time.Time
	WAOptInSource string
	// WAOptOutAt is when it asked to stop; nothing is sent after that, whatever else is recorded.
	WAOptOutAt time.Time
	// WhatsAppAt is when a template was last sent to it, and WATemplate which one.
	WhatsAppAt time.Time
	WATemplate string
}

// WhatsAppAllowed says whether a WhatsApp template may be sent to the lead: it opted in, has not
// opted out since, and is not do_not_contact.
func (l *Lead) WhatsAppAllowed() bool {
	return !l.WAOptInAt.IsZero() && l.WAOptOutAt.IsZero() && l.Status != "do_not_contact"
}

// FirstEmail is the address outreach should use.
func (l *Lead) FirstEmail() string {
	first, _, _ := strings.Cut(l.Emails, ";")

	return strings.TrimSpace(first)
}

// Store is the lead list in its own SQLite file.
type Store struct {
	db *sql.DB
}

// Open opens (and creates, the first time) the lead list at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)

	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA foreign_keys=ON"} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()

			return nil, err
		}
	}

	for _, ddl := range []string{schema, runsSchema, compareSchema} {
		if _, err := db.Exec(ddl); err != nil {
			_ = db.Close()

			return nil, fmt.Errorf("lead schema: %w", err)
		}
	}

	for _, c := range []struct{ name, decl string }{
		{"saleshandy_sequence", "TEXT NOT NULL DEFAULT ''"},
		{"wa_opt_in_at", "INTEGER NOT NULL DEFAULT 0"},
		{"wa_opt_in_source", "TEXT NOT NULL DEFAULT ''"},
		{"wa_opt_out_at", "INTEGER NOT NULL DEFAULT 0"},
		{"wa_template", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := addColumn(db, "leads", c.name, c.decl); err != nil {
			_ = db.Close()

			return nil, err
		}
	}

	return &Store{db: db}, nil
}

// addColumn adds a column to a table that predates it; a no-op when it is already there.
func addColumn(db *sql.DB, table, column, decl string) error {
	rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}

		if name == column {
			return nil
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + decl)

	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS leads (
	id INTEGER PRIMARY KEY,
	source TEXT NOT NULL,
	source_id TEXT NOT NULL,
	name TEXT NOT NULL,
	category TEXT NOT NULL DEFAULT '',
	phone TEXT NOT NULL DEFAULT '',
	emails TEXT NOT NULL DEFAULT '',
	website TEXT NOT NULL DEFAULT '',
	address TEXT NOT NULL DEFAULT '',
	city TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT '',
	country TEXT NOT NULL DEFAULT '',
	rating REAL NOT NULL DEFAULT 0,
	reviews INTEGER NOT NULL DEFAULT 0,
	link TEXT NOT NULL DEFAULT '',
	query TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'new',
	note TEXT NOT NULL DEFAULT '',
	status_at INTEGER NOT NULL DEFAULT 0,
	saleshandy_at INTEGER NOT NULL DEFAULT 0,
	whatsapp_at INTEGER NOT NULL DEFAULT 0,
	first_seen INTEGER NOT NULL,
	last_seen INTEGER NOT NULL,
	UNIQUE (source, source_id)
);
CREATE INDEX IF NOT EXISTS leads_status ON leads (status);
CREATE INDEX IF NOT EXISTS leads_city ON leads (city);
CREATE INDEX IF NOT EXISTS leads_last_seen ON leads (last_seen);
CREATE TABLE IF NOT EXISTS lead_jobs (
	lead_id INTEGER NOT NULL REFERENCES leads (id) ON DELETE CASCADE,
	job_id TEXT NOT NULL,
	PRIMARY KEY (lead_id, job_id)
);
CREATE INDEX IF NOT EXISTS lead_jobs_job ON lead_jobs (job_id);
CREATE TABLE IF NOT EXISTS saleshandy_pushes (
	id INTEGER PRIMARY KEY,
	request_id TEXT NOT NULL,
	sequence TEXT NOT NULL,
	step_id TEXT NOT NULL,
	prospects INTEGER NOT NULL,
	at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS whatsapp_sends (
	id INTEGER PRIMARY KEY,
	lead_id INTEGER NOT NULL REFERENCES leads (id) ON DELETE CASCADE,
	phone TEXT NOT NULL,
	template TEXT NOT NULL,
	language TEXT NOT NULL,
	message_id TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS whatsapp_sends_lead ON whatsapp_sends (lead_id);
CREATE TABLE IF NOT EXISTS ingested_jobs (
	job_id TEXT PRIMARY KEY,
	rows INTEGER NOT NULL,
	at INTEGER NOT NULL
);
`

// upsert records a lead found by jobID. A later sighting refreshes what the listing says, but
// never blanks a field it no longer shows, and never touches the operator's status or note.
func upsert(ctx context.Context, tx *sql.Tx, l *Lead, jobID string, now time.Time) error {
	const q = `
INSERT INTO leads (source, source_id, name, category, phone, emails, website, address, city, state,
	country, rating, reviews, link, query, first_seen, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source, source_id) DO UPDATE SET
	name = excluded.name,
	category = CASE WHEN excluded.category <> '' THEN excluded.category ELSE leads.category END,
	phone = CASE WHEN excluded.phone <> '' THEN excluded.phone ELSE leads.phone END,
	emails = CASE WHEN excluded.emails <> '' THEN excluded.emails ELSE leads.emails END,
	website = CASE WHEN excluded.website <> '' THEN excluded.website ELSE leads.website END,
	address = CASE WHEN excluded.address <> '' THEN excluded.address ELSE leads.address END,
	city = CASE WHEN excluded.city <> '' THEN excluded.city ELSE leads.city END,
	state = CASE WHEN excluded.state <> '' THEN excluded.state ELSE leads.state END,
	country = CASE WHEN excluded.country <> '' THEN excluded.country ELSE leads.country END,
	rating = CASE WHEN excluded.rating > 0 THEN excluded.rating ELSE leads.rating END,
	reviews = CASE WHEN excluded.reviews > 0 THEN excluded.reviews ELSE leads.reviews END,
	link = CASE WHEN excluded.link <> '' THEN excluded.link ELSE leads.link END,
	query = CASE WHEN excluded.query <> '' THEN excluded.query ELSE leads.query END,
	last_seen = excluded.last_seen
RETURNING id`

	var id int64

	err := tx.QueryRowContext(ctx, q, l.Source, l.SourceID, l.Name, l.Category, l.Phone, l.Emails,
		l.Website, l.Address, l.City, l.State, l.Country, l.Rating, l.Reviews, l.Link, l.Query,
		now.Unix(), now.Unix()).Scan(&id)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO lead_jobs (lead_id, job_id) VALUES (?, ?)`, id, jobID)

	return err
}

// Filter is what the lead table is narrowed and ordered by.
type Filter struct {
	Q          string // name, email, phone or website contains
	Source     string
	Status     string
	City       string
	Category   string
	JobID      string
	IDs        []int64 // only these leads
	Saleshandy string  // "sent", "not_sent", or "" for either
	WhatsApp   string  // "opted_in" (may be messaged), "sent", or "" for either
	HasEmail   bool
	HasPhone   bool
	HasWebsite bool
	MinRating  float64
	MinReviews int
	Sort       string // newest (default), rating, reviews, name
	Page       int    // 1-based
	PageSize   int
}

func (f *Filter) where() (clause string, args []any) {
	var conds []string

	if q := strings.TrimSpace(f.Q); q != "" {
		like := "%" + strings.ToLower(q) + "%"

		conds = append(conds, "(lower(name) LIKE ? OR lower(emails) LIKE ? OR phone LIKE ? OR lower(website) LIKE ?)")
		args = append(args, like, like, like, like)
	}

	for _, eq := range []struct {
		col, val string
	}{{"source", f.Source}, {"status", f.Status}} {
		if eq.val != "" {
			conds = append(conds, eq.col+" = ?")
			args = append(args, eq.val)
		}
	}

	for _, contains := range []struct {
		col, val string
	}{{"city", f.City}, {"category", f.Category}} {
		if v := strings.TrimSpace(contains.val); v != "" {
			conds = append(conds, "lower("+contains.col+") LIKE ?")
			args = append(args, "%"+strings.ToLower(v)+"%")
		}
	}

	if f.JobID != "" {
		conds = append(conds, "id IN (SELECT lead_id FROM lead_jobs WHERE job_id = ?)")
		args = append(args, f.JobID)
	}

	if len(f.IDs) > 0 {
		conds = append(conds, "id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.IDs)), ",")+")")

		for _, id := range f.IDs {
			args = append(args, id)
		}
	}

	switch f.Saleshandy {
	case "sent":
		conds = append(conds, "saleshandy_at > 0")
	case "not_sent":
		conds = append(conds, "saleshandy_at = 0")
	}

	switch f.WhatsApp {
	case "opted_in":
		conds = append(conds, "wa_opt_in_at > 0 AND wa_opt_out_at = 0 AND status <> 'do_not_contact'")
	case "sent":
		conds = append(conds, "whatsapp_at > 0")
	}

	if f.HasEmail {
		conds = append(conds, "emails <> ''")
	}

	if f.HasPhone {
		conds = append(conds, "phone <> ''")
	}

	if f.HasWebsite {
		conds = append(conds, "website <> ''")
	}

	if f.MinRating > 0 {
		conds = append(conds, "rating >= ?")
		args = append(args, f.MinRating)
	}

	if f.MinReviews > 0 {
		conds = append(conds, "reviews >= ?")
		args = append(args, f.MinReviews)
	}

	if len(conds) == 0 {
		return "", args
	}

	return " WHERE " + strings.Join(conds, " AND "), args
}

func (f *Filter) orderBy() string {
	switch f.Sort {
	case "rating":
		return " ORDER BY rating DESC, reviews DESC, id DESC"
	case "reviews":
		return " ORDER BY reviews DESC, id DESC"
	case "name":
		return " ORDER BY name COLLATE NOCASE, id"
	default:
		return " ORDER BY last_seen DESC, id DESC"
	}
}

const leadColumns = `id, source, source_id, name, category, phone, emails, website, address, city, state,
	country, rating, reviews, link, query, status, note, status_at, first_seen, last_seen,
	saleshandy_at, saleshandy_sequence, wa_opt_in_at, wa_opt_in_source, wa_opt_out_at, whatsapp_at,
	wa_template, (SELECT count(*) FROM lead_jobs WHERE lead_id = leads.id)`

func scanLead(sc interface{ Scan(...any) error }) (Lead, error) {
	var (
		l                                           Lead
		statusAt, firstSeen, lastSeen, saleshandyAt int64
		optIn, optOut, whatsappAt                   int64
	)

	err := sc.Scan(&l.ID, &l.Source, &l.SourceID, &l.Name, &l.Category, &l.Phone, &l.Emails,
		&l.Website, &l.Address, &l.City, &l.State, &l.Country, &l.Rating, &l.Reviews, &l.Link,
		&l.Query, &l.Status, &l.Note, &statusAt, &firstSeen, &lastSeen, &saleshandyAt,
		&l.SaleshandySequence, &optIn, &l.WAOptInSource, &optOut, &whatsappAt, &l.WATemplate, &l.Jobs)
	if err != nil {
		return l, err
	}

	l.WAOptInAt, l.WAOptOutAt, l.WhatsAppAt = unixOrZero(optIn), unixOrZero(optOut), unixOrZero(whatsappAt)

	if saleshandyAt > 0 {
		l.SaleshandyAt = time.Unix(saleshandyAt, 0).UTC()
	}

	if statusAt > 0 {
		l.StatusAt = time.Unix(statusAt, 0).UTC()
	}

	l.FirstSeen = time.Unix(firstSeen, 0).UTC()
	l.LastSeen = time.Unix(lastSeen, 0).UTC()

	return l, nil
}

// Query returns one page of leads matching f, and how many match in all. PageSize 0 means every
// matching lead (for exports).
func (s *Store) Query(ctx context.Context, f *Filter) ([]Lead, int, error) {
	where, args := f.where()

	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM leads"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// where holds only fixed column names and "?" placeholders; every value is a bound argument.
	q := "SELECT " + leadColumns + " FROM leads" + where + f.orderBy() //nolint:gosec // see above

	if f.PageSize > 0 {
		page := max(f.Page, 1)
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.PageSize, (page-1)*f.PageSize)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Lead

	for rows.Next() {
		l, err := scanLead(rows)
		if err != nil {
			return nil, 0, err
		}

		out = append(out, l)
	}

	return out, total, rows.Err()
}

// Get returns one lead.
func (s *Store) Get(ctx context.Context, id int64) (Lead, error) {
	return scanLead(s.db.QueryRowContext(ctx, "SELECT "+leadColumns+" FROM leads WHERE id = ?", id))
}

var errBadStatus = errors.New("unknown lead status")

// SetStatus moves leads to status. An empty note leaves each lead's note as it was.
func (s *Store) SetStatus(ctx context.Context, ids []int64, status, note string) error {
	if !validStatus(status) {
		return errBadStatus
	}

	if len(ids) == 0 {
		return nil
	}

	now := time.Now().UTC().Unix()
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")

	args := []any{status, now}
	q := "UPDATE leads SET status = ?, status_at = ?"

	if note != "" {
		q += ", note = ?"

		args = append(args, note)
	}

	for _, id := range ids {
		args = append(args, id)
	}

	_, err := s.db.ExecContext(ctx, q+" WHERE id IN ("+placeholders+")", args...)

	return err
}

// SetNote replaces one lead's note.
func (s *Store) SetNote(ctx context.Context, id int64, note string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE leads SET note = ? WHERE id = ?", note, id)

	return err
}

// Stats are the counts across the whole lead list, for the header tiles.
type Stats struct {
	Total, WithEmail, WithPhone, Contacted int
}

// Stats counts the lead list.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats

	err := s.db.QueryRowContext(ctx, `SELECT count(*),
		coalesce(sum(emails <> ''), 0),
		coalesce(sum(phone <> ''), 0),
		coalesce(sum(status <> 'new'), 0) FROM leads`).Scan(&st.Total, &st.WithEmail, &st.WithPhone, &st.Contacted)

	return st, err
}

// JobLeadCounts is how many leads each job found, for the job list.
func (s *Store) JobLeadCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT job_id, count(*) FROM lead_jobs GROUP BY job_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}

	for rows.Next() {
		var (
			id string
			n  int
		)

		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}

		out[id] = n
	}

	return out, rows.Err()
}

// Ingested says whether a job's results are already in the lead list.
func (s *Store) Ingested(ctx context.Context, jobID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM ingested_jobs WHERE job_id = ?", jobID).Scan(&n)

	return n > 0, err
}

// MarkSaleshandy records that leads were sent into a Saleshandy sequence by one import request.
func (s *Store) MarkSaleshandy(ctx context.Context, ids []int64, requestID, sequence, stepID string) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Unix()

	_, err = tx.ExecContext(ctx, `INSERT INTO saleshandy_pushes (request_id, sequence, step_id, prospects, at)
		VALUES (?, ?, ?, ?, ?)`, requestID, sequence, stepID, len(ids), now)
	if err != nil {
		return err
	}

	for _, id := range ids {
		_, err = tx.ExecContext(ctx, "UPDATE leads SET saleshandy_at = ?, saleshandy_sequence = ? WHERE id = ?", now, sequence, id)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func unixOrZero(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}

	return time.Unix(sec, 0).UTC()
}

// ErrOptInSource is an opt-in recorded without saying how the business agreed.
var ErrOptInSource = errors.New("say how the business agreed to WhatsApp messages (at least 5 characters)")

// SetWhatsAppOptIn records that a lead agreed to WhatsApp messages, and how. It clears an
// earlier opt-out: the business has asked again.
func (s *Store) SetWhatsAppOptIn(ctx context.Context, id int64, source string) error {
	source = strings.TrimSpace(source)
	if len([]rune(source)) < 5 {
		return ErrOptInSource
	}

	_, err := s.db.ExecContext(ctx, "UPDATE leads SET wa_opt_in_at = ?, wa_opt_in_source = ?, wa_opt_out_at = 0 WHERE id = ?",
		time.Now().UTC().Unix(), source, id)

	return err
}

// SetWhatsAppOptOut records that a lead asked for no more WhatsApp messages.
func (s *Store) SetWhatsAppOptOut(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE leads SET wa_opt_out_at = ?, wa_opt_in_at = 0 WHERE id = ?", time.Now().UTC().Unix(), id)

	return err
}

// WhatsAppSend is one template sent (or refused by Meta) to one lead.
type WhatsAppSend struct {
	LeadID    int64
	Phone     string
	Template  string
	Language  string
	MessageID string
	Error     string
}

// RecordWhatsApp logs a send; a successful one also marks the lead as messaged.
func (s *Store) RecordWhatsApp(ctx context.Context, w *WhatsAppSend) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Unix()

	_, err = tx.ExecContext(ctx, `INSERT INTO whatsapp_sends (lead_id, phone, template, language, message_id, error, at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, w.LeadID, w.Phone, w.Template, w.Language, w.MessageID, w.Error, now)
	if err != nil {
		return err
	}

	if w.Error == "" {
		if _, err = tx.ExecContext(ctx, "UPDATE leads SET whatsapp_at = ?, wa_template = ? WHERE id = ?", now, w.Template, w.LeadID); err != nil {
			return err
		}
	}

	return tx.Commit()
}
