package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResendSender_Send_Success(t *testing.T) {
	var gotPayload resendPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("expected auth header 'Bearer test-key', got %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("expected json content type, got %q", got)
		}

		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotPayload); err != nil {
			t.Fatalf("failed to unmarshal request body: %v", err)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"email_123"}`))
	}))
	defer srv.Close()

	sender := NewResendSender("test-key", "from@test.com", "to@test.com")
	sender.httpClient = srv.Client()
	sender.baseURL = srv.URL

	err := sender.Send(context.Background(), SupportRequest{
		Category:      "bug",
		Subject:       "Test Subject",
		Message:       "Something broke in my scraping job",
		UserID:        "user_123",
		UserEmail:     "user@example.com",
		CreditBalance: "$4.50",
		UserAgent:     "Mozilla/5.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify payload
	if gotPayload.From != "from@test.com" {
		t.Errorf("expected from 'from@test.com', got %q", gotPayload.From)
	}
	if len(gotPayload.To) != 1 || gotPayload.To[0] != "to@test.com" {
		t.Errorf("expected to ['to@test.com'], got %v", gotPayload.To)
	}
	if gotPayload.ReplyTo != "user@example.com" {
		t.Errorf("expected reply_to 'user@example.com', got %q", gotPayload.ReplyTo)
	}
	if gotPayload.Subject != "[Support: bug] Test Subject" {
		t.Errorf("expected subject '[Support: bug] Test Subject', got %q", gotPayload.Subject)
	}
}

func TestResendSender_Send_NoSubject(t *testing.T) {
	var gotPayload resendPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"email_456"}`))
	}))
	defer srv.Close()

	sender := NewResendSender("test-key", "from@test.com", "to@test.com")
	sender.httpClient = srv.Client()
	sender.baseURL = srv.URL

	err := sender.Send(context.Background(), SupportRequest{
		Category: "billing",
		Message:  "I have a billing question",
		UserID:   "user_123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPayload.Subject != "[Support: billing] No subject" {
		t.Errorf("expected fallback subject, got %q", gotPayload.Subject)
	}
}

func TestResendSender_Send_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_api_key"}`))
	}))
	defer srv.Close()

	sender := NewResendSender("bad-key", "from@test.com", "to@test.com")
	sender.httpClient = srv.Client()
	sender.baseURL = srv.URL

	err := sender.Send(context.Background(), SupportRequest{
		Category: "bug",
		Message:  "test message content here",
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestResendSender_Send_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := NewResendSender("test-key", "from@test.com", "to@test.com")
	sender.httpClient = srv.Client()
	sender.baseURL = srv.URL

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := sender.Send(ctx, SupportRequest{
		Category: "bug",
		Message:  "this should fail due to cancelled context",
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
