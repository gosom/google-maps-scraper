package s3uploader

import (
	"context"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Uploader struct {
	client *s3.Client
}

func New(accessKey, secretKey, region string) *Uploader {
	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(creds),
		config.WithRegion(region),
	)
	if err != nil {
		return nil
	}

	client := s3.NewFromConfig(cfg)

	return &Uploader{
		client: client,
	}
}

func (u *Uploader) Upload(ctx context.Context, bucketName, key string, body io.Reader) error {
	input := &s3.PutObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(key),
		Body:   body,
	}

	_, err := u.client.PutObject(ctx, input)
	if err != nil {
		return err
	}

	return nil
}
