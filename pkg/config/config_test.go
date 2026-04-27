package config_test

import (
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/pkg/appenv"
	"github.com/gosom/google-maps-scraper/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validKey32 = "12345678901234567890123456789012" // exactly 32 bytes
)

// withMinimumEnv provides the minimum required environment variables so that
// Load() does not fail on required fields. Tests that check specific defaults
// can build on top of this.
func withMinimumEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DSN", "postgres://test:test@localhost:5432/testdb")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_placeholder")
}

// withFullProductionEnv sets all vars required for a valid production config.
func withFullProductionEnv(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("STRIPE_SECRET_KEY", "sk_live_xxx")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_xxx")
	t.Setenv("ALLOWED_ORIGINS", "https://example.com")
	t.Setenv("ENCRYPTION_KEY", validKey32)
	t.Setenv("API_KEY_SERVER_SECRET", validKey32)
}

func TestLoad_Defaults(t *testing.T) {
	withMinimumEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err)

	// Core / runtime defaults
	assert.Equal(t, appenv.Development, cfg.AppEnv, "APP_ENV should default to Development")
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, ":9090", cfg.InternalAddr)
	assert.Equal(t, "./webdata", cfg.DataFolder)
	assert.Equal(t, 0, cfg.Concurrency)
	assert.Equal(t, false, cfg.DisableTelemetry)

	// Log nested struct defaults
	assert.Equal(t, "both", cfg.Log.Output)
	assert.Equal(t, "", cfg.Log.FilePath)
	assert.Equal(t, "logs", cfg.Log.Dir)
	assert.Equal(t, "brezel-api.log", cfg.Log.FileName)
	assert.Equal(t, 100, cfg.Log.MaxSizeMB)
	assert.Equal(t, 7, cfg.Log.RetentionDays)

	// Database connection pool defaults
	assert.Equal(t, "postgres://test:test@localhost:5432/testdb", cfg.DSN)
	assert.Equal(t, "", cfg.MigrationDSN)
	assert.Equal(t, 25, cfg.DB.MaxOpenConns)
	assert.Equal(t, 10, cfg.DB.MaxIdleConns)
	assert.Equal(t, 5*time.Minute, cfg.DB.ConnMaxLifetime)
	assert.Equal(t, 2*time.Minute, cfg.DB.ConnMaxIdleTime)

	// Auth
	assert.Equal(t, "sk_test_placeholder", cfg.ClerkSecretKey)
	assert.Nil(t, cfg.APIKeyServerSecret)
	assert.Equal(t, "", cfg.EncryptionKey)

	// Stripe defaults
	assert.Equal(t, "", cfg.Stripe.SecretKey)
	assert.Equal(t, "", cfg.Stripe.WebhookSecret)
	assert.Equal(t, "", cfg.Stripe.WebhookSecretPrevious)
	assert.Nil(t, cfg.Stripe.WebhookAllowedCIDRs)

	// CORS
	assert.Nil(t, cfg.AllowedOrigins)

	// Stuck-job defaults
	assert.Equal(t, 10, cfg.StuckJobCheckIntervalMinutes)
	assert.Equal(t, 4, cfg.StuckJobTimeoutHours)
	assert.Equal(t, 90, cfg.WebhookEventRetentionDays)

	// AWS defaults
	assert.Equal(t, "", cfg.AWS.AccessKeyID)
	assert.Equal(t, "", cfg.AWS.SecretAccessKey)
	assert.Equal(t, "us-east-1", cfg.AWS.Region)
	assert.Equal(t, "", cfg.S3BucketName)

	// Google defaults
	assert.Equal(t, "", cfg.Google.ClientID)
	assert.Equal(t, "", cfg.Google.ClientSecret)
	assert.Equal(t, "", cfg.Google.RedirectURL)
	assert.Equal(t, "", cfg.Google.CookiesFile)

	// External services defaults
	assert.Equal(t, "", cfg.WebshareAPIKey)
	assert.Equal(t, "", cfg.ResendAPIKey)
	assert.Nil(t, cfg.Proxies)

	// Build metadata defaults
	assert.Equal(t, "", cfg.Build.GitCommit)
	assert.Equal(t, "", cfg.Build.BuildDate)
	assert.Equal(t, "", cfg.Build.Version)
	assert.Equal(t, "development", cfg.Build.Environment)
}

func setupDevShort(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("APP_ENV", "dev")
}

func setupDevelopment(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("APP_ENV", "development")
}

func setupStaging(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("APP_ENV", "staging")
}

func setupProdShort(t *testing.T) {
	t.Helper()
	withFullProductionEnv(t)
	t.Setenv("APP_ENV", "prod")
}

func setupInvalidEnv(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("APP_ENV", "invalid_env")
}

