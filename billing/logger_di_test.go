package billing

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestBillingService_LoggerDI asserts that billing.New accepts a *slog.Logger
// and uses it (module=billing tag visible in logged output).
//
// The tag is `module`, not `component`: `component` is reserved by main.go for
// the runner identity (filerunner, webrunner, …). Tagging billing with the
// same key would silently shadow the runner tag in most log aggregators.
func TestBillingService_LoggerDI(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	svc := New(nil, nil, "", nil, nil, logger)
	if svc == nil {
		t.Fatal("billing.New returned nil")
	}

	svc.logger.Info("test_billing_logger")
	out := buf.String()
	if !strings.Contains(out, "module=billing") {
		t.Errorf("expected 'module=billing' in logger output, got: %s", out)
	}
	if strings.Contains(out, "component=billing") {
		t.Errorf("billing must not tag itself as component=… (reserved for runner level), got: %s", out)
	}
}
