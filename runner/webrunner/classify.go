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
//   - resultCount: number of result rows written by the time the run ended.
//   - seedErr: the last terminal error from a seed URL, used to produce a
//     user-facing failure_reason when all seeds failed.
//
// Pure function: no I/O, no goroutines, no global state. Safe to call from any
// goroutine and trivial to unit-test without mocks.
func classifyOutcome(mateErr error, userInitiatedCancel bool, resultCount int, seedErr error) JobOutcome {
	if mateErr == nil {
		return OutcomeSuccess(resultCount)
	}
	switch {
	case errors.Is(mateErr, context.Canceled):
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
