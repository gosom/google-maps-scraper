package webrunner

import (
	"strings"
	"testing"

	"github.com/gosom/google-maps-scraper/pkg/appenv"
	pkgconfig "github.com/gosom/google-maps-scraper/pkg/config"
	"github.com/gosom/google-maps-scraper/runner"
)

// TestValidate_FailsInProductionWhenEncryptionKeyMissing covers the
// S-C5 / audit M-7 fail-fast guard. Without ENCRYPTION_KEY in production,
// integration credentials would be stored as plaintext (web/web.go silently
// downgrades). pkg/config.Validate() enforces this before any runner starts.
func TestValidate_FailsInProductionWhenEncryptionKeyMissing(t *testing.T) {
	cfg := &pkgconfig.Config{
		AppEnv:         appenv.Production,
		ClerkSecretKey: "sk_test_clerk",
		Stripe: pkgconfig.StripeConfig{
			SecretKey:     "sk_test_stripe",
			WebhookSecret: "whsec_test",
		},
		AllowedOrigins: []string{"https://example.com"},
		EncryptionKey:  "",
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when ENCRYPTION_KEY is missing in production, got nil")
	}
	if !strings.Contains(err.Error(), "ENCRYPTION_KEY") {
		t.Errorf("expected error to mention ENCRYPTION_KEY, got: %v", err)
	}
}

// TestValidate_FailsInProductionWhenStripeWebhookSecretMissing covers
// the STRIPE_WEBHOOK_SECRET guard. Documented here so the test net for
// the production fail-fast block is complete.
func TestValidate_FailsInProductionWhenStripeWebhookSecretMissing(t *testing.T) {
	cfg := &pkgconfig.Config{
		AppEnv:         appenv.Production,
		ClerkSecretKey: "sk_test_clerk",
		Stripe: pkgconfig.StripeConfig{
			SecretKey:     "sk_test_stripe",
			WebhookSecret: "",
		},
		AllowedOrigins: []string{"https://example.com"},
		EncryptionKey:  "0123456789abcdef0123456789abcdef",
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when STRIPE_WEBHOOK_SECRET is missing in production, got nil")
	}
	if !strings.Contains(err.Error(), "STRIPE_WEBHOOK_SECRET") {
		t.Errorf("expected error to mention STRIPE_WEBHOOK_SECRET, got: %v", err)
	}
}

// TestValidate_DoesNotFailInDevelopmentWhenSecretsMissing verifies
// that the fail-fast block is gated on APP_ENV=production and does not fire
// in local dev / test environments where these secrets are routinely absent.
func TestValidate_DoesNotFailInDevelopmentWhenSecretsMissing(t *testing.T) {
	cfg := &pkgconfig.Config{
		AppEnv:         appenv.Development,
		ClerkSecretKey: "sk_test_clerk",
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("development environment should NOT trigger the production fail-fast guard, got: %v", err)
	}
}

// TestBuildServerConfig_DoesNotFailInDevelopmentWhenSecretsMissing verifies
// that buildServerConfig does not re-apply production-only validation (which
// now lives entirely in pkg/config.Validate). It should succeed in dev even
// with no Stripe credentials, as long as APIKeyServerSecret is either empty
// or ≥ 32 bytes.
func TestBuildServerConfig_DoesNotFailInDevelopmentWhenSecretsMissing(t *testing.T) {
	// API_KEY_SERVER_SECRET is required at >= 32 bytes when api keys are
	// enabled; supply a long enough value to clear the unrelated guard so
	// this test isolates only the production-secrets check.
	appCfg := &pkgconfig.Config{
		AppEnv:             appenv.Development,
		ClerkSecretKey:     "sk_test_clerk",
		APIKeyServerSecret: []byte("0123456789abcdef0123456789abcdef"),
	}

	// buildServerConfig will return an error from a different code path (it
	// touches DB-backed repos with a nil DB), but it must NOT return the
	// "production mode requires" error. We assert the absence of that
	// specific error string rather than expecting a fully successful build,
	// because the test would otherwise need a real DB + Clerk client to
	// reach the success path.
	_, err := buildServerConfig(&runner.Config{}, nil, nil, appCfg)
	if err != nil && strings.Contains(err.Error(), "production mode requires") {
		t.Errorf("development environment should NOT trigger the production fail-fast guard, got: %v", err)
	}
}

func TestStripeWebhookSecrets_IncludesPreviousSecret(t *testing.T) {
	stripeCfg := pkgconfig.StripeConfig{
		WebhookSecret:         "whsec_current",
		WebhookSecretPrevious: "whsec_previous",
	}

	got := stripeCfg.WebhookSecrets()
	if len(got) != 2 {
		t.Fatalf("expected 2 webhook secrets, got %d (%v)", len(got), got)
	}
	if got[0] != "whsec_current" {
		t.Fatalf("expected current secret first, got %q", got[0])
	}
	if got[1] != "whsec_previous" {
		t.Fatalf("expected previous secret second, got %q", got[1])
	}
}
