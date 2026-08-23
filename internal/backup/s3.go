package backup

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"pgmanager/internal/config"
)

// s3Store is the ObjectStore backed by an S3-compatible bucket (AWS S3, or
// any endpoint that speaks the S3 API — R2, B2, MinIO, ...).
type s3Store struct {
	client   *s3.Client
	uploader *manager.Uploader
	bucket   string
}

// NewS3Store builds an ObjectStore from cfg. It never returns cfg's secret
// (or any credential) in an error: acceptance criterion 7 requires backup
// credentials never appear in an API response, log line, or audit entry,
// and an error returned here can end up in all three.
func NewS3Store(cfg config.BackupConfig) (ObjectStore, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("backup: bucket is required")
	}
	if cfg.AccessKeyID == "" {
		return nil, fmt.Errorf("backup: access_key_id is required")
	}

	secret, err := cfg.Secret()
	if err != nil {
		// cfg.Secret's own error only ever names a file path on read
		// failure, never the secret's value — safe to wrap as-is.
		return nil, fmt.Errorf("backup: resolve secret: %w", err)
	}
	if secret == "" {
		return nil, fmt.Errorf("backup: secret_access_key or secret_access_key_file is required")
	}

	var endpointOrNil *string
	if cfg.Endpoint != "" {
		endpointOrNil = aws.String(cfg.Endpoint)
	}

	c := s3.New(s3.Options{
		Region:       cfg.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, secret, ""),
		BaseEndpoint: endpointOrNil,      // set for R2/B2/MinIO; nil for AWS
		UsePathStyle: cfg.Endpoint != "", // required by MinIO and most non-AWS endpoints
	})

	return &s3Store{
		client:   c,
		uploader: manager.NewUploader(c), // streams multipart from an io.Reader
		bucket:   cfg.Bucket,
	}, nil
}

// Put implements ObjectStore.
func (s *s3Store) Put(ctx context.Context, key string, body io.Reader) (int64, error) {
	// manager.Uploader.Upload does not report how many bytes it sent, so
	// count them ourselves as they're read off body.
	counting := &countingReader{r: body}
	_, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   counting,
	})
	if err != nil {
		return counting.n, fmt.Errorf("backup: upload %s: %w", key, err)
	}
	return counting.n, nil
}

// Get implements ObjectStore.
func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("backup: get %s: %w", key, err)
	}
	return out.Body, nil
}

// Delete implements ObjectStore.
func (s *s3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("backup: delete %s: %w", key, err)
	}
	return nil
}

// countingReader wraps an io.Reader and tracks how many bytes have been read
// through it, so Put can report a size even though manager.Uploader doesn't.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
