package s3uploader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePresigner satisfies the package-private presigner interface. The
// optional get hook lets tests inject errors; unset returns a fixed URL so
// the success path can be asserted without SDK ceremony.
type fakePresigner struct {
	get     func(*s3.GetObjectInput) (*v4.PresignedHTTPRequest, error)
	lastIn  *s3.GetObjectInput
	lastOpt int // captured PresignOptions count for assertions
}

func (f *fakePresigner) PresignGetObject(_ context.Context, in *s3.GetObjectInput, opts ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	f.lastIn = in
	f.lastOpt = len(opts)
	if f.get == nil {
		return &v4.PresignedHTTPRequest{URL: "https://example.com/presigned?sig=abc"}, nil
	}
	return f.get(in)
}

// fakeS3 implements the package-private s3API interface with hooks per
// method. Each test sets only the hooks it cares about; unset hooks fall
// back to a benign success response. lastPut captures the last PutObject
// input so tests can assert on the request the production code built.
type fakeS3 struct {
	put     func(*s3.PutObjectInput) (*s3.PutObjectOutput, error)
	get     func(*s3.GetObjectInput) (*s3.GetObjectOutput, error)
	head    func(*s3.HeadBucketInput) (*s3.HeadBucketOutput, error)
	lastPut *s3.PutObjectInput
}

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.lastPut = in
	// Drain the body so countingReader observes the byte count — real S3
	// SDK transports always read the body, and TestUpload_RecordsMetrics
	// relies on this to assert OpBytes equals len(body).
	if in != nil && in.Body != nil {
		_, _ = io.Copy(io.Discard, in.Body)
	}
	if f.put == nil {
		return &s3.PutObjectOutput{ETag: aws.String(`"abc"`)}, nil
	}
	return f.put(in)
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if f.get == nil {
		return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader("hello"))}, nil
	}
	return f.get(in)
}

func (f *fakeS3) HeadBucket(_ context.Context, in *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	if f.head == nil {
		return &s3.HeadBucketOutput{}, nil
	}
	return f.head(in)
}

// newTestUploader constructs an Uploader wired to a stubbed s3API without
// going through New() (which loads AWS config). Options are applied to the
// internal config so behaviour-flag-driven branches (sseEnabled, metrics,
// logger) match production. presigner is left nil — Task 12 covers the
// presign path with its own dedicated fake.
func newTestUploader(stub *fakeS3, opts ...Option) *Uploader {
	c := defaultConfig()
	for _, opt := range opts {
		opt(c)
	}
	return &Uploader{
		client:            stub,
		presigner:         nil,
		log:               c.logger.With(slog.String("component", "s3uploader")),
		sseEnabled:        c.sseEnabled,
		metrics:           c.metrics,
		retryerMode:       "standard",
		retryerMaxBackoff: c.maxBackoff,
	}
}

func TestUpload_SetsContentTypeAndKey(t *testing.T) {
	t.Parallel()

	stub := &fakeS3{}
	u := newTestUploader(stub)
	res, err := u.Upload(context.Background(), "bkt", "k.csv", bytes.NewReader([]byte("a,b\n")), "text/csv; charset=utf-8")
	require.NoError(t, err)
	assert.Equal(t, `"abc"`, res.ETag)
	require.NotNil(t, stub.lastPut)
	assert.Equal(t, "bkt", aws.ToString(stub.lastPut.Bucket))
	assert.Equal(t, "k.csv", aws.ToString(stub.lastPut.Key))
	assert.Equal(t, "text/csv; charset=utf-8", aws.ToString(stub.lastPut.ContentType))
}

func TestUpload_DoesNotSetSSEByDefault_DOSpacesCompat(t *testing.T) {
	t.Parallel()

	stub := &fakeS3{}
	u := newTestUploader(stub)
	_, err := u.Upload(context.Background(), "bkt", "k.csv", bytes.NewReader(nil), "text/csv")
	require.NoError(t, err)
	assert.Equal(t, types.ServerSideEncryption(""), stub.lastPut.ServerSideEncryption,
		"SSE must NOT be set by default — keeps DO Spaces compatibility")
}

func TestUpload_SetsSSEWhenEnabled(t *testing.T) {
	t.Parallel()

	stub := &fakeS3{}
	u := newTestUploader(stub, WithServerSideEncryption(true))
	_, err := u.Upload(context.Background(), "bkt", "k.csv", bytes.NewReader(nil), "text/csv")
	require.NoError(t, err)
	assert.Equal(t, types.ServerSideEncryptionAes256, stub.lastPut.ServerSideEncryption)
}

