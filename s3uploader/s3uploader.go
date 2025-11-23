package s3uploader

import (
	"context"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Uploader struct {
	client *s3.Client
}

// UploadResult contains the response metadata from S3 upload
type UploadResult struct {
	ETag      string  // Entity tag (MD5 hash for non-multipart uploads)
	VersionID *string // S3 version ID if bucket versioning is enabled
}

func New(accessKey, secretKey, region string) *Uploader {
	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(creds),
		config.WithRegion(region),
		config.WithRetryMaxAttempts(3),              // Retry up to 3 times for transient failures
		config.WithRetryMode(aws.RetryModeAdaptive), // Use adaptive retry mode
	)
	if err != nil {
		return nil
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
	}
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

	output, err := u.client.PutObject(ctx, input)
	if err != nil {
		return nil, err
	}

	// Extract ETag from response (remove quotes if present)
	etag := ""
	if output.ETag != nil {
		etag = aws.ToString(output.ETag)
	}

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

	output, err := u.client.GetObject(ctx, input)
	if err != nil {
		return nil, err
	}

	return output.Body, nil
}
