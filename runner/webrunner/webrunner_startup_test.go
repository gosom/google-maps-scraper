package webrunner

import (
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/runner"
)

// TestBuildServerConfig_FailsInProductionWhenEncryptionKeyMissing covers the
// S-C5 / audit M-7 fail-fast guard. Without ENCRYPTION_KEY in production,
// integration credentials would be stored as plaintext (web/web.go silently
// downgrades). The buildServerConfig guard refuses to start the server.
func TestBuildServerConfig_FailsInProductionWhenEncryptionKeyMissing(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_clerk")
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_stripe")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_test")
	t.Setenv("ALLOWED_ORIGINS", "https://example.com")
	t.Setenv("ENCRYPTION_KEY", "")

	_, err := buildServerConfig(&runner.Config{}, nil, nil)
	if err == nil {
		t.Fatal("expected error when ENCRYPTION_KEY is missing in production, got nil")
	}
	if !strings.Contains(err.Error(), "ENCRYPTION_KEY") {
		t.Errorf("expected error to mention ENCRYPTION_KEY, got: %v", err)
	}
}

// TestBuildServerConfig_FailsInProductionWhenStripeWebhookSecretMissing covers
// the existing pre-S-C5 STRIPE_WEBHOOK_SECRET guard. Documented here so the
// test net for the production fail-fast block is complete.
func TestBuildServerConfig_FailsInProductionWhenStripeWebhookSecretMissing(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_clerk")
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_stripe")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	t.Setenv("ALLOWED_ORIGINS", "https://example.com")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")

	_, err := buildServerConfig(&runner.Config{}, nil, nil)
	if err == nil {
		t.Fatal("expected error when STRIPE_WEBHOOK_SECRET is missing in production, got nil")
	}
	if !strings.Contains(err.Error(), "STRIPE_WEBHOOK_SECRET") {
		t.Errorf("expected error to mention STRIPE_WEBHOOK_SECRET, got: %v", err)
	}
}

// TestBuildServerConfig_DoesNotFailInDevelopmentWhenSecretsMissing verifies
// that the fail-fast block is gated on APP_ENV=production and does not fire
// in local dev / test environments where these secrets are routinely absent.
func TestBuildServerConfig_DoesNotFailInDevelopmentWhenSecretsMissing(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_clerk")
	t.Setenv("STRIPE_SECRET_KEY", "")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "")
	t.Setenv("ALLOWED_ORIGINS", "")
	t.Setenv("ENCRYPTION_KEY", "")
	// API_KEY_SERVER_SECRET is required at >= 32 bytes when api keys are
	// enabled; supply a long enough value to clear the unrelated guard so
	// this test isolates only the production-secrets check.
	t.Setenv("API_KEY_SERVER_SECRET", "0123456789abcdef0123456789abcdef")

	// buildServerConfig will return an error from a different code path (it
	// touches DB-backed repos with a nil DB), but it must NOT return the
	// "production mode requires" error. We assert the absence of that
	// specific error string rather than expecting a fully successful build,
	// because the test would otherwise need a real DB + Clerk client to
	// reach the success path.
	_, err := buildServerConfig(&runner.Config{}, nil, nil)
	if err != nil && strings.Contains(err.Error(), "production mode requires") {
		t.Errorf("development environment should NOT trigger the production fail-fast guard, got: %v", err)
	}
}
