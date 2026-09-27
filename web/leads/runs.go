package leads

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/AxioIntel/Axio-CRED/gmaps"
)

// The run ledger: one row per search a job ran (search_runs) and one per job (job_runs), for both
// collectors, so they can be compared on the same numbers. v2 records each search as it ends, with
// its own timing. v1 runs its searches in parallel inside a browser, so its per-search rows are
// read from the finished CSV afterwards and their timing is the job's (job_level = 1).

const runsSchema = `
CREATE TABLE IF NOT EXISTS search_runs (
	id INTEGER PRIMARY KEY,
	job_id TEXT NOT NULL,
	search TEXT NOT NULL,
	method TEXT NOT NULL,
	job_level INTEGER NOT NULL DEFAULT 0,
	started_at INTEGER NOT NULL,
	finished_at INTEGER NOT NULL,
	pages INTEGER NOT NULL DEFAULT 0,
	places_found INTEGER NOT NULL DEFAULT 0,
	duplicates INTEGER NOT NULL DEFAULT 0,
	websites INTEGER NOT NULL DEFAULT 0,
	email_attempted INTEGER NOT NULL DEFAULT 0,
	emails_found INTEGER NOT NULL DEFAULT 0,
	email_none INTEGER NOT NULL DEFAULT 0,
	email_fetch_failed INTEGER NOT NULL DEFAULT 0,
	email_skipped INTEGER NOT NULL DEFAULT 0,
	unique_emails INTEGER NOT NULL DEFAULT 0,
	blocks INTEGER NOT NULL DEFAULT 0,
	min_site_gap_ms INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS search_runs_job ON search_runs (job_id);
CREATE TABLE IF NOT EXISTS job_runs (
	job_id TEXT PRIMARY KEY,
	collector TEXT NOT NULL,
	started_at INTEGER NOT NULL DEFAULT 0,
	finished_at INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT '',
	searches INTEGER NOT NULL DEFAULT 0,
	bound_ms INTEGER NOT NULL DEFAULT 0,
	blocks INTEGER NOT NULL DEFAULT 0,
	restarts INTEGER NOT NULL DEFAULT 0,
	stalls INTEGER NOT NULL DEFAULT 0,
	discarded INTEGER NOT NULL DEFAULT 0,
	unique_emails INTEGER NOT NULL DEFAULT 0,
	cpu_p50 REAL NOT NULL DEFAULT 0,
	cpu_p95 REAL NOT NULL DEFAULT 0,
	mem_p50 REAL NOT NULL DEFAULT 0,
	mem_p95 REAL NOT NULL DEFAULT 0
);
`

// V1Method is the ledger's name for v1's runs.
const V1Method = "v1-browser"

// RunRow is one search_runs row.
type RunRow struct {
	JobID    string
	JobLevel bool // timing is the whole job's (v1), not this search's
	gmaps.SearchRun
}

