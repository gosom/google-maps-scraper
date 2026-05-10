package s3uploader

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestS3Uploader_LoggerDI asserts that s3uploader.New accepts a *slog.Logger
// and tags it with module=s3uploader. The tag is `module`, not `component`,
// because main.go reserves `component` for the runner identity — see
// web/web.go for the same convention.
func TestS3Uploader_LoggerDI(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// New will attempt to load AWS config; with dummy creds it will succeed
	// (static credentials provider doesn't validate at construction time).
	u, err := New(
		WithCredentials("dummy-key", "dummy-secret"),
		WithRegion("us-east-1"),
		WithLogger(logger),
	)
	require.NoError(t, err)
	require.NotNil(t, u)

	u.log.Info("test_s3_logger")
	out := buf.String()
	if !strings.Contains(out, "module=s3uploader") {
		t.Errorf("expected 'module=s3uploader' in logger output, got: %s", out)
	}
	if strings.Contains(out, "component=s3uploader") {
		t.Errorf("s3uploader must not tag itself as component=… (reserved for runner level), got: %s", out)
	}
}
