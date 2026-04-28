package billing

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestBillingService_LoggerDI asserts that billing.New accepts a *slog.Logger
// and uses it (component attribute visible in logged output).
func TestBillingService_LoggerDI(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	svc := New(nil, nil, "", nil, nil, logger)
	if svc == nil {
		t.Fatal("billing.New returned nil")
	}

	// Log something through the injected logger to verify component attr.
	svc.logger.Info("test_billing_logger")
	if !strings.Contains(buf.String(), "component=billing") {
		t.Errorf("expected 'component=billing' in logger output, got: %s", buf.String())
	}
}