// RecordSearchRun records one search a job ran (v2, as each search ends).
func (s *Store) RecordSearchRun(ctx context.Context, jobID string, r *gmaps.SearchRun) error {
	return insertRun(ctx, s.db, jobID, false, r)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertRun(ctx context.Context, db execer, jobID string, jobLevel bool, r *gmaps.SearchRun) error {
	_, err := db.ExecContext(ctx, `INSERT INTO search_runs (job_id, search, method, job_level, started_at,
	finished_at, pages, places_found, duplicates, websites, email_attempted, emails_found, email_none,
	email_fetch_failed, email_skipped, unique_emails, blocks, min_site_gap_ms, status, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, r.Search, r.Method, boolInt(jobLevel), r.StartedAt.UnixMilli(), r.FinishedAt.UnixMilli(),
		r.Pages, r.PlacesFound, r.Duplicates, r.Websites, r.EmailAttempted, r.EmailsFound, r.EmailNone,
		r.EmailFetchFailed, r.EmailSkipped, r.UniqueEmails, r.Blocks, r.MinSiteGap.Milliseconds(), r.Status, r.Error)

	return err
}

// placeholders is n SQL parameter markers, "?, ?, ...": nothing else is ever put in the query.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// RecordRunsFromCSV records v1's searches from its finished CSV: one row per search of the job
// (a search with no row is recorded "empty"), counted by the input_id every row carries. Timing is
// the job's. A CSV from before email_status still counts places, websites and emails found. Any
// earlier rows for the job (a previous attempt) are replaced.
func (s *Store) RecordRunsFromCSV(ctx context.Context, jobID, csvPath string, searches []string,
	started, finished time.Time) error {
	counts, err := countCSVRuns(csvPath)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "DELETE FROM search_runs WHERE job_id = ?", jobID); err != nil {
		return err
	}

	for _, search := range searches {
		search = strings.TrimSpace(search)
		if search == "" {
			continue
		}

		var run gmaps.SearchRun
		if c := counts[search]; c != nil {
			run = c.run
			run.UniqueEmails = len(c.emails)
		}

		run.Search, run.Method, run.StartedAt, run.FinishedAt = search, V1Method, started, finished

		run.Status = gmaps.SearchEmpty
		if run.PlacesFound > 0 {
			run.Status = gmaps.SearchOK
		}

		if err := insertRun(ctx, tx, jobID, true, &run); err != nil {
			return err
		}
	}

	return tx.Commit()
}

type csvRun struct {
	run    gmaps.SearchRun
	emails map[string]bool
}

// CountCSVEmails is how many distinct email addresses a job's CSV holds (0 for no file).
func CountCSVEmails(csvPath string) (int, error) {
	counts, err := countCSVRuns(csvPath)
	if err != nil {
		return 0, err
	}

	all := map[string]bool{}

	for _, c := range counts {
		for e := range c.emails {
			all[e] = true
		}
	}

	return len(all), nil
}

// countCSVRuns tallies a job CSV per search.
func countCSVRuns(csvPath string) (map[string]*csvRun, error) {
	out := map[string]*csvRun{}

	f, err := os.Open(csvPath)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}

	if err != nil {
		return nil, err
	}

	defer f.Close()

	t, err := newCSVTable(f)
	if t == nil || err != nil {
		return out, err
	}

	hasStatus := t.has("email_status")

	for {
		rec, err := t.next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, err
		}

		search := searchOf(t.get(rec, "input_id"))

		c := out[search]
		if c == nil {
			c = &csvRun{emails: map[string]bool{}}
			out[search] = c
		}

		emails := joinEmails(t.get(rec, "emails"))
		for _, e := range strings.Split(emails, "; ") {
			if e != "" {
				c.emails[e] = true
			}
		}

		c.run.PlacesFound++
		countEmailOutcome(&c.run, hasStatus, t.get(rec, "email_status"), t.get(rec, "website"), emails != "")
	}

	return out, nil
}

// countEmailOutcome adds one row's email outcome to its search's counts.
func countEmailOutcome(r *gmaps.SearchRun, hasStatus bool, status, website string, hasEmail bool) {
	if !hasStatus {
		if website != "" {
			r.Websites++
			r.EmailAttempted++

			if hasEmail {
				r.EmailsFound++
			} else {
				r.EmailNone++
			}
		}

		return
	}

	switch {
	case status == gmaps.EmailFound:
		r.Websites++
		r.EmailAttempted++
		r.EmailsFound++
	case status == gmaps.EmailNone:
		r.Websites++
		r.EmailAttempted++
		r.EmailNone++
	case strings.HasPrefix(status, gmaps.EmailFetchFailed):
		r.Websites++
		r.EmailAttempted++
		r.EmailFetchFailed++
	case status == gmaps.EmailSkippedBudget:
		r.Websites++
		r.EmailSkipped++
	}
}

// Runs are the search_runs rows of these jobs, in the order they were recorded.
func (s *Store) Runs(ctx context.Context, jobIDs ...string) ([]RunRow, error) {
	if len(jobIDs) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(jobIDs))
	for _, id := range jobIDs {
		args = append(args, id)
	}

	//nolint:gosec // only "?" placeholders are added to the query
	query := `SELECT job_id, search, method, job_level, started_at, finished_at,
	pages, places_found, duplicates, websites, email_attempted, emails_found, email_none,
	email_fetch_failed, email_skipped, unique_emails, blocks, min_site_gap_ms, status, error
FROM search_runs WHERE job_id IN (` + placeholders(len(jobIDs)) + `) ORDER BY id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RunRow

	for rows.Next() {
		var (
			r                 RunRow
			jobLevel          int
			started, finished int64
			gapMs             int64
		)

		if err := rows.Scan(&r.JobID, &r.Search, &r.Method, &jobLevel, &started, &finished, &r.Pages,
			&r.PlacesFound, &r.Duplicates, &r.Websites, &r.EmailAttempted, &r.EmailsFound, &r.EmailNone,
			&r.EmailFetchFailed, &r.EmailSkipped, &r.UniqueEmails, &r.Blocks, &gapMs, &r.Status, &r.Error); err != nil {
			return nil, err
		}

		r.JobLevel = jobLevel == 1
		r.StartedAt, r.FinishedAt = time.UnixMilli(started).UTC(), time.UnixMilli(finished).UTC()
		r.MinSiteGap = time.Duration(gapMs) * time.Millisecond
		out = append(out, r)
	}

	return out, rows.Err()
}

// JobRun is one job's run as the ledger keeps it.
type JobRun struct {
	JobID      string
	Collector  string
	StartedAt  time.Time
	FinishedAt time.Time
	Status     string
	Searches   int
	Bound      time.Duration // the longest the job was allowed
	Blocks     int
	Restarts   int  // times a restart cut it off
	Stalls     int  // times the watchdog stopped it
	Discarded  bool // interrupted by the pause window or AxioIntel's yield: not a fair sample
	// UniqueEmails are the distinct addresses in the job's own CSV. Not the lead list's: a lead
	// keeps the emails any job found, so it would credit one collector with another's.
	UniqueEmails int
	CPUP50       float64
	CPUP95       float64
	MemP50       float64
	MemP95       float64
}

// Seconds is how long the job ran.
func (j *JobRun) Seconds() float64 { return j.FinishedAt.Sub(j.StartedAt).Seconds() }

// StartJobRun records that a job's run began, keeping its restart and stall counts from any
// earlier attempt. The earlier attempt's search rows are dropped, and so is its "discarded" mark:
// a job the pause window or yield stopped runs again from its first search, and that run is
// whole. The ledger holds the job's last run, the one its CSV and status describe.
func (s *Store) StartJobRun(ctx context.Context, jobID, collector string, searches int, bound time.Duration,
	at time.Time) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM search_runs WHERE job_id = ?", jobID); err != nil {
		return err
	}

	_, err := s.db.ExecContext(ctx, `INSERT INTO job_runs (job_id, collector, started_at, searches, bound_ms, status)
VALUES (?, ?, ?, ?, ?, 'working')
ON CONFLICT (job_id) DO UPDATE SET collector = excluded.collector, started_at = excluded.started_at,
	searches = excluded.searches, bound_ms = excluded.bound_ms, status = 'working', finished_at = 0,
	discarded = 0`,
		jobID, collector, at.UnixMilli(), searches, bound.Milliseconds())

	return err
}

