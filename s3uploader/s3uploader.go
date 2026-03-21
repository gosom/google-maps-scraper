package s3uploader

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	pkglogger "github.com/gosom/google-maps-scraper/pkg/logger"
)

type Uploader struct {
	client *s3.Client
	log    *slog.Logger
}

// UploadResult contains the response metadata from S3 upload
type UploadResult struct {
	ETag      string  // Entity tag (MD5 hash for non-multipart uploads)
	VersionID *string // S3 version ID if bucket versioning is enabled
}

func New(accessKey, secretKey, region string) (*Uploader, error) {
	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(creds),
		config.WithRegion(region),
		config.WithRetryMaxAttempts(3),              // Retry up to 3 times for transient failures
		config.WithRetryMode(aws.RetryModeAdaptive), // Use adaptive retry mode
	)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		// Configure additional retry behavior
		o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
			so.MaxAttempts = 3
			so.MaxBackoff = 20 // Maximum backoff time in seconds
		})
	})

	return &Uploader{
		client: client,
		log:    pkglogger.NewWithComponent(os.Getenv("LOG_LEVEL"), "s3uploader"),
	}, nil
}

// Upload uploads a file to S3 with proper Content-Type and retry logic
// Returns UploadResult containing ETag and VersionID from S3 response
func (u *Uploader) Upload(ctx context.Context, bucketName, key string, body io.Reader, contentType string) (*UploadResult, error) {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType), // Set Content-Type header
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

// Download retrieves a file from S3 and returns an io.ReadCloser
// The caller is responsible for closing the returned ReadCloser
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
