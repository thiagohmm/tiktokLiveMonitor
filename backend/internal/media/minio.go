// Package media stores Fila PIX receipts (jpg/pdf) in a MinIO/S3-compatible
// bucket. The backend is the only component with credentials: the frontend
// always reads receipts through the authenticated API.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrObjectNotFound is returned when the object is not in the bucket.
var ErrObjectNotFound = errors.New("media object not found")

// Store is the storage contract used by the Fila PIX service.
type Store interface {
	EnsureBucket(ctx context.Context) error
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// Config holds the MinIO settings (env-driven).
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

// LoadConfigFromEnv builds the MinIO config.
func LoadConfigFromEnv() Config {
	return Config{
		Endpoint:  strings.TrimSpace(os.Getenv("MINIO_ENDPOINT")),
		AccessKey: strings.TrimSpace(os.Getenv("MINIO_ACCESS_KEY")),
		SecretKey: strings.TrimSpace(os.Getenv("MINIO_SECRET_KEY")),
		Bucket:    defaultBucket(strings.TrimSpace(os.Getenv("MINIO_BUCKET"))),
		UseSSL:    envBool("MINIO_USE_SSL", false),
	}
}

func defaultBucket(name string) string {
	if name == "" {
		return "tlm-pix-media"
	}
	return name
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// Configured reports whether the minimum storage settings are present.
func (c Config) Configured() bool {
	return c.Endpoint != "" && c.AccessKey != "" && c.SecretKey != "" && c.Bucket != ""
}

// MinIOStorage implements Store over the MinIO Go SDK.
type MinIOStorage struct {
	client *minio.Client
	bucket string
}

// NewMinIOStorage creates the client. The bucket is ensured separately so a
// temporarily unavailable MinIO does not take the whole backend down.
func NewMinIOStorage(cfg Config) (*MinIOStorage, error) {
	if !cfg.Configured() {
		return nil, fmt.Errorf("minio not configured")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	return &MinIOStorage{client: client, bucket: cfg.Bucket}, nil
}

// EnsureBucket creates the bucket when missing.
func (s *MinIOStorage) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("minio bucket exists: %w", err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		// A concurrent creator may win the race; re-check before failing.
		if ok, checkErr := s.client.BucketExists(ctx, s.bucket); checkErr == nil && ok {
			return nil
		}
		return fmt.Errorf("minio make bucket: %w", err)
	}
	return nil
}

// Put uploads an object with an explicit content type.
func (s *MinIOStorage) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("minio put %s: %w", key, err)
	}
	return nil
}

// Get downloads an object.
func (s *MinIOStorage) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("minio get %s: %w", key, err)
	}
	defer func() { _ = obj.Close() }()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(obj); err != nil {
		if isNotFound(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("minio read %s: %w", key, err)
	}
	return buf.Bytes(), nil
}

// Delete removes an object; missing objects are treated as success.
func (s *MinIOStorage) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("minio delete %s: %w", key, err)
	}
	return nil
}

func isNotFound(err error) bool {
	if errors.Is(err, ErrObjectNotFound) {
		return true
	}
	resp := minio.ToErrorResponse(err)
	return resp.StatusCode == 404 || resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket"
}
