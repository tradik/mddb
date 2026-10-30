package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

// syncS3Timeout bounds one S3 call of a sync: the listing, or one object.
var syncS3Timeout = 5 * time.Minute

// s3SyncSource reads the objects under a prefix of an S3-compatible bucket,
// through the client S3-backed collections and S3 backups use (newS3Client),
// without creating the bucket: a source is only read.
// The listing carries each object's ETag, so an unchanged object is not
// downloaded at all.
type s3SyncSource struct {
	action *SyncAction
	client *minio.Client
	bucket string
}

// newS3SyncSource connects to the action's bucket, which must exist.
func newS3SyncSource(a *SyncAction) (*s3SyncSource, error) {
	cfg := *a.StorageConfig
	cfg.Bucket = a.Bucket
	client, exists, err := newS3Client(&cfg)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("sync source: S3 bucket %q does not exist", a.Bucket)
	}
	return &s3SyncSource{action: a, client: client, bucket: a.Bucket}, nil
}

// list returns the objects under the prefix, "directory" markers left out.
// An object's key below the prefix derives its document key.
func (t *s3SyncSource) list(ctx context.Context) ([]syncItem, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, syncS3Timeout)
	defer cancel() // also ends the listing when it is cut short
	a := t.action
	var items []syncItem
	for obj := range t.client.ListObjects(ctx, t.bucket, minio.ListObjectsOptions{Prefix: a.Prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, false, obj.Err
		}
		if strings.HasSuffix(obj.Key, "/") {
			continue
		}
		if len(items) == a.MaxDocuments {
			return items, true, nil
		}
		items = append(items, syncItem{ref: obj.Key, key: syncKey(strings.TrimPrefix(obj.Key, a.Prefix)),
			etag: obj.ETag, size: obj.Size})
	}
	return items, false, nil
}

// fetch downloads an object whose ETag differs from the last run's.
func (t *s3SyncSource) fetch(ctx context.Context, it syncItem, prev syncState) (syncFetched, error) {
	limit := t.action.MaxBytes
	if it.etag != "" && it.etag == prev.etag {
		return syncFetched{notModified: true}, nil
	}
	if it.size > limit {
		return syncFetched{}, fmt.Errorf("larger than maxBytes (%d)", limit)
	}
	ctx, cancel := context.WithTimeout(ctx, syncS3Timeout)
	defer cancel()
	// minio-go's GetObject only checks its arguments; the request, and any
	// failure of it, comes with the first read.
	obj, err := t.client.GetObject(ctx, t.bucket, it.ref, minio.GetObjectOptions{})
	if err != nil {
		return syncFetched{}, err
	}
	defer func() { _ = obj.Close() }()
	body, err := io.ReadAll(io.LimitReader(obj, limit+1))
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return syncFetched{}, errSourceGone
		}
		return syncFetched{}, err
	}
	if int64(len(body)) > limit {
		return syncFetched{}, fmt.Errorf("larger than maxBytes (%d)", limit)
	}
	return syncFetched{body: body, etag: it.etag}, nil
}
