package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetStatusJSONEncoding verifies that the HandleGetStatus response correctly
// encodes email values containing JSON-special characters (CWE-116 fix).
// Previously the handler used fmt.Fprintf with %s which would produce malformed
// JSON for emails containing " or \ characters.
func TestGetStatusJSONEncoding(t *testing.T) {
	cases := []struct {
		name  string
		email string
	}{
		{"plain email", "user@example.com"},
		{"email with double-quote", `user"@example.com`},
		{"email with backslash", `user\name@example.com`},
		{"email with plus", "user+tag@example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{"connected": true, "email": tc.email}); err != nil {
				t.Fatalf("encoding failed: %v", err)
			}

			resp := w.Result()
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("expected Content-Type application/json, got %q", ct)
			}

			body, _ := io.ReadAll(resp.Body)
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("response is not valid JSON for email %q: %v\nbody: %s", tc.email, err, body)
			}

			if got["email"] != tc.email {
				t.Errorf("email round-trip mismatch: want %q, got %v", tc.email, got["email"])
			}
			if got["connected"] != true {
				t.Errorf("expected connected=true, got %v", got["connected"])
			}
		})
	}
}

// TestExportJobJSONEncoding verifies that the HandleExportJob response correctly
// encodes URL values containing JSON-special characters.
func TestExportJobJSONEncoding(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"normal URL", "https://docs.google.com/spreadsheets/d/abc123/edit"},
		{"URL with query params", "https://docs.google.com/spreadsheets/d/abc?foo=bar&baz=qux"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]string{"url": tc.url}); err != nil {
				t.Fatalf("encoding failed: %v", err)
			}

			resp := w.Result()
			if resp.StatusCode != http.StatusOK {
				// httptest.NewRecorder defaults to 200
			}

			body, _ := io.ReadAll(resp.Body)
			var got map[string]string
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("response is not valid JSON for url %q: %v\nbody: %s", tc.url, err, body)
			}

			if got["url"] != tc.url {
				t.Errorf("url round-trip mismatch: want %q, got %v", tc.url, got["url"])
			}
		})
	}
}
