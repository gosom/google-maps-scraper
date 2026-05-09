package webrunner

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gosom/google-maps-scraper/models"
)

// Each constructor enforces an invariant about the (Status, Cause, FailureReason,
// rawErr) tuple so callers cannot accidentally produce illegal combinations like
// "Status=Completed but Cause=RuntimeError". Tests pin these invariants.

func TestOutcomeSuccess(t *testing.T) {
	t.Parallel()
	o := OutcomeSuccess(42)
	assert.Equal(t, models.StatusCompleted, o.Status)
	assert.Equal(t, CauseSuccess, o.Cause)
	assert.Equal(t, 42, o.ResultCount)
	assert.Empty(t, o.FailureReason, "successful outcome must have no failure reason")
	assert.NoError(t, o.Err(), "successful outcome must not carry a raw error")
}

func TestOutcomePartial_TimeoutWithResults(t *testing.T) {
	t.Parallel()
	// Timeout WITH results is treated as success per existing webrunner.go:1136-1139.
	// OutcomePartial captures this case explicitly so the outer worker can log
	// "job_scrape_partial" if it ever wants to distinguish — today it logs
	// "job_scrape_succeeded" for any Status==Completed.
	o := OutcomePartial(7)
	assert.Equal(t, models.StatusCompleted, o.Status, "partial-with-results is still Completed")
	assert.Equal(t, CausePartial, o.Cause)
	assert.Equal(t, 7, o.ResultCount)
}

func TestOutcomeUserCancelled(t *testing.T) {
	t.Parallel()
	o := OutcomeUserCancelled()
	assert.Equal(t, models.StatusCancelled, o.Status)
	assert.Equal(t, CauseUserCancel, o.Cause)
	assert.Empty(t, o.FailureReason, "user cancellation is not a failure")
}

func TestOutcomeFailed_CarriesReasonAndRawErr(t *testing.T) {
	t.Parallel()
	raw := errors.New("playwright: net::ERR_PROXY_CONNECTION_FAILED at https://x")
	o := OutcomeFailed(CauseSeedExhausted, "Proxy connection failed", raw)
	assert.Equal(t, models.StatusFailed, o.Status)
	assert.Equal(t, CauseSeedExhausted, o.Cause)
	assert.Equal(t, "Proxy connection failed", o.FailureReason)
	require.ErrorIs(t, o.Err(), raw, "raw error must be retrievable for support logging")
}

// TestOutcomeFailed_NilRawErrIsAllowed pins that OutcomeFailed accepts a nil
// raw error — used by the "Scraping aborted before any results were
// collected" branch in classifyOutcome where there's no underlying error to
// surface, only a sanitized message. Without this, a future change that
// nil-checks raw at construction time would silently break that path.
func TestOutcomeFailed_NilRawErrIsAllowed(t *testing.T) {
	t.Parallel()
	o := OutcomeFailed(CauseSeedExhausted, "Scraping aborted before any results were collected", nil)
	assert.Equal(t, models.StatusFailed, o.Status)
	assert.Equal(t, "Scraping aborted before any results were collected", o.FailureReason)
	assert.NoError(t, o.Err(), "nil raw must round-trip as nil from Err()")
}
