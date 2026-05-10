package webrunner

import (
	"context"
	"errors"
)

// classifyOutcome maps the raw outputs of a scrape run to a typed JobOutcome.
//
// Inputs:
//   - mateErr: the error returned by mate.Start (nil = clean exit).
//   - userInitiatedCancel: true when the cancellation was triggered by the user
//     hitting Cancel via the API (i.e. the job's DB status transitioned through
//     "aborting" before the context was cancelled).
//   - naturalCompletion: true when the cancel that propagated into mate.Start
//     came from the exit monitor signalling that all places-found were
//     scraped — i.e. the cancel is the *successful* termination path, not an
//     abort. Without this flag, every unlimited-mode job that ran to
//     completion would be misclassified as "partial" because the exit
//     monitor's job-done signal lands in mate.Start as context.Canceled.
//     Mutually exclusive with userInitiatedCancel by construction (the
//     monitor only fires when work is done; user-cancel goes through a
//     different code path that flips the DB status first).
//   - resultCount: number of result rows written by the time the run ended.
//   - seedErr: the last terminal error from a seed URL, used to produce a
//     user-facing failure_reason when all seeds failed.
//
// Pure function: no I/O, no goroutines, no global state. Safe to call from any
// goroutine and trivial to unit-test without mocks.
func classifyOutcome(mateErr error, userInitiatedCancel, naturalCompletion bool, resultCount int, seedErr error) JobOutcome {
	if mateErr == nil {
		return OutcomeSuccess(resultCount)
	}
	switch {
	case errors.Is(mateErr, context.Canceled):
		// The exit monitor's "all places done" signal cancels mateCtx as
		// the normal success-path termination. Treat that exactly like a
		// clean mate.Start return.
		if naturalCompletion {
			return OutcomeSuccess(resultCount)
		}
		if userInitiatedCancel {
			if resultCount > 0 {
				return OutcomePartial(resultCount)
			}
			return OutcomeUserCancelled()
		}
		if resultCount > 0 {
			return OutcomePartial(resultCount)
		}
		if seedErr != nil {
			return OutcomeFailed(CauseSeedExhausted, sanitizeSeedError(seedErr), seedErr)
		}
		return OutcomeFailed(CauseSeedExhausted, "Scraping aborted before any results were collected", nil)
	case errors.Is(mateErr, context.DeadlineExceeded):
		if resultCount > 0 {
			return OutcomePartial(resultCount)
		}
		return OutcomeFailed(CauseHardTimeout, "job timed out with 0 results", mateErr)
	default:
		return OutcomeFailed(CauseRuntimeError, "job failed due to a runtime error", mateErr)
	}
}