func TestUpload_WrapsErrorWithBucketAndKey(t *testing.T) {
	t.Parallel()

	stub := &fakeS3{put: func(*s3.PutObjectInput) (*s3.PutObjectOutput, error) {
		return nil, errors.New("boom")
	}}
	u := newTestUploader(stub)
	_, err := u.Upload(context.Background(), "bkt", "k.csv", bytes.NewReader(nil), "text/csv")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bkt/k.csv")
	assert.Contains(t, err.Error(), "boom")
}

func TestDownload_StreamsBodyAndWrapsError(t *testing.T) {
	t.Parallel()

	stub := &fakeS3{}
	u := newTestUploader(stub)
	rc, err := u.Download(context.Background(), "bkt", "k.csv")
	require.NoError(t, err)
	body, readErr := io.ReadAll(rc)
	require.NoError(t, readErr)
	require.NoError(t, rc.Close())
	assert.Equal(t, "hello", string(body))

	stub.get = func(*s3.GetObjectInput) (*s3.GetObjectOutput, error) {
		return nil, errors.New("nope")
	}
	_, err = u.Download(context.Background(), "bkt", "k.csv")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bkt/k.csv")
	assert.Contains(t, err.Error(), "nope")
}

func TestVerifyBucket_OK(t *testing.T) {
	t.Parallel()

	stub := &fakeS3{}
	u := newTestUploader(stub)
	require.NoError(t, u.VerifyBucket(context.Background(), "bkt"))
}

func TestVerifyBucket_WrapsError(t *testing.T) {
	t.Parallel()

	stub := &fakeS3{head: func(*s3.HeadBucketInput) (*s3.HeadBucketOutput, error) {
		return nil, errors.New("403 Forbidden")
	}}
	u := newTestUploader(stub)
	err := u.VerifyBucket(context.Background(), "bkt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bkt")
	assert.Contains(t, err.Error(), "403")
}

func TestPresignGet_ReturnsURL(t *testing.T) {
	t.Parallel()

	pres := &fakePresigner{}
	u := &Uploader{
		client:    &fakeS3{},
		presigner: pres,
		log:       slog.Default().With(slog.String("component", "s3uploader")),
	}
	url, err := u.PresignGet(context.Background(), "bkt", "k.csv", 5*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/presigned?sig=abc", url)
	require.NotNil(t, pres.lastIn)
	assert.Equal(t, "bkt", aws.ToString(pres.lastIn.Bucket))
	assert.Equal(t, "k.csv", aws.ToString(pres.lastIn.Key))
	assert.Equal(t, 1, pres.lastOpt, "WithPresignExpires should be passed exactly once")
}

func TestPresignGet_WrapsError(t *testing.T) {
	t.Parallel()

	pres := &fakePresigner{get: func(*s3.GetObjectInput) (*v4.PresignedHTTPRequest, error) {
		return nil, errors.New("boom")
	}}
	u := &Uploader{
		client:    &fakeS3{},
		presigner: pres,
		log:       slog.Default().With(slog.String("component", "s3uploader")),
	}
	_, err := u.PresignGet(context.Background(), "bkt", "k.csv", time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bkt/k.csv")
	assert.Contains(t, err.Error(), "boom")
}

func TestPresignGet_NilPresignerReturnsError(t *testing.T) {
	t.Parallel()

	u := &Uploader{
		client:    &fakeS3{},
		presigner: nil,
		log:       slog.Default().With(slog.String("component", "s3uploader")),
	}
	_, err := u.PresignGet(context.Background(), "bkt", "k.csv", time.Minute)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "presigner not configured")
}

func TestValidateEndpoint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		wantErr bool
	}{
		{"", false},
		{"https://nyc3.digitaloceanspaces.com", false},
		{"http://nyc3.digitaloceanspaces.com", true},             // not https
		{"https://KEY:SECRET@nyc3.digitaloceanspaces.com", true}, // userinfo
		{"https://", true},      // no host
		{"::not a url::", true}, // url.Parse rejects (missing protocol scheme)
	}
	for _, c := range cases {
		err := validateEndpoint(c.in)
		if c.wantErr {
			assert.Error(t, err, "expected error for %q", c.in)
		} else {
			assert.NoError(t, err, "expected no error for %q", c.in)
		}
	}
}
