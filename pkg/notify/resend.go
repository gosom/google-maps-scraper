package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ResendSender sends support emails via the Resend REST API.
// It does NOT use the Resend Go SDK — just net/http + JSON,
// consistent with how the codebase calls Stripe's API.
type ResendSender struct {
	apiKey     string
	from       string
	to         string
	baseURL    string // default "https://api.resend.com"; override in tests
	httpClient *http.Client
}

// NewResendSender creates a Resend email sender.
// apiKey: Resend API key (re_...)
// from: sender address (e.g., "BrezelScraper Support <noreply@brezel.ai>")
// to: recipient address (e.g., "support@brezel.ai")
func NewResendSender(apiKey, from, to string) *ResendSender {
	return &ResendSender{
		apiKey:  apiKey,
		from:    from,
		to:      to,
		baseURL: "https://api.resend.com",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	ReplyTo string   `json:"reply_to,omitempty"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
}

func (s *ResendSender) Send(ctx context.Context, req SupportRequest) error {
	body := fmt.Sprintf(
		"Category: %s\nSubject: %s\n\n%s\n\n---\nUser ID: %s\nEmail: %s\nCredit Balance: %s\nUser Agent: %s",
		req.Category, req.Subject, req.Message,
		req.UserID, req.UserEmail, req.CreditBalance, req.UserAgent,
	)

	subject := fmt.Sprintf("[Support: %s] %s", req.Category, req.Subject)
	if req.Subject == "" {
		subject = fmt.Sprintf("[Support: %s] No subject", req.Category)
	}

	payload := resendPayload{
		From:    s.from,
		To:      []string{s.to},
		ReplyTo: req.UserEmail,
		Subject: subject,
		Text:    body,
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: marshal payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/emails", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("notify: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("notify: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// security: LimitReader prevents unbounded read from Resend error responses (CWE-400)
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("notify: resend returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
