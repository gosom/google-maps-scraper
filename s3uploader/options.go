package s3uploader

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// Option configures the Uploader. Use the With... functions to set values.
type Option func(*uploaderConfig)

type uploaderConfig struct {
	accessKey, secretKey string
	region               string
	endpoint             string // "" => AWS default; set for DO Spaces / MinIO
	forcePathStyle       bool
	sseEnabled           bool
	checksumMode         aws.RequestChecksumCalculation
	logger               *slog.Logger
	maxAttempts          int
	maxBackoff           time.Duration
	metrics              *Metrics
}

func defaultConfig() *uploaderConfig {
	return &uploaderConfig{
		region:       "us-east-1",
		logger:       slog.Default(),
		maxAttempts:  3,
		maxBackoff:   20 * time.Second,
		checksumMode: aws.RequestChecksumCalculationWhenSupported, // SDK default
	}
}

// WithCredentials sets the static AWS access key and secret. Required.
func WithCredentials(accessKey, secretKey string) Option {
	return func(c *uploaderConfig) { c.accessKey, c.secretKey = accessKey, secretKey }
}

// WithRegion sets the AWS region (or DO Spaces region slug). Empty string
// is ignored to preserve the default.
func WithRegion(region string) Option {
	return func(c *uploaderConfig) {
		if region != "" {
			c.region = region
		}
	}
}

// WithEndpoint sets a custom S3-compatible endpoint, e.g.
// "https://nyc3.digitaloceanspaces.com" for DigitalOcean Spaces.
// Empty string keeps the AWS default endpoint.
//
// The endpoint is validated at construction: must be https://, must not
// contain user-info (which would leak credentials into logs).
func WithEndpoint(endpoint string) Option {
	return func(c *uploaderConfig) { c.endpoint = endpoint }
}

// WithForcePathStyle forces path-style addressing
// (https://endpoint/bucket/key) instead of virtual-hosted
// (https://bucket.endpoint/key). Default false; needed only for
// some MinIO setups or buckets with dots in the name (virtual-hosted
// fails TLS hostname validation when the bucket name has dots).
func WithForcePathStyle(force bool) Option {
	return func(c *uploaderConfig) { c.forcePathStyle = force }
}

// WithServerSideEncryption enables AES-256 SSE on PutObject.
// Default off — Spaces does not document this header. Safe to enable
// for AWS S3 if you want SSE-S3 at rest.
func WithServerSideEncryption(enabled bool) Option {
	return func(c *uploaderConfig) { c.sseEnabled = enabled }
}

// WithChecksumMode controls when the SDK computes request checksums
// (CRC32 trailers etc). Default WhenSupported matches SDK behaviour.
// Set WhenRequired to disable trailers — useful if an S3-compatible
// backend rejects them.
func WithChecksumMode(mode aws.RequestChecksumCalculation) Option {
	return func(c *uploaderConfig) { c.checksumMode = mode }
}

// ParseChecksumMode maps the AWS_CHECKSUM_MODE env value to the SDK enum.
// Empty/unknown => WhenSupported (SDK default). "required" => WhenRequired.
func ParseChecksumMode(s string) aws.RequestChecksumCalculation {
	switch s {
	case "required":
		return aws.RequestChecksumCalculationWhenRequired
	default:
		return aws.RequestChecksumCalculationWhenSupported
	}
}

// WithLogger sets the slog logger to use for component-tagged log lines.
// Nil is ignored to preserve the default (slog.Default()).
func WithLogger(logger *slog.Logger) Option {
	return func(c *uploaderConfig) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// WithMetrics installs the provided Prometheus collectors. nil disables
// metric recording (the Uploader checks for nil before every observation).
func WithMetrics(m *Metrics) Option {
	return func(c *uploaderConfig) { c.metrics = m }
}

// WithRetry overrides the default retry attempts and max backoff. Pass
// non-positive values to keep the defaults (3 attempts, 20s backoff).
func WithRetry(maxAttempts int, maxBackoff time.Duration) Option {
	return func(c *uploaderConfig) {
		if maxAttempts > 0 {
			c.maxAttempts = maxAttempts
		}
		if maxBackoff > 0 {
			c.maxBackoff = maxBackoff
		}
	}
}

// validateEndpoint rejects malformed endpoint URLs.
// Empty string is allowed (means "use AWS default").
func validateEndpoint(s string) error {
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}
	if u.Scheme != "https" {
		return errors.New("endpoint must use https:// (got " + u.Scheme + ")")
	}
	if u.User != nil {
		// Reject https://KEY:SECRET@host — credentials must come from env, not the URL.
		return errors.New("endpoint must not contain user-info; pass credentials via AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY")
	}
	if u.Host == "" {
		return errors.New("endpoint must have a host")
	}
	return nil
}
