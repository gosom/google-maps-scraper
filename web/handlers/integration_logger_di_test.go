package handlers

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/appenv"
)

// TestIntegrationHandler_LoggerDI asserts that NewIntegrationHandler accepts
// a *slog.Logger and uses it (component attribute visible in output).
func TestIntegrationHandler_LoggerDI(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	h := NewIntegrationHandler(nil, nil, nil, nil, appenv.Environment(0), logger)
	if h == nil {
		t.Fatal("NewIntegrationHandler returned nil")
	}

	// Log something through the injected logger to verify the component attr is set.
	h.log.Info("test_integration_logger")
	if !strings.Contains(buf.String(), "component=integration") {
		t.Errorf("expected 'component=integration' in logger output, got: %s", buf.String())
	}
}
