package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/gosom/google-maps-scraper/pkg/appenv"
)

// Config holds the complete typed configuration for the binary, loaded
// from environment variables exactly once at startup via Load().
//
// Nested structs use envPrefix to scope their variables. DSN and a few
// other vars that are widely referenced (or have no natural prefix) stay
// at the top level.
type Config struct {
	// ── Core / runtime ──────────────────────────────────────────────
	AppEnv           appenv.Environment `env:"APP_ENV"`
	LogLevel         string             `env:"LOG_LEVEL" envDefault:"info"`
	InternalAddr     string             `env:"INTERNAL_ADDR" envDefault:":9090"`
	DataFolder       string             `env:"DATA_FOLDER" envDefault:"./webdata"`
	Concurrency      int                `env:"CONCURRENCY"`
	DisableTelemetry bool               `env:"DISABLE_TELEMETRY"`

	// ── Logging ─────────────────────────────────────────────────────
	Log LogConfig `envPrefix:"LOG_"`

	// ── Database DSN (top-level — widely referenced) ─────────────────
	DSN          string `env:"DSN,required"`
	MigrationDSN string `env:"MIGRATION_DSN"`

	// ── Database connection pool ─────────────────────────────────────
	DB DBConfig `envPrefix:"DB_"`

	// ── Auth & crypto ────────────────────────────────────────────────
	ClerkSecretKey     string `env:"CLERK_SECRET_KEY,required"`
	APIKeyServerSecret []byte `env:"API_KEY_SERVER_SECRET"`
	EncryptionKey      string `env:"ENCRYPTION_KEY"`

	// ── Stripe ──────────────────────────────────────────────────────
	Stripe StripeConfig `envPrefix:"STRIPE_"`

	// ── CORS ────────────────────────────────────────────────────────
	AllowedOrigins []string `env:"ALLOWED_ORIGINS" envSeparator:","`

	// ── Stuck-job detection ──────────────────────────────────────────
	StuckJobCheckIntervalMinutes int `env:"STUCK_JOB_CHECK_INTERVAL_MINUTES" envDefault:"10"`
	StuckJobTimeoutHours         int `env:"STUCK_JOB_TIMEOUT_HOURS" envDefault:"4"`

	// ── Webhook event cleanup ────────────────────────────────────────
	WebhookEventRetentionDays int `env:"WEBHOOK_EVENT_RETENTION_DAYS" envDefault:"90"`

	// ── AWS / S3 ────────────────────────────────────────────────────
	AWS          AWSConfig `envPrefix:"AWS_"`
	S3BucketName string    `env:"S3_BUCKET_NAME"`

	// ── Google ──────────────────────────────────────────────────────
	Google GoogleConfig `envPrefix:"GOOGLE_"`

	// ── External services ───────────────────────────────────────────
	WebshareAPIKey string   `env:"WEBSHARE_API_KEY"`
	ResendAPIKey   string   `env:"RESEND_API_KEY"`
	Proxies        []string `env:"PROXIES" envSeparator:","`

	// ── Build metadata ───────────────────────────────────────────────
	Build BuildConfig
}

// LogConfig holds log-sink configuration. These vars all share the LOG_
// prefix (resolved via envPrefix on the parent).
//
// LogLevel intentionally stays on the parent Config: it is referenced
// in many places and has no LOG_ prefix in the existing environment.
type LogConfig struct {
	Output        string `env:"OUTPUT" envDefault:"both"`
	FilePath      string `env:"FILE_PATH"`
	Dir           string `env:"DIR" envDefault:"logs"`
	FileName      string `env:"FILE_NAME" envDefault:"brezel-api.log"`
	MaxSizeMB     int    `env:"MAX_SIZE_MB" envDefault:"100"`
	RetentionDays int    `env:"RETENTION_DAYS" envDefault:"7"`
}

// DBConfig holds connection-pool tunables. Variables share the DB_ prefix.
type DBConfig struct {
	MaxOpenConns    int           `env:"MAX_OPEN_CONNS" envDefault:"25"`
	MaxIdleConns    int           `env:"MAX_IDLE_CONNS" envDefault:"10"`
	ConnMaxLifetime time.Duration `env:"CONN_MAX_LIFETIME" envDefault:"5m"`
	ConnMaxIdleTime time.Duration `env:"CONN_MAX_IDLE_TIME" envDefault:"2m"`
}

// StripeConfig holds Stripe API credentials and webhook configuration.
// Variables share the STRIPE_ prefix.
type StripeConfig struct {
	SecretKey             string   `env:"SECRET_KEY"`
	WebhookSecret         string   `env:"WEBHOOK_SECRET"`
	WebhookSecretPrevious string   `env:"WEBHOOK_SECRET_PREVIOUS"`
	WebhookAllowedCIDRs   []string `env:"WEBHOOK_ALLOWED_CIDRS" envSeparator:","`
}

// WebhookSecrets returns the non-empty webhook signing secrets in order
// [current, previous]. This replaces the stripeWebhookSecretsFromEnv
// helper that Chunk 2 will delete from the webrunner.
func (s StripeConfig) WebhookSecrets() []string {
	out := make([]string, 0, 2)

	if s.WebhookSecret != "" {
		out = append(out, s.WebhookSecret)
	}

	if s.WebhookSecretPrevious != "" {
		out = append(out, s.WebhookSecretPrevious)
	}

	return out
}