func TestLoad_AppEnvParsing(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T)
		expected appenv.Environment
		wantErr  bool
	}{
		{
			name:     "empty string defaults to development",
			setup:    withMinimumEnv,
			expected: appenv.Development,
		},
		{
			name:     "development string",
			setup:    setupDevelopment,
			expected: appenv.Development,
		},
		{
			name:     "dev short form",
			setup:    setupDevShort,
			expected: appenv.Development,
		},
		{
			name:     "staging string",
			setup:    setupStaging,
			expected: appenv.Staging,
		},
		{
			name:     "production string (full config)",
			setup:    withFullProductionEnv,
			expected: appenv.Production,
		},
		{
			name:     "prod short form (full config)",
			setup:    setupProdShort,
			expected: appenv.Production,
		},
		{
			name:    "invalid value errors",
			setup:   setupInvalidEnv,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)

			cfg, err := config.Load()
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, cfg.AppEnv)
		})
	}
}

func TestLoad_RequiredFieldsMissing(t *testing.T) {
	t.Run("missing DSN fails", func(t *testing.T) {
		t.Setenv("CLERK_SECRET_KEY", "sk_test_placeholder")

		_, err := config.Load()
		require.Error(t, err)
	})

	t.Run("missing CLERK_SECRET_KEY fails", func(t *testing.T) {
		t.Setenv("DSN", "postgres://test:test@localhost/db")

		_, err := config.Load()
		require.Error(t, err)
	})
}

func TestStripe_WebhookSecrets(t *testing.T) {
	t.Run("both secrets non-empty returns both", func(t *testing.T) {
		withMinimumEnv(t)
		t.Setenv("STRIPE_SECRET_KEY", "sk_test_abc")
		t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_current")
		t.Setenv("STRIPE_WEBHOOK_SECRET_PREVIOUS", "whsec_previous")

		cfg, err := config.Load()
		require.NoError(t, err)

		secrets := cfg.Stripe.WebhookSecrets()
		assert.Equal(t, []string{"whsec_current", "whsec_previous"}, secrets)
	})

	t.Run("empty previous omitted", func(t *testing.T) {
		withMinimumEnv(t)
		t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_current")

		cfg, err := config.Load()
		require.NoError(t, err)

		secrets := cfg.Stripe.WebhookSecrets()
		assert.Equal(t, []string{"whsec_current"}, secrets)
	})

	t.Run("both empty returns empty", func(t *testing.T) {
		withMinimumEnv(t)

		cfg, err := config.Load()
		require.NoError(t, err)

		secrets := cfg.Stripe.WebhookSecrets()
		assert.Empty(t, secrets)
	})
}

func TestValidate_NonProductionAlwaysPasses(t *testing.T) {
	withMinimumEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.NoError(t, cfg.Validate())
}

func setupMissingStripeSecret(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_xxx")
	t.Setenv("ALLOWED_ORIGINS", "https://example.com")
	t.Setenv("ENCRYPTION_KEY", validKey32)
	t.Setenv("API_KEY_SERVER_SECRET", validKey32)
}

func setupMissingWebhookSecret(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("STRIPE_SECRET_KEY", "sk_live_xxx")
	t.Setenv("ALLOWED_ORIGINS", "https://example.com")
	t.Setenv("ENCRYPTION_KEY", validKey32)
	t.Setenv("API_KEY_SERVER_SECRET", validKey32)
}

func setupMissingAllowedOrigins(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("STRIPE_SECRET_KEY", "sk_live_xxx")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_xxx")
	t.Setenv("ENCRYPTION_KEY", validKey32)
	t.Setenv("API_KEY_SERVER_SECRET", validKey32)
}

func setupBadEncryptionKey(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("STRIPE_SECRET_KEY", "sk_live_xxx")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_xxx")
	t.Setenv("ALLOWED_ORIGINS", "https://example.com")
	t.Setenv("ENCRYPTION_KEY", "tooshort")
	t.Setenv("API_KEY_SERVER_SECRET", validKey32)
}

func setupShortAPIKeySecret(t *testing.T) {
	t.Helper()
	withMinimumEnv(t)
	t.Setenv("API_KEY_SERVER_SECRET", "tooshort")
}

func TestValidate_ProductionFailFast(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T)
		errContains string
	}{
		{
			name:        "missing STRIPE_SECRET_KEY in production",
			setup:       setupMissingStripeSecret,
			errContains: "STRIPE_SECRET_KEY",
		},
		{
			name:        "missing STRIPE_WEBHOOK_SECRET in production",
			setup:       setupMissingWebhookSecret,
			errContains: "STRIPE_WEBHOOK_SECRET",
		},
		{
			name:        "empty ALLOWED_ORIGINS in production",
			setup:       setupMissingAllowedOrigins,
			errContains: "ALLOWED_ORIGINS",
		},
		{
			name:        "ENCRYPTION_KEY wrong length in production",
			setup:       setupBadEncryptionKey,
			errContains: "ENCRYPTION_KEY",
		},
		{
			name:        "API_KEY_SERVER_SECRET too short (always validated)",
			setup:       setupShortAPIKeySecret,
			errContains: "API_KEY_SERVER_SECRET",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)

			cfg, err := config.Load()
			if err != nil {
				// Some failures surface from env.ParseAsWithOptions itself
				// (e.g. required fields); check the error message regardless.
				assert.Contains(t, err.Error(), tt.errContains)

				return
			}

			err = cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestValidate_ProductionAllValid(t *testing.T) {
	withFullProductionEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.NoError(t, cfg.Validate())
}
