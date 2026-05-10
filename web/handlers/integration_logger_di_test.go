package handlers

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/appenv"
	pkgconfig "github.com/gosom/google-maps-scraper/pkg/config"
)

// TestIntegrationHandler_LoggerDI asserts that NewIntegrationHandler accepts
// a *slog.Logger and tags it with service=integration. The tag is `service`,
// not `component`, because the integration handler is a 3rd-tier component
// (under the api module under the runner) — using `component` here would
// shadow the runner-level component tag. See web/web.go for the rule.
func TestIntegrationHandler_LoggerDI(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	h := NewIntegrationHandler(nil, nil, nil, nil, appenv.Environment(0), pkgconfig.GoogleConfig{}, logger)
	if h == nil {
		t.Fatal("NewIntegrationHandler returned nil")
	}

	h.log.Info("test_integration_logger")
	out := buf.String()
	if !strings.Contains(out, "service=integration") {
		t.Errorf("expected 'service=integration' in logger output, got: %s", out)
	}
	if strings.Contains(out, "component=integration") {
		t.Errorf("integration handler must not tag itself as component=… (reserved for runner level), got: %s", out)
	}
}
