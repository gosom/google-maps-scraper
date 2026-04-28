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
	u, err := New(
		WithCredentials("dummy-key", "dummy-secret"),
		WithRegion("us-east-1"),
		WithLogger(logger),
	)
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
	u, err := New(
		WithCredentials("dummy-key", "dummy-secret"),
		WithRegion("us-east-1"),
	)
	require.NoError(t, err)
	require.NotNil(t, u)

	assert.Equal(t, "standard", u.retryerModeForTest())
}

// TestNew_RequiresCredentials verifies the constructor refuses to build
// an Uploader when credentials are missing.
func TestNew_RequiresCredentials(t *testing.T) {
	_, err := New(WithRegion("us-east-1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access key and secret key are required")
}

// TestNew_DOSpacesEndpoint verifies that constructing with a DigitalOcean
// Spaces endpoint succeeds. We can't easily inspect BaseEndpoint on the
// client without unsafe access, but constructing without error against a
// non-AWS host is the smoke test; functional behaviour is covered by the
// stub-client tests added in a later chunk.
func TestNew_DOSpacesEndpoint(t *testing.T) {
	u, err := New(
		WithCredentials("DO_KEY", "DO_SECRET"),
		WithRegion("nyc3"),
		WithEndpoint("https://nyc3.digitaloceanspaces.com"),
	)
	require.NoError(t, err)
	require.NotNil(t, u)
}
