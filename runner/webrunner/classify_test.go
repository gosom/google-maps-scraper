package webrunner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := classifyOutcome(tc.mateErr, tc.userInitiatedCancel, tc.resultCount, tc.seedErr)

			require.Equal(t, tc.wantStatus, got.Status, "Status mismatch")
			require.Equal(t, tc.wantCause, got.Cause, "Cause mismatch")
			assert.Equal(t, tc.wantFailureReason, got.FailureReason, "FailureReason mismatch")
		})
	}
}
