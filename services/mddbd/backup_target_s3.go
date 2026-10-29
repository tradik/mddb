package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/minio/minio-go/v7"
)

// backupStagingDir is where an S3 backup is written and hashed before the
// upload, inside MDDB_BACKUP_DIR; it is emptied after every run.
const backupStagingDir = ".scheduled-staging"

// backupS3Timeout bounds one S3 call of a scheduled backup — an upload of a
// large database included — so a target that stops answering cannot hold a
// run, and the rule's next runs behind it, for ever.
var backupS3Timeout = time.Hour

// s3BackupTarget keeps backups in an S3-compatible bucket (AWS S3, MinIO,
// Cloudflare R2, GCS and Azure through their S3 interfaces). The client is
// the one S3-backed collections use (NewS3Backend); the objects mirror the
// local files, so any S3 tool can fetch a backup for POST /v1/restore.
type s3BackupTarget struct {
	action *BackupAction
	client *minio.Client
	bucket string
}

// newS3BackupTarget connects to the action's bucket, creating it if missing.
func newS3BackupTarget(a *BackupAction) (*s3BackupTarget, error) {
	cfg := *a.StorageConfig
	cfg.Bucket = a.Bucket
	cfg.Prefix = "" // the action's prefix starts every key itself
	be, err := NewS3Backend(&cfg)
	if err != nil {
		return nil, err
	}
	return &s3BackupTarget{action: a, client: be.client, bucket: be.bucket}, nil
}

func (t *s3BackupTarget) stage(name string) (string, string, error) {
	return stageIn(backupStagingDir, path.Base(name))
}

// store uploads every file of the set under the key prefix of name, then
// reads each object back and compares its sha256 with the local file's.
func (t *s3BackupTarget) store(set *backupSet, name string) (string, error) {
	keyDir := path.Dir(name)
	if keyDir == "." {
		keyDir = ""
	}
	root, err := os.OpenRoot(set.dir) // NOSONAR gosecurity:S2083 -- the staging directory, confined by safeBackupPath
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()

	for _, f := range set.files {
		key := path.Join(keyDir, f.rel)
		if err := t.upload(root, f, key); err != nil {
			return "", fmt.Errorf("uploading %s: %w", key, err)
		}
	}
	return "s3://" + t.bucket + "/" + name, nil
}

// upload puts one file at key and proves the object holds the bytes whose
// sha256 the set recorded.
func (t *s3BackupTarget) upload(root *os.Root, file backupSetFile, key string) error {
	f, err := root.Open(file.rel) // NOSONAR gosecurity:S2083 -- os.Root: cannot leave dir
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), backupS3Timeout)
	defer cancel()
	if _, err := t.client.PutObject(ctx, t.bucket, key, f, file.size,
		minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
		return err
	}

	// minio-go's GetObject only checks its arguments; the request, and any
	// failure of it, comes with the first read.
	var got string
	obj, err := t.client.GetObject(ctx, t.bucket, key, minio.GetObjectOptions{})
	if err == nil {
		defer func() { _ = obj.Close() }()
		got, err = sha256Hex(obj)
	}
	if err != nil {
		return fmt.Errorf("reading it back: %w", err)
	}
	if got != file.sum {
		return fmt.Errorf("the stored object's sha256 is %s, the backup's %s", got, file.sum)
	}
	return nil
}

// discard empties the staging copy: the backup lives in the bucket now, or
// the run failed.
func (t *s3BackupTarget) discard(dir, base string, _ bool) {
	_ = removeBackupSet(dir, base)
}

// list returns every key under the action's prefix; the caller keeps the
// backups' main files.
func (t *s3BackupTarget) list() ([]string, error) {
	return t.keys(t.action.Prefix)
}

func (t *s3BackupTarget) keys(prefix string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), backupS3Timeout)
	defer cancel()
	var keys []string
	for obj := range t.client.ListObjects(ctx, t.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}

// remove deletes a backup's main object, checksum object and collection
// objects.
func (t *s3BackupTarget) remove(name string) error {
	keys, err := t.keys(name + ".d/")
	if err != nil {
		return err
	}
	keys = append(keys, sumFileName(name), name)
	ctx, cancel := context.WithTimeout(context.Background(), backupS3Timeout)
	defer cancel()
	var errs []error
	for _, k := range keys {
		// S3 deletes are idempotent: a key already gone is no error.
		if err := t.client.RemoveObject(ctx, t.bucket, k, minio.RemoveObjectOptions{}); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", k, err))
		}
	}
	return errors.Join(errs...)
}
