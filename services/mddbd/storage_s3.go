package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"mddb/internal/storage"
)

// S3Backend stores documents in an S3-compatible object store (AWS S3, MinIO, Cloudflare R2, etc.).
type S3Backend struct {
	client *minio.Client
	bucket string
	prefix string // optional path prefix within bucket, e.g. "mddb/"
}

// newS3Client connects to cfg's endpoint and checks cfg's bucket, which it
// reports as missing without creating it. A sync source (INT-022) only reads,
// so it stops here; NewS3Backend goes on to create the bucket it writes to.
func newS3Client(cfg *StorageConfigDef) (client *minio.Client, exists bool, err error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, false, fmt.Errorf("s3 backend requires endpoint and bucket")
	}

	client, err = minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseTLS,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, false, fmt.Errorf("failed to create S3 client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exists, err = client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check S3 bucket: %w", err)
	}
	return client, exists, nil
}

// NewS3Backend creates a new S3 storage backend from the given config,
// creating its bucket if it is missing.
func NewS3Backend(cfg *StorageConfigDef) (*S3Backend, error) {
	client, exists, err := newS3Client(cfg)
	if err != nil {
		return nil, err
	}
	if !exists {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("failed to create S3 bucket %q: %w", cfg.Bucket, err)
		}
	}

	prefix := cfg.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	return &S3Backend{
		client: client,
		bucket: cfg.Bucket,
		prefix: prefix,
	}, nil
}

// Name implements the StorageBackend interface.
func (s *S3Backend) Name() string { return "s3" }

func (s *S3Backend) docKey(collection, docID string) string {
	return s.prefix + "docs/" + collection + "/" + docID
}

// byKeyKey is the object path of a key-index entry. The key and language are
// normalised as in bbolt (storage.NormalizeKey), so "README.md" and
// "readme.md" name one object; the collection keeps its case (#360).
//
// No release has written these objects: the server keeps the key index of
// every collection in bbolt and moves only document bodies to a backend
// (GO-021), so there is nothing in a bucket to migrate.
func (s *S3Backend) byKeyKey(collection, key, lang string) string {
	return s.prefix + "bykey/" + collection + "/" + storage.NormalizeKey(key) + "/" + storage.NormalizeKey(lang)
}

// PutDoc implements the StorageBackend interface.
func (s *S3Backend) PutDoc(collection, docID string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := s.client.PutObject(ctx, s.bucket, s.docKey(collection, docID),
		bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

// GetDoc implements the StorageBackend interface.
func (s *S3Backend) GetDoc(collection, docID string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	obj, err := s.client.GetObject(ctx, s.bucket, s.docKey(collection, docID), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(obj)
	if err != nil {
		// Check if the error is a "not found" response
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

// DeleteDoc implements the StorageBackend interface.
func (s *S3Backend) DeleteDoc(collection, docID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.client.RemoveObject(ctx, s.bucket, s.docKey(collection, docID), minio.RemoveObjectOptions{})
}

// ListDocs implements the StorageBackend interface.
func (s *S3Backend) ListDocs(collection string, fn func(docID string, data []byte) error) error {
	ctx := context.Background()
	prefix := s.prefix + "docs/" + collection + "/"

	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return obj.Err
		}
		docID := strings.TrimPrefix(obj.Key, prefix)
		if docID == "" {
			continue
		}
		data, err := s.GetDoc(collection, docID)
		if err != nil {
			return err
		}
		if data == nil {
			continue
		}
		if err := fn(docID, data); err != nil {
			return err
		}
	}
	return nil
}

// PutByKey implements the StorageBackend interface.
func (s *S3Backend) PutByKey(collection, key, lang, docID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s.client.PutObject(ctx, s.bucket, s.byKeyKey(collection, key, lang),
		bytes.NewReader([]byte(docID)), int64(len(docID)),
		minio.PutObjectOptions{ContentType: "text/plain"})
	return err
}

// GetByKey implements the StorageBackend interface.
func (s *S3Backend) GetByKey(collection, key, lang string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	obj, err := s.client.GetObject(ctx, s.bucket, s.byKeyKey(collection, key, lang), minio.GetObjectOptions{})
	if err != nil {
		return "", err
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(obj)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// DeleteByKey implements the StorageBackend interface.
func (s *S3Backend) DeleteByKey(collection, key, lang string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.client.RemoveObject(ctx, s.bucket, s.byKeyKey(collection, key, lang), minio.RemoveObjectOptions{})
}

// Close implements the StorageBackend interface.
func (s *S3Backend) Close() error {
	// minio-go client has no Close method; nothing to release.
	return nil
}

// Ensure S3Backend implements StorageBackend at compile time.
var _ StorageBackend = (*S3Backend)(nil)
