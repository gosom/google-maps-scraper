package s3uploader

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_RetryerHasReasonableMaxBackoff guards against the regression where
// retry.StandardOptions.MaxBackoff was set to the integer literal 20 — which
// is 20 nanoseconds, not 20 seconds — effectively disabling retry backoff.
func TestNew_RetryerHasReasonableMaxBackoff(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	u, err := New("dummy-key", "dummy-secret", "us-east-1", logger)
	require.NoError(t, err)
	require.NotNil(t, u)

	got := u.retryerMaxBackoffForTest()
	assert.GreaterOrEqual(t, got, 1*time.Second, "MaxBackoff must be at least 1s, got %s", got)
}
