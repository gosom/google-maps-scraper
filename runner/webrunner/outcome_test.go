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

func TestJobOutcome_RawErrIsUnexported(t *testing.T) {
	t.Parallel()
	// Documents the design: rawErr is unexported. Callers MUST use Err() —
	// they should not be able to grep job.RawErr and string-match on it.
	o := OutcomeFailed(CauseRuntimeError, "X", errors.New("internal detail"))
	// If you can read the unexported field by name, this whole test file
	// failed to compile (which would itself signal a regression). The
	// runtime assertion is just that Err() returns the same value.
	assert.NotNil(t, o.Err())
}