// AWSConfig holds AWS/S3 credentials. Variables share the AWS_ prefix.
// S3_BUCKET_NAME stays at the top level (no AWS_ prefix on the bucket var).
type AWSConfig struct {
	AccessKeyID     string `env:"ACCESS_KEY_ID"`
	SecretAccessKey string `env:"SECRET_ACCESS_KEY"`
	Region          string `env:"REGION" envDefault:"us-east-1"`
}

// GoogleConfig holds Google OAuth credentials. Variables share the GOOGLE_ prefix.
type GoogleConfig struct {
	ClientID     string `env:"CLIENT_ID"`
	ClientSecret string `env:"CLIENT_SECRET"`
	RedirectURL  string `env:"REDIRECT_URL"`
	CookiesFile  string `env:"COOKIES_FILE"`
}

// BuildConfig holds metadata injected at build time via -ldflags. These
// vars have no common prefix, so envPrefix is not used here.
type BuildConfig struct {
	GitCommit   string `env:"GIT_COMMIT"`
	BuildDate   string `env:"BUILD_DATE"`
	Version     string `env:"VERSION"`
	Environment string `env:"ENVIRONMENT" envDefault:"development"`
}

// appEnvType is the reflect.Type for appenv.Environment, used to register
// a custom parser in the caarlos0/env FuncMap.
var appEnvType = reflect.TypeOf(appenv.Environment(0))

// byteSliceType is the reflect.Type for []byte. The default caarlos0/env
// behavior for []byte is to parse comma-separated uint8 integers, which
// is wrong for secret material like API keys. We override it to treat the
// raw env-var string as bytes directly.
var byteSliceType = reflect.TypeOf([]byte{})

// Load reads all environment variables once and returns a fully populated
// *Config. Required fields (DSN, CLERK_SECRET_KEY) cause an immediate
// error if absent. After parsing, Validate() is called; production
// deployments get additional secret-presence checks.
//
// Custom FuncMap entries:
//   - appenv.Environment: delegates to appenv.Parse for whitelist validation.
//   - []byte: treats the raw string value as bytes (not comma-separated ints).
func Load() (*Config, error) {
	opts := env.Options{
		FuncMap: map[reflect.Type]env.ParserFunc{
			appEnvType: func(v string) (interface{}, error) {
				return appenv.Parse(v)
			},
			byteSliceType: func(v string) (interface{}, error) {
				return []byte(v), nil
			},
		},
	}

	cfg, err := env.ParseAsWithOptions[Config](opts)
	if err != nil {
		return nil, fmt.Errorf("config: parse env: %w", err)
	}

	// caarlos0/env's envSeparator splits on the literal separator only; it
	// does not trim per-element whitespace. Operators commonly write
	// "https://a.com, https://b.com" with a space after the comma — without
	// this pass, the second origin would be " https://b.com" (leading space)
	// and silently fail exact-match lookups (CORS map, CIDR parser, proxy URL
	// parser). Trim every []string field that uses envSeparator.
	cfg.AllowedOrigins = trimAndDropEmpty(cfg.AllowedOrigins)
	cfg.Proxies = trimAndDropEmpty(cfg.Proxies)
	cfg.Stripe.WebhookAllowedCIDRs = trimAndDropEmpty(cfg.Stripe.WebhookAllowedCIDRs)

	if validateErr := cfg.Validate(); validateErr != nil {
		return nil, validateErr
	}

	return &cfg, nil
}

// trimAndDropEmpty trims whitespace from each element and drops empties.
// Pure helper; tested via TestLoad_TrimsCSVWhitespace.
func trimAndDropEmpty(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:0]
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Validate enforces runtime invariants. In production, required secrets
// must be present and well-formed. API_KEY_SERVER_SECRET length is
// enforced regardless of environment (when the field is non-empty).
func (c *Config) Validate() error {
	var errs []error

	// API_KEY_SERVER_SECRET: always validated when non-empty.
	if len(c.APIKeyServerSecret) > 0 && len(c.APIKeyServerSecret) < 32 {
		errs = append(errs, fmt.Errorf("API_KEY_SERVER_SECRET must be at least 32 bytes (got %d)", len(c.APIKeyServerSecret)))
	}

	if !c.AppEnv.IsProduction() {
		return errors.Join(errs...)
	}

	// Production-only checks.
	if c.Stripe.SecretKey == "" {
		errs = append(errs, errors.New("STRIPE_SECRET_KEY is required in production"))
	}

	if c.Stripe.WebhookSecret == "" {
		errs = append(errs, errors.New("STRIPE_WEBHOOK_SECRET is required in production"))
	}

	if len(c.AllowedOrigins) == 0 {
		errs = append(errs, errors.New("ALLOWED_ORIGINS must not be empty in production"))
	}

	if c.EncryptionKey == "" {
		errs = append(errs, errors.New("ENCRYPTION_KEY is required in production"))
	} else if len(c.EncryptionKey) != 32 {
		errs = append(errs, fmt.Errorf("ENCRYPTION_KEY must be exactly 32 bytes in production (got %d)", len(c.EncryptionKey)))
	}

	return errors.Join(errs...)
}
