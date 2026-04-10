package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/notify"
	"github.com/gosom/google-maps-scraper/web/auth"
)

// mockSender captures the last SupportRequest it received.
type mockSender struct {
	lastReq notify.SupportRequest
	err     error
}

func (m *mockSender) Send(_ context.Context, req notify.SupportRequest) error {
	m.lastReq = req
	return m.err
}

// newSupportTestHandler creates a SupportHandlers with minimal deps and the given sender.
func newSupportTestHandler(sender notify.Sender) *SupportHandlers {
	return &SupportHandlers{
		Deps:   Dependencies{},
		Sender: sender,
	}
}

// postSupport builds a POST /api/v1/support request with the given JSON body and optional userID in context.
func postSupport(t *testing.T, body string, userID string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/support", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		ctx := context.WithValue(req.Context(), auth.UserIDKey, userID)
		req = req.WithContext(ctx)
	}
	return req, httptest.NewRecorder()
}

func TestSupport_ValidRequest(t *testing.T) {
	sender := &mockSender{}
	h := newSupportTestHandler(sender)

	body := `{"category":"bug","subject":"Test","message":"Something broke in my scraping job please help"}`
	req, rr := postSupport(t, body, "user_123")

	h.SubmitSupportRequest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if sender.lastReq.Category != "bug" {
		t.Errorf("expected category 'bug', got %q", sender.lastReq.Category)
	}
	if sender.lastReq.UserID != "user_123" {
		t.Errorf("expected user_id 'user_123', got %q", sender.lastReq.UserID)
	}
}

func TestSupport_MissingCategory(t *testing.T) {
	sender := &mockSender{}
	h := newSupportTestHandler(sender)

	body := `{"message":"Something broke in my scraping job please help"}`
	req, rr := postSupport(t, body, "user_123")

	h.SubmitSupportRequest(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSupport_InvalidCategory(t *testing.T) {
	sender := &mockSender{}
	h := newSupportTestHandler(sender)

	body := `{"category":"hacking","message":"Something broke in my scraping job please help"}`
	req, rr := postSupport(t, body, "user_123")

	h.SubmitSupportRequest(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSupport_MessageTooShort(t *testing.T) {
	sender := &mockSender{}
	h := newSupportTestHandler(sender)

	body := `{"category":"bug","message":"ab"}`
	req, rr := postSupport(t, body, "user_123")

	h.SubmitSupportRequest(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSupport_MessageTooLong(t *testing.T) {
	sender := &mockSender{}
	h := newSupportTestHandler(sender)

	longMsg := strings.Repeat("a", 5001)
	body := `{"category":"bug","message":"` + longMsg + `"}`
	req, rr := postSupport(t, body, "user_123")

	h.SubmitSupportRequest(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSupport_UnknownField(t *testing.T) {
	sender := &mockSender{}
	h := newSupportTestHandler(sender)

	body := `{"category":"bug","message":"valid message here please","evil_field":"injected"}`
	req, rr := postSupport(t, body, "user_123")

	h.SubmitSupportRequest(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSupport_NoAuth(t *testing.T) {
	sender := &mockSender{}
	h := newSupportTestHandler(sender)

	body := `{"category":"bug","message":"valid message here please"}`
	req, rr := postSupport(t, body, "") // no user ID

	h.SubmitSupportRequest(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSupport_SenderFailure(t *testing.T) {
	sender := &mockSender{err: errors.New("resend is down")}
	h := newSupportTestHandler(sender)

	body := `{"category":"bug","message":"valid message here please"}`
	req, rr := postSupport(t, body, "user_123")

	h.SubmitSupportRequest(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
	// Verify the error message is generic (not "resend is down")
	var apiErr struct{ Message string }
	_ = json.NewDecoder(rr.Body).Decode(&apiErr)
	if strings.Contains(apiErr.Message, "resend") {
		t.Error("error message should not leak internal details")
	}
}

func TestSupport_SubjectSanitization(t *testing.T) {
	sender := &mockSender{}
	h := newSupportTestHandler(sender)

	body := `{"category":"bug","subject":"Test\r\nBCC: evil@attacker.com","message":"valid message here please"}`
	req, rr := postSupport(t, body, "user_123")

	h.SubmitSupportRequest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// Verify control chars were stripped
	if strings.ContainsAny(sender.lastReq.Subject, "\r\n") {
		t.Errorf("subject should have newlines stripped, got %q", sender.lastReq.Subject)
	}
}

func TestSanitizeSubject(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Normal Subject", "Normal Subject"},
		{"Has\r\nnewlines", "Hasnewlines"},
		{"Has\x00null", "Hasnull"},
		{"  trimmed  ", "trimmed"},
		{"\tTabs\tInside", "TabsInside"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeSubject(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeSubject(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
