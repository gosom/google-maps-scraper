package config_test

import (
	"os"
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
	assert.Equal(t, "", cfg.AWS.Endpoint)
	assert.False(t, cfg.AWS.ForcePathStyle)
	assert.False(t, cfg.AWS.SSEEnabled)
	assert.Equal(t, "", cfg.AWS.ChecksumMode)
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

// TestLoad_TrimsCSVWhitespace verifies that values written with spaces around
// the comma separator (a common operator-formatting style) are normalized.
// caarlos0/env's envSeparator does not trim per-element whitespace; without
// the explicit pass in Load(), the second origin in "https://a, https://b"
// would carry a leading space and silently fail exact-match CORS lookups.
func TestLoad_TrimsCSVWhitespace(t *testing.T) {
	t.Setenv("DSN", "postgres://localhost/test")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_clerk")
	t.Setenv("ALLOWED_ORIGINS", "https://a.com, https://b.com ,  https://c.com")
	t.Setenv("STRIPE_WEBHOOK_ALLOWED_CIDRS", "10.0.0.0/8 , 192.168.0.0/16")
	t.Setenv("PROXIES", "http://p1, http://p2 ")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, []string{"https://a.com", "https://b.com", "https://c.com"}, cfg.AllowedOrigins,
		"AllowedOrigins must be trimmed per element")
	assert.Equal(t, []string{"10.0.0.0/8", "192.168.0.0/16"}, cfg.Stripe.WebhookAllowedCIDRs,
		"WebhookAllowedCIDRs must be trimmed per element")
	assert.Equal(t, []string{"http://p1", "http://p2"}, cfg.Proxies,
		"Proxies must be trimmed per element")
}

// TestAWSConfig_DOSpaces verifies that DO Spaces env vars (AWS_ENDPOINT, etc.)
// are parsed correctly and round-trip through Load.
func TestAWSConfig_DOSpaces(t *testing.T) {
	withMinimumEnv(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "DO_KEY")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "DO_SECRET")
	t.Setenv("AWS_REGION", "nyc3")
	t.Setenv("AWS_ENDPOINT", "https://nyc3.digitaloceanspaces.com")
	t.Setenv("S3_BUCKET_NAME", "brezel-csv")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "DO_KEY", cfg.AWS.AccessKeyID)
	assert.Equal(t, "nyc3", cfg.AWS.Region)
	assert.Equal(t, "https://nyc3.digitaloceanspaces.com", cfg.AWS.Endpoint)
	assert.False(t, cfg.AWS.ForcePathStyle)
	assert.False(t, cfg.AWS.SSEEnabled)
	assert.Equal(t, "brezel-csv", cfg.S3BucketName)
}

// TestAWSConfig_AWSDefaults verifies the no-endpoint AWS-default path is unchanged.
func TestAWSConfig_AWSDefaults(t *testing.T) {
	withMinimumEnv(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA...")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("S3_BUCKET_NAME", "brezel-csv")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "us-east-1", cfg.AWS.Region) // default
	assert.Empty(t, cfg.AWS.Endpoint)            // empty => AWS
	assert.False(t, cfg.AWS.ForcePathStyle)
	assert.False(t, cfg.AWS.SSEEnabled)
}

// Note: tests serialize on env var; do not add t.Parallel().
func TestLoad_WithDataFolderOverride(t *testing.T) {
	tests := []struct {
		name     string
		setupEnv func(t *testing.T)
		opts     []config.LoadOption
		expected string
	}{
		{
			name: "flag set, env unset",
			setupEnv: func(t *testing.T) {
				t.Helper()
				require.NoError(t, os.Unsetenv("DATA_FOLDER"))
			},
			opts:     []config.LoadOption{config.WithDataFolderOverride("/custom")},
			expected: "/custom",
		},
		{
			name: "flag unset, env set",
			setupEnv: func(t *testing.T) {
				t.Helper()
				t.Setenv("DATA_FOLDER", "/from-env")
			},
			opts:     nil,
			expected: "/from-env",
		},
		{
			name: "both unset",
			setupEnv: func(t *testing.T) {
				t.Helper()
				require.NoError(t, os.Unsetenv("DATA_FOLDER"))
			},
			opts:     nil,
			expected: "./webdata",
		},
		{
			name: "both set",
			setupEnv: func(t *testing.T) {
				t.Helper()
				t.Setenv("DATA_FOLDER", "/from-env")
			},
			opts:     []config.LoadOption{config.WithDataFolderOverride("/custom")},
			expected: "/custom",
		},
		{
			name: "flag empty, env set to default value",
			setupEnv: func(t *testing.T) {
				t.Helper()
				t.Setenv("DATA_FOLDER", "./webdata")
			},
			opts:     []config.LoadOption{config.WithDataFolderOverride("")},
			expected: "./webdata",
		},
		{
			// caarlos0/env v11: when an env var is set to an empty string,
			// envDefault DOES fire and the default value wins. This locks the
			// observed library behavior so a future env-lib upgrade that flips
			// the semantics (set-but-empty winning over envDefault) breaks
			// this test loudly rather than silently.
			name: "flag empty, env set to empty string",
			setupEnv: func(t *testing.T) {
				t.Helper()
				t.Setenv("DATA_FOLDER", "")
			},
			opts:     []config.LoadOption{config.WithDataFolderOverride("")},
			expected: "./webdata",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMinimumEnv(t)
			tt.setupEnv(t)

			cfg, err := config.Load(tt.opts...)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, cfg.DataFolder)
		})
	}
}

// TestLoad_DropsEmptyCSVElements covers a related edge case where consecutive
// commas or trailing commas (e.g. "a,,b" or "a,b,") produce empty elements.
// The trim pass also drops them so downstream consumers don't see empty strings.
func TestLoad_DropsEmptyCSVElements(t *testing.T) {
	t.Setenv("DSN", "postgres://localhost/test")
	t.Setenv("CLERK_SECRET_KEY", "sk_test_clerk")
	t.Setenv("ALLOWED_ORIGINS", "https://a.com,, https://b.com,")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, []string{"https://a.com", "https://b.com"}, cfg.AllowedOrigins)
}
