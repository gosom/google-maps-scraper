package webrunner

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyOutcome(t *testing.T) {
	t.Parallel()

	proxyErr := errors.New("playwright: net::ERR_PROXY_CONNECTION_FAILED at https://maps.google.com")

	tests := []struct {
		name                string
		mateErr             error
		userInitiatedCancel bool
		resultCount         int
		seedErr             error
		wantStatus          string
		wantCause           TerminalCause
		wantFailureReason   string
		wantReasonNotRaw    string
	}{
		{
			name:        "happy path: no error, results present",
			mateErr:     nil,
			resultCount: 5,
			wantStatus:  "completed",
			wantCause:   CauseSuccess,
		},
		{
			name:              "all seeds terminally failed: canceled with 0 results and seed error",
			mateErr:           context.Canceled,
			resultCount:       0,
			seedErr:           proxyErr,
			wantStatus:        "failed",
			wantCause:         CauseSeedExhausted,
			wantFailureReason: "Scraping aborted: proxy connection failed",
			wantReasonNotRaw:  proxyErr.Error(),
		},
		{
			name:                "user cancelled with results: partial outcome",
			mateErr:             context.Canceled,
			userInitiatedCancel: true,
			resultCount:         3,
			wantStatus:          "completed",
			wantCause:           CausePartial,
		},
		{
			name:                "user cancelled with 0 results: clean cancel",
			mateErr:             context.Canceled,
			userInitiatedCancel: true,
			resultCount:         0,
			wantStatus:          "cancelled",
			wantCause:           CauseUserCancel,
		},
		{
			name:        "deadline exceeded with results: partial outcome",
			mateErr:     context.DeadlineExceeded,
			resultCount: 7,
			wantStatus:  "completed",
			wantCause:   CausePartial,
		},
		{
			name:              "deadline exceeded with 0 results: hard timeout failure",
			mateErr:           context.DeadlineExceeded,
			resultCount:       0,
			wantStatus:        "failed",
			wantCause:         CauseHardTimeout,
			wantFailureReason: "job timed out with 0 results",
		},
		{
			name:              "runtime error: unexpected error from mate",
			mateErr:           errors.New("unexpected internal error"),
			resultCount:       0,
			wantStatus:        "failed",
			wantCause:         CauseRuntimeError,
			wantFailureReason: "job failed due to a runtime error",
		},
		{
			name:              "context.Canceled with 0 results and no seed error: seed exhausted generic",
			mateErr:           context.Canceled,
			resultCount:       0,
			seedErr:           nil,
			wantStatus:        "failed",
			wantCause:         CauseSeedExhausted,
			wantFailureReason: "Scraping aborted before any results were collected",
		},
		{
			name:                "wrapped context.Canceled with results: errors.Is must unwrap",
			mateErr:             fmt.Errorf("transport layer: %w", context.Canceled),
			userInitiatedCancel: false,
			resultCount:         3,
			wantStatus:          "completed",
			wantCause:           CausePartial,
		},
		{
			name:                "system-cancel with results: partial",
			mateErr:             context.Canceled,
			userInitiatedCancel: false,
			resultCount:         4,
			wantStatus:          "completed",
			wantCause:           CausePartial,
		},
		{
			// Defense against a future refactor that swaps errors.Is for
			// pointer equality (mateErr == context.Canceled) — middleware
			// can wrap the cancel multiple times.
			name:                "doubly-wrapped context.Canceled: errors.Is must unwrap to root",
			mateErr:             fmt.Errorf("outer: %w", fmt.Errorf("transport: %w", context.Canceled)),
			userInitiatedCancel: false,
			resultCount:         0,
			seedErr:             nil,
			wantStatus:          "failed",
			wantCause:           CauseSeedExhausted,
			wantFailureReason:   "Scraping aborted before any results were collected",
		},
		{
			// mateErr=nil dominates: even if a stale seedErr was recorded
			// earlier in the run, a clean mate.Start return means the job
			// succeeded. Without this assertion, a refactor that always
			// branches on seedErr would silently downgrade good runs to
			// "failed".
			name:        "happy path with stale seedErr: success still wins",
			mateErr:     nil,
			resultCount: 12,
			seedErr:     errors.New("an old transient seed error"),
			wantStatus:  "completed",
			wantCause:   CauseSuccess,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := classifyOutcome(tc.mateErr, tc.userInitiatedCancel, tc.resultCount, tc.seedErr)

			assert.Equal(t, tc.wantStatus, got.Status, "Status mismatch")
			assert.Equal(t, tc.wantCause, got.Cause, "Cause mismatch")
			assert.Equal(t, tc.wantFailureReason, got.FailureReason, "FailureReason mismatch")
			if tc.wantReasonNotRaw != "" {
				assert.NotContains(t, got.FailureReason, tc.wantReasonNotRaw,
					"FailureReason must NOT contain the raw error verbatim — sanitizer must transform it")
			}
		})
	}
}
