package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/web/auth"
)

// TestScrape_RejectsUnknownFields is the integration counterpart of the
// decodeStrict unit tests in decode_test.go — it exercises the full HTTP
// path from the Scrape handler down through decodeStrict and asserts the
// 422 response. The body adds an `admin_override` field that doesn't
// exist on apiScrapeRequest; before Task 3.1 the silent-drop behavior of
// json.NewDecoder would have accepted it, masking confusion-attack
// payloads.
func TestScrape_RejectsUnknownFields(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	body := `{"name":"test","keywords":["pizza"],"lang":"en","depth":5,"max_results":10,"max_time":60,"admin_override":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.Scrape(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for unknown field, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	// Defense in depth: the response message MUST be generic. Echoing
	// the wrapped json error would expose the user-supplied field name
	// `admin_override` and create an XSS / log-injection vector for
	// malicious field names like `<script>...</script>`.
	if strings.Contains(rr.Body.String(), "admin_override") {
		t.Errorf("response leaked attacker-controlled field name: %s", rr.Body.String())
	}
}

// TestScrape_RejectsTrailingData covers the d.More() arm of decodeStrict
// at the handler level. Two concatenated documents must be rejected with
// 422 — without this, a parser-divergence attacker can sneak the second
// document through, relying on the server reading the first one only.
func TestScrape_RejectsTrailingData(t *testing.T) {
	t.Parallel()

	h := &APIHandlers{Deps: Dependencies{Auth: &auth.AuthMiddleware{}}}
	body := `{"name":"a","keywords":["x"],"lang":"en","depth":5,"max_results":10,"max_time":60}{"name":"b","keywords":["y"],"lang":"en","depth":5,"max_results":10,"max_time":60}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.Scrape(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for trailing data, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}
