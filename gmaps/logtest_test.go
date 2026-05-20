package gmaps

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/logger"
	"github.com/gosom/scrapemate"
)

// newCaptureLogger returns a context whose scrapemate logger writes
// JSON-formatted records to buf. Mirrors the production setup in
// runner/webrunner/webrunner.go where the user-facing job_id and user_id
// are added as With-attributes so every emitted line carries them.
func newCaptureLogger(t *testing.T, userJobID, userID string) (context.Context, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	base := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	withAttrs := base.With(slog.String("job_id", userJobID), slog.String("user_id", userID))
	ctx := scrapemate.ContextWithLogger(context.Background(), logger.NewSlogAdapter(withAttrs))
	return ctx, buf
}

// decodeLogLines parses each newline-delimited JSON record from buf into
// a map. Caller asserts on individual records.
func decodeLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestCaptureLoggerCarriesWithAttributes(t *testing.T) {
	ctx, buf := newCaptureLogger(t, "019e41ff-aaaa-7bbb-cccc-dddd", "user_TEST")

	log := scrapemate.GetLoggerFromContext(ctx)
	log.Warn("smoke_test", "place_url", "https://example.com/place")

	recs := decodeLogLines(t, buf)
	if len(recs) != 1 {
		t.Fatalf("want 1 log record, got %d", len(recs))
	}
	if recs[0]["job_id"] != "019e41ff-aaaa-7bbb-cccc-dddd" {
		t.Errorf("job_id from .With not emitted, got %v", recs[0]["job_id"])
	}
	if recs[0]["user_id"] != "user_TEST" {
		t.Errorf("user_id from .With not emitted, got %v", recs[0]["user_id"])
	}
	if recs[0]["place_url"] != "https://example.com/place" {
		t.Errorf("place_url from call site not emitted, got %v", recs[0]["place_url"])
	}
}
