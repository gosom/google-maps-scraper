package notify

import "context"

// SupportRequest is the enriched payload sent to the notification backend.
// All fields are validated and sanitized before reaching this struct.
type SupportRequest struct {
	// User-provided (validated)
	Category string // one of: bug, feature, billing, account, other
	Subject  string // optional, max 200 chars, control chars stripped
	Message  string // required, 10-5000 chars

	// Server-enriched (never from client)
	UserID        string
	UserEmail     string
	CreditBalance string
	UserAgent     string
}

// Sender delivers support requests to an external system (email, ticket, Slack).
// Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, req SupportRequest) error
}
