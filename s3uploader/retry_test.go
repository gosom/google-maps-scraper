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

// TestNew_UsesStandardRetryerNotAdaptive guards against the contradictory
// retry config where config.WithRetryMode(Adaptive) was silently overridden
// by retry.NewStandard. We pick the standard retryer explicitly because
// adaptive mode's client-side throttle handling is tuned for AWS S3 and
// not reliable against S3-compatible stores like DigitalOcean Spaces.
func TestNew_UsesStandardRetryerNotAdaptive(t *testing.T) {
	u, err := New("dummy-key", "dummy-secret", "us-east-1", nil)
	require.NoError(t, err)
	require.NotNil(t, u)

	assert.Equal(t, "standard", u.retryerModeForTest())
}
