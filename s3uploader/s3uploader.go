package s3uploader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// s3API is the subset of *s3.Client we use for normal upload/download.
type s3API interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadBucket(ctx context.Context, in *s3.HeadBucketInput, opts ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

// presigner is the subset of *s3.PresignClient we use.
type presigner interface {
	PresignGetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// Uploader holds the configured S3 client (and presigner) plus the
// resolved per-Uploader options. Construct via New(opts...).
type Uploader struct {
	client     s3API
	presigner  presigner
	log        *slog.Logger
	sseEnabled bool

	// retryerMode and retryerMaxBackoff are set during construction and
	// exposed via test-only accessors so we can verify retry config without
	// reaching into the SDK's private retryer state.
	retryerMode       string
	retryerMaxBackoff time.Duration
}

// retryerModeForTest exposes the configured retry mode for tests only.
func (u *Uploader) retryerModeForTest() string { return u.retryerMode }

// retryerMaxBackoffForTest exposes the configured retry max-backoff for tests only.
func (u *Uploader) retryerMaxBackoffForTest() time.Duration { return u.retryerMaxBackoff }

// UploadResult contains the response metadata from S3 upload
type UploadResult struct {
	ETag      string  // Entity tag (MD5 hash for non-multipart uploads)
	VersionID *string // S3 version ID if bucket versioning is enabled
}

// New constructs an Uploader from functional options. WithCredentials is
// required; all other options have sensible defaults. WithEndpoint is
// validated (must be https://, no user-info).
func New(opts ...Option) (*Uploader, error) {
	c := defaultConfig()
	for _, opt := range opts {
		opt(c)
	}

	if c.accessKey == "" || c.secretKey == "" {
		return nil, errors.New("s3uploader: access key and secret key are required")
	}
	if err := validateEndpoint(c.endpoint); err != nil {
		return nil, fmt.Errorf("s3uploader: %w", err)
	}

	creds := credentials.NewStaticCredentialsProvider(c.accessKey, c.secretKey, "")

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(creds),
		config.WithRegion(c.region),
		config.WithRequestChecksumCalculation(c.checksumMode),
	)
	if err != nil {
		return nil, fmt.Errorf("s3uploader: loading AWS config: %w", err)
	}

	realClient := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if c.endpoint != "" {
			o.BaseEndpoint = aws.String(c.endpoint)
		}
		o.UsePathStyle = c.forcePathStyle
		o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
			so.MaxAttempts = c.maxAttempts
			so.MaxBackoff = c.maxBackoff
		})
	})

	return &Uploader{
		client:            realClient,
		presigner:         s3.NewPresignClient(realClient),
		log:               c.logger.With(slog.String("component", "s3uploader")),
		sseEnabled:        c.sseEnabled,
		retryerMode:       "standard",
		retryerMaxBackoff: c.maxBackoff,
	}, nil
}

// VerifyBucket runs HeadBucket to confirm credentials and bucket access.
// Returns nil if reachable, a wrapped error otherwise.
func (u *Uploader) VerifyBucket(ctx context.Context, bucket string) error {
	_, err := u.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		return fmt.Errorf("s3 head bucket %s: %w", bucket, err)
	}
	return nil
}

// Upload uploads a file to S3 with proper Content-Type and retry logic.
// Returns UploadResult containing ETag and VersionID from S3 response.
func (u *Uploader) Upload(ctx context.Context, bucketName, key string, body io.Reader, contentType string) (*UploadResult, error) {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType), // Set Content-Type header
	}
	// SSE is opt-in via WithServerSideEncryption(true). Default is off so
	// DigitalOcean Spaces — which doesn't document the SSE header — keeps
	// working out of the box.
	if u.sseEnabled {
		input.ServerSideEncryption = types.ServerSideEncryptionAes256
	}

	u.log.Debug("s3_upload_started", slog.String("bucket", bucketName), slog.String("object_key", key), slog.String("content_type", contentType))
	output, err := u.client.PutObject(ctx, input)
	if err != nil {
		u.log.Error("s3_upload_failed", slog.String("bucket", bucketName), slog.String("object_key", key), slog.Any("error", err))
		return nil, err
	}

	// Extract ETag from response (remove quotes if present)
	etag := ""
	if output.ETag != nil {
		etag = aws.ToString(output.ETag)
	}

	u.log.Info("s3_upload_success", slog.String("bucket", bucketName), slog.String("object_key", key), slog.String("etag", etag))
	return &UploadResult{
		ETag:      etag,
		VersionID: output.VersionId, // May be nil if versioning not enabled
	}, nil
}

// Download retrieves a file from S3 and returns an io.ReadCloser.
// The caller is responsible for closing the returned ReadCloser.
func (u *Uploader) Download(ctx context.Context, bucketName, key string) (io.ReadCloser, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
	}

	u.log.Debug("s3_download_started", slog.String("bucket", bucketName), slog.String("object_key", key))
	output, err := u.client.GetObject(ctx, input)
	if err != nil {
		u.log.Error("s3_download_failed", slog.String("bucket", bucketName), slog.String("object_key", key), slog.Any("error", err))
		return nil, err
	}

	u.log.Debug("s3_download_success", slog.String("bucket", bucketName), slog.String("object_key", key))
	return output.Body, nil
}

// Compile-time check that *Uploader satisfies the runner.S3Uploader contract.
// We can't import runner here (cycle), so we restate the minimal shape locally.
var _ interface {
	Upload(ctx context.Context, bucket, key string, body io.Reader, contentType string) (*UploadResult, error)
} = (*Uploader)(nil)
