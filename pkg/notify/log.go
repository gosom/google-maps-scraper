package notify

import (
	"context"
	"log/slog"
)

// LogSender writes support requests to the logger instead of sending email.
// Used when RESEND_API_KEY is not configured (local development).
type LogSender struct {
	Logger *slog.Logger
}

func (s *LogSender) Send(_ context.Context, req SupportRequest) error {
	s.Logger.Info("support_request_received",
		slog.String("category", req.Category),
		slog.String("subject", req.Subject),
		slog.String("user_id", req.UserID),
		// security: user_email removed — PII; user_id is sufficient for lookup.
		// security: NEVER log req.Message — may contain PII, passwords, API keys
		slog.Int("message_length", len(req.Message)),
	)
	return nil
}
