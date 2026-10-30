package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"mddb/internal/automationlog"
)

// s3SyncAction is a sync of the prefix "site/" of the bucket "docs" on f.
func s3SyncAction(f *fakeS3) *SyncAction {
	return &SyncAction{Source: SyncSourceS3, Prefix: "site/", Collection: "kb", Lang: "en", Prune: true,
		StorageConfig: &StorageConfigDef{Endpoint: f.endpoint(), Bucket: "docs", Region: "us-east-1",
			AccessKey: "test", SecretKey: "test-secret"}}
}

// putObject stores an object in f directly.
func (f *fakeS3) putObject(bucket, key, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buckets[bucket] == nil {
		f.buckets[bucket] = map[string][]byte{}
	}
	f.buckets[bucket][key] = []byte(body)
}

// An S3 prefix: objects below it become documents, an object whose listed
// ETag has not changed is not even downloaded, a changed one is updated, a
// removed one deleted with prune.
func TestSyncS3Prefix(t *testing.T) {
	s := newBackupTestServer(t)
	f := newFakeS3(t)
	f.putObject("docs", "site/a.md", "---\ntitle: A\n---\nalpha")
	f.putObject("docs", "site/sub/b.html", page("B", "beta"))
	f.putObject("docs", "site/dir/", "")
	f.putObject("docs", "other/x.md", "outside the prefix")
	rule := syncRule(t, s, s3SyncAction(f))
	if got := s.AutomationManager.Get(rule.ID).Sync.StorageConfig; got.SecretKey != "" || !got.SecretKeySet {
		t.Errorf("the read rule shows its secret: %+v", got)
	}

	out, err := runSync(t, s, rule)
	if err != nil || out.counts != (automationlog.SyncCounts{Added: 2}) {
		t.Fatalf("first run: %+v, %v", out, err)
	}
	if d, err := s.loadDocByRef("kb", "sub-b", "en"); err != nil || !strings.Contains(d.ContentMD, "# B") ||
		metaFirst(d.Meta, syncMetaSource) != "site/sub/b.html" {
		t.Fatalf("the html object: %+v, %v", d, err)
	}

	// Every GET would now return other bytes: an unchanged run proves none
	// was made.
	f.corrupt = true
	if out, err := runSync(t, s, rule); err != nil || out.counts != (automationlog.SyncCounts{Unchanged: 2}) {
		t.Fatalf("second run: %+v, %v", out, err)
	}
	f.corrupt = false

	f.putObject("docs", "site/sub/b.html", page("B", "beta, revised"))
	f.mu.Lock()
	delete(f.buckets["docs"], "site/a.md")
	f.mu.Unlock()
	if out, err := runSync(t, s, rule); err != nil || out.counts != (automationlog.SyncCounts{Updated: 1, Deleted: 1}) {
		t.Fatalf("third run: %+v, %v", out, err)
	}
	if _, err := s.loadDocByRef("kb", "a", "en"); !errors.Is(err, errDocNotFound) {
		t.Errorf("the removed object's document: %v", err)
	}

	// A failing download fails that object; a failing listing the run.
	f.putObject("docs", "site/sub/b.html", page("B", "third"))
	f.failNext(http.MethodGet, http.StatusForbidden)
	if out, err := runSync(t, s, rule); err == nil || out.counts.Failed != 1 {
		t.Errorf("a failing GET: %+v, %v", out, err)
	}
	f.failNext("LIST", http.StatusForbidden)
	if _, err := runSync(t, s, rule); err == nil || !strings.Contains(err.Error(), "listing the source") {
		t.Errorf("a failing listing: %v", err)
	}
}

// The S3 source's own bounds and answers.
func TestS3SyncSourceBounds(t *testing.T) {
	f := newFakeS3(t)
	f.putObject("docs", "site/1.md", "one")
	f.putObject("docs", "site/2.md", strings.Repeat("x", 64))
	a := s3SyncAction(f)
	a.MaxDocuments, a.MaxBytes = 1, 32
	if err := a.normalize(); err != nil {
		t.Fatal(err)
	}
	src, err := newS3SyncSource(a)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if items, truncated, err := src.list(ctx); err != nil || len(items) != 1 || !truncated {
		t.Errorf("past maxDocuments: %v, %v, %v", items, truncated, err)
	}
	for name, it := range map[string]syncItem{
		"listed size": {ref: "site/2.md", size: 64},
		"read size":   {ref: "site/2.md"},
	} {
		if _, err := src.fetch(ctx, it, syncState{}); err == nil || !strings.Contains(err.Error(), "maxBytes") {
			t.Errorf("%s over maxBytes: %v", name, err)
		}
	}
	if _, err := src.fetch(ctx, syncItem{ref: "site/gone.md"}, syncState{}); !errors.Is(err, errSourceGone) {
		t.Errorf("a vanished object: %v", err)
	}
	if _, err := src.fetch(ctx, syncItem{ref: ""}, syncState{}); err == nil {
		t.Error("an empty object name was fetched")
	}
}

// A sync only reads its source: a missing bucket fails the connection and is
// not created, unlike the bucket of an S3 backup target or collection.
func TestS3SyncSourceDoesNotCreateTheBucket(t *testing.T) {
	f := newFakeS3(t)
	a := s3SyncAction(f)
	a.Bucket = "docs"
	if _, err := newS3SyncSource(a); err == nil || !strings.Contains(err.Error(), `"docs" does not exist`) {
		t.Fatalf("missing bucket: err = %v", err)
	}
	if keys := f.keys("docs"); f.buckets["docs"] != nil || keys != nil {
		t.Errorf("the sync created the bucket")
	}

	f.failNext(http.MethodHead, http.StatusForbidden)
	if _, err := newS3SyncSource(a); err == nil || !strings.Contains(err.Error(), "check S3 bucket") {
		t.Errorf("bucket check failure: err = %v", err)
	}
	if _, err := newS3SyncSource(&SyncAction{Bucket: "docs", StorageConfig: &StorageConfigDef{}}); err == nil {
		t.Error("no endpoint: want an error")
	}
}
