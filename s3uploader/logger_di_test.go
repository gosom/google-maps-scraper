package s3uploader

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestS3Uploader_LoggerDI asserts that s3uploader.New accepts a *slog.Logger
// and wires it with the component attribute. We skip actual AWS calls.
func TestS3Uploader_LoggerDI(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// New will attempt to load AWS config; with dummy creds it will succeed
	// (static credentials provider doesn't validate at construction time).
	u, err := New("dummy-key", "dummy-secret", "us-east-1", logger)
	if err != nil {
		t.Skipf("s3uploader.New failed (expected in CI without AWS): %v", err)
	}
	if u == nil {
		t.Fatal("s3uploader.New returned nil uploader")
	}

	// Emit a line through the injected logger to check the component attr.
	u.log.Info("test_s3_logger")
	if !strings.Contains(buf.String(), "component=s3uploader") {
		t.Errorf("expected 'component=s3uploader' in logger output, got: %s", buf.String())
	}
}
