// Package webrunner — outcome.go defines the value-typed result of running
// a single scrape job. The function webrunner.scrapeJob returns a JobOutcome
// rather than mutating *web.Job in place, so the outer worker can log the
// correct event by switching on outcome.Status (the prior code logged
// "job_scrape_succeeded" whenever scrapeJob returned nil error, even if
// internal state had set jobSuccess=false — the bug this type prevents
// by construction).
package webrunner

import "github.com/gosom/google-maps-scraper/models"

// TerminalCause is an internal-only label describing WHY a job ended.
// Logged to slog as a structured attribute and used in the outer worker's
// switch; never surfaced to the UI (see FailureReason for that).
//
// Named string (not iota) so Loki streams read "seed_exhausted" instead of
// "1" — operational debugging trumps a few bytes per log line.
type TerminalCause string

const (
	CauseSuccess       TerminalCause = "success"
	CauseSeedExhausted TerminalCause = "seed_exhausted" // all seeds terminally failed
	CauseUserCancel    TerminalCause = "user_cancel"    // user hit Cancel via API
	CauseHardTimeout   TerminalCause = "hard_timeout"   // job's allowed_seconds budget expired
	CauseRuntimeError  TerminalCause = "runtime_error"  // unexpected error from mate.Start
	CausePartial       TerminalCause = "partial"        // timed out / cancelled but produced results
	// CauseProxyPoolExhausted is set when proxypool.Pool.Acquire returns
	// ErrPoolExhausted — every proxy is cooling or quarantined and the
	// scrape cannot proceed. Operator-visible: indicates the entire pool
	// has been burned by the target (typically Google detecting datacenter
	// IPs across the whole Decodo allocation).
	CauseProxyPoolExhausted TerminalCause = "proxy_pool_exhausted"
)

// JobOutcome is the discriminated result of one scrape job.
//
// Constructed via OutcomeSuccess / OutcomePartial / OutcomeUserCancelled /
// OutcomeFailed — direct struct literals are discouraged because the
// constructors enforce the (Status, Cause, FailureReason) correlation.
//
// rawErr is unexported on purpose. Use Err() to retrieve it. The reason:
// raw errors are for ERROR-level support logging only; they MUST NOT leak
// into the UI's failure_reason. Keeping rawErr unexported makes it
// awkward enough that callers reach for FailureReason instead.
type JobOutcome struct {
	Status        string
	Cause         TerminalCause
	FailureReason string
	ResultCount   int
	rawErr        error
}

// Err returns the raw error captured at outcome construction. Always nil for
// success / user-cancel outcomes. For failed outcomes, the error is logged
// at ERROR by the outer worker with the job_id attribute so support can
// correlate the user-facing FailureReason back to the underlying technical
// cause in Loki.
func (o JobOutcome) Err() error { return o.rawErr }

// OutcomeSuccess — every seed completed and we wrote at least one result row.
func OutcomeSuccess(resultCount int) JobOutcome {
	return JobOutcome{
		Status:      models.StatusCompleted,
		Cause:       CauseSuccess,
		ResultCount: resultCount,
	}
}

// OutcomePartial — the job didn't complete cleanly (timeout, mid-run cancel)
// but produced at least one result row. Returned by classifyOutcome.
//
// Note: in the current orchestration, scrapeJob's post-run sanity check
// (the seeds_incomplete + zero_results_written guards) may downgrade a
// partial to OutcomeFailed when the seed/result bookkeeping disagrees —
// so a CausePartial Cause is rarely visible in the persisted row today.
// Future work (typed seed-outcome events in Task 4/5) will make
// CausePartial reachable in steady state.
func OutcomePartial(resultCount int) JobOutcome {
	return JobOutcome{
		Status:      models.StatusCompleted,
		Cause:       CausePartial,
		ResultCount: resultCount,
	}
}

// OutcomeUserCancelled — the user hit Cancel via the API; the DB row's status
// transitioned through "aborting" and we caught it. Not a failure; no
// FailureReason needed.
func OutcomeUserCancelled() JobOutcome {
	return JobOutcome{
		Status: models.StatusCancelled,
		Cause:  CauseUserCancel,
	}
}

// OutcomeFailed — terminal failure with a sanitized user-facing reason and
// the raw error attached for support correlation. Use the appropriate cause:
// CauseSeedExhausted for proxy/network failures, CauseHardTimeout for
// allowed_seconds expiry, CauseRuntimeError for everything else.
func OutcomeFailed(cause TerminalCause, reason string, raw error) JobOutcome {
	return JobOutcome{
		Status:        models.StatusFailed,
		Cause:         cause,
		FailureReason: reason,
		rawErr:        raw,
	}
}
