package leads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// What the Verify page keeps: comparisons of v1 against v2 (which jobs each ran, in which arm and
// run), the last smoke check of each collector, and the page's saved query set.

const compareSchema = `
CREATE TABLE IF NOT EXISTS comparisons (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	queries TEXT NOT NULL,
	arms TEXT NOT NULL,
	runs_per_arm INTEGER NOT NULL,
	stopped INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS comparison_jobs (
	comparison_id TEXT NOT NULL REFERENCES comparisons (id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	arm TEXT NOT NULL,
	run_no INTEGER NOT NULL,
	job_id TEXT NOT NULL,
	PRIMARY KEY (comparison_id, seq)
);
CREATE INDEX IF NOT EXISTS comparison_jobs_job ON comparison_jobs (job_id);
CREATE TABLE IF NOT EXISTS smoke_checks (
	id INTEGER PRIMARY KEY,
	collector TEXT NOT NULL,
	job_id TEXT NOT NULL,
	at INTEGER NOT NULL,
	passed INTEGER NOT NULL,
	checks TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS smoke_checks_collector ON smoke_checks (collector, at);
CREATE TABLE IF NOT EXISTS verify_settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// Comparison is one v1-against-v2 experiment.
type Comparison struct {
	ID         string
	Name       string
	CreatedAt  time.Time
	Queries    []string
	Arms       json.RawMessage // the arms' settings, as the web layer defines them
	RunsPerArm int
	Stopped    bool
	Jobs       []ComparisonJob
}

// ComparisonJob is one job of a comparison: one arm's run.
type ComparisonJob struct {
	Seq   int
	Arm   string
	RunNo int
	JobID string
}

// CreateComparison stores a comparison and the jobs it queued.
func (s *Store) CreateComparison(ctx context.Context, c *Comparison) error {
	queries, err := json.Marshal(c.Queries)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `INSERT INTO comparisons (id, name, created_at, queries, arms, runs_per_arm)
VALUES (?, ?, ?, ?, ?, ?)`, c.ID, c.Name, c.CreatedAt.Unix(), string(queries), string(c.Arms), c.RunsPerArm)
	if err != nil {
		return err
	}

	for _, j := range c.Jobs {
		_, err = tx.ExecContext(ctx, `INSERT INTO comparison_jobs (comparison_id, seq, arm, run_no, job_id)
VALUES (?, ?, ?, ?, ?)`, c.ID, j.Seq, j.Arm, j.RunNo, j.JobID)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ErrNoComparison is a comparison id that is not there.
var ErrNoComparison = errors.New("no such comparison")

// Comparison is one comparison with its jobs, in queue order.
func (s *Store) Comparison(ctx context.Context, id string) (Comparison, error) {
	var (
		c       Comparison
		created int64
		queries string
		arms    string
		stopped int
	)

	err := s.db.QueryRowContext(ctx, `SELECT id, name, created_at, queries, arms, runs_per_arm, stopped
FROM comparisons WHERE id = ?`, id).Scan(&c.ID, &c.Name, &created, &queries, &arms, &c.RunsPerArm, &stopped)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNoComparison
	}

	if err != nil {
		return c, err
	}

	c.CreatedAt, c.Arms, c.Stopped = time.Unix(created, 0).UTC(), json.RawMessage(arms), stopped == 1
	if err := json.Unmarshal([]byte(queries), &c.Queries); err != nil {
		return c, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT seq, arm, run_no, job_id FROM comparison_jobs
WHERE comparison_id = ? ORDER BY seq`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()

	for rows.Next() {
		var j ComparisonJob
		if err := rows.Scan(&j.Seq, &j.Arm, &j.RunNo, &j.JobID); err != nil {
			return c, err
		}

		c.Jobs = append(c.Jobs, j)
	}

	return c, rows.Err()
}

// Comparisons are the most recent comparisons, newest first, with their jobs.
func (s *Store) Comparisons(ctx context.Context, limit int) ([]Comparison, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM comparisons ORDER BY created_at DESC, rowid DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}

	var ids []string

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()

			return nil, err
		}

		ids = append(ids, id)
	}

	_ = rows.Close()

	out := make([]Comparison, 0, len(ids))

	for _, id := range ids {
		c, err := s.Comparison(ctx, id)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	return out, nil
}

// StopComparison marks a comparison stopped; the web layer deletes its queued jobs.
func (s *Store) StopComparison(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE comparisons SET stopped = 1 WHERE id = ?", id)

	return err
}

// Check is one line of a smoke check.
type Check struct {
	Name   string `json:"name"`
	Result string `json:"result"` // pass, fail, warn
	Detail string `json:"detail,omitempty"`
}

// Check results.
const (
	CheckPass = "pass"
	CheckFail = "fail"
	CheckWarn = "warn"
)

// SmokeCheck is one smoke check of one collector.
type SmokeCheck struct {
	Collector string
	JobID     string
	At        time.Time
	Passed    bool
	Checks    []Check
}

// RecordSmokeCheck stores a smoke check's result.
func (s *Store) RecordSmokeCheck(ctx context.Context, c *SmokeCheck) error {
	checks, err := json.Marshal(c.Checks)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `INSERT INTO smoke_checks (collector, job_id, at, passed, checks)
VALUES (?, ?, ?, ?, ?)`, c.Collector, c.JobID, c.At.Unix(), boolInt(c.Passed), string(checks))

	return err
}

// LastSmokeChecks is each collector's latest smoke check.
func (s *Store) LastSmokeChecks(ctx context.Context) (map[string]SmokeCheck, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT collector, job_id, at, passed, checks FROM smoke_checks
WHERE id IN (SELECT max(id) FROM smoke_checks GROUP BY collector)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]SmokeCheck{}

	for rows.Next() {
		var (
			c      SmokeCheck
			at     int64
			passed int
			checks string
		)

		if err := rows.Scan(&c.Collector, &c.JobID, &at, &passed, &checks); err != nil {
			return nil, err
		}

		c.At, c.Passed = time.Unix(at, 0).UTC(), passed == 1
		if err := json.Unmarshal([]byte(checks), &c.Checks); err != nil {
			return nil, err
		}

		out[c.Collector] = c
	}

	return out, rows.Err()
}

// Setting is a saved Verify page setting ("" when unset).
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string

	err := s.db.QueryRowContext(ctx, "SELECT value FROM verify_settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}

	return v, err
}

// SetSetting saves a Verify page setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO verify_settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)

	return err
}