// FinishJobRun records how a job's run ended.
func (s *Store) FinishJobRun(ctx context.Context, j *JobRun) error {
	res, err := s.db.ExecContext(ctx, `UPDATE job_runs SET finished_at = ?, status = ?, blocks = ?,
	unique_emails = ?, cpu_p50 = ?, cpu_p95 = ?, mem_p50 = ?, mem_p95 = ? WHERE job_id = ?`,
		j.FinishedAt.UnixMilli(), j.Status, j.Blocks, j.UniqueEmails, j.CPUP50, j.CPUP95, j.MemP50, j.MemP95, j.JobID)
	if err != nil {
		return err
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %s has no run to finish", j.JobID)
	}

	return nil
}

// Job run counters bumped by the runner.
const (
	CounterRestarts  = "restarts"
	CounterStalls    = "stalls"
	CounterDiscarded = "discarded"
)

// BumpJobRun adds one to a job run's restarts or stalls, or marks it discarded. A job with no run
// recorded yet is left alone.
func (s *Store) BumpJobRun(ctx context.Context, jobID, counter string) error {
	var set string

	switch counter {
	case CounterRestarts:
		set = "restarts = restarts + 1"
	case CounterStalls:
		set = "stalls = stalls + 1"
	case CounterDiscarded:
		set = "discarded = 1"
	default:
		return fmt.Errorf("unknown job run counter %q", counter)
	}

	_, err := s.db.ExecContext(ctx, "UPDATE job_runs SET "+set+" WHERE job_id = ?", jobID) //nolint:gosec // set is one of three constants above

	return err
}

// JobRuns are the job_runs rows of these jobs, by job id.
func (s *Store) JobRuns(ctx context.Context, jobIDs ...string) (map[string]JobRun, error) {
	out := map[string]JobRun{}
	if len(jobIDs) == 0 {
		return out, nil
	}

	args := make([]any, 0, len(jobIDs))
	for _, id := range jobIDs {
		args = append(args, id)
	}

	//nolint:gosec // only "?" placeholders are added to the query
	query := `SELECT job_id, collector, started_at, finished_at, status, searches,
	bound_ms, blocks, restarts, stalls, discarded, unique_emails, cpu_p50, cpu_p95, mem_p50, mem_p95
FROM job_runs WHERE job_id IN (` + placeholders(len(jobIDs)) + `)`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			j                 JobRun
			started, finished int64
			boundMs           int64
			discarded         int
		)

		if err := rows.Scan(&j.JobID, &j.Collector, &started, &finished, &j.Status, &j.Searches, &boundMs,
			&j.Blocks, &j.Restarts, &j.Stalls, &discarded, &j.UniqueEmails, &j.CPUP50, &j.CPUP95, &j.MemP50,
			&j.MemP95); err != nil {
			return nil, err
		}

		j.StartedAt = time.UnixMilli(started).UTC()
		if finished > 0 {
			j.FinishedAt = time.UnixMilli(finished).UTC()
		}

		j.Bound = time.Duration(boundMs) * time.Millisecond
		j.Discarded = discarded == 1
		out[j.JobID] = j
	}

	return out, rows.Err()
}
