package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mddb/internal/binlog"
	"mddb/internal/temporal"

	bolt "go.etcd.io/bbolt"
)

// #336: the per-collection layout end to end, inside the normal test run.

func perCollectionServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("MDDB_STORAGE_LAYOUT", "per-collection")
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(func() { s.closeCollectionFiles(); cleanup() })
	return s
}

// closeStorage closes every file that holds data, so each storage call that
// follows fails in either layout: the main file and, in the per-collection
// layout, every open collection file. The named collections get their file
// first, so a write to a collection that has none yet cannot open a fresh
// one and succeed.
func closeStorage(t *testing.T, s *Server, collections ...string) {
	t.Helper()
	for _, c := range collections {
		if _, err := s.collectionDBForWrite(c); err != nil {
			t.Fatalf("opening the file of %s: %v", c, err)
		}
	}
	for _, db := range s.collectionFileDBs() {
		if err := db.Close(); err != nil {
			t.Fatalf("closing a collection file: %v", err)
		}
	}
	if err := s.DB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestPerCollectionWritesLandInTheirOwnFiles(t *testing.T) {
	s := perCollectionServer(t)
	addTestDoc(t, s, "blog", "a", "en", "zebra crossing", nil)
	addTestDoc(t, s, "wiki", "x", "en", "zebra stripes", nil)

	if held, _ := mainFileHoldsCollections(s.DB); held {
		t.Error("collection data reached the main file")
	}
	for _, c := range []string{"blog", "wiki"} {
		if _, err := os.Stat(filepath.Join(collectionDir(s.Path), collectionFileName(c))); err != nil {
			t.Errorf("no file for %s: %v", c, err)
		}
	}
	if s.collectionDB("never") != s.DB {
		t.Error("a collection without a file should read from the main file")
	}
	if n := ftsHits(t, s, "blog", "zebra"); n != 1 {
		t.Errorf("FTS in a collection file: %d hits", n)
	}
	if got := len(s.collectionDBs()); got != 2 {
		t.Errorf("collectionDBs = %d, want 2", got)
	}
	docs := 0
	_ = s.DBViewAll(func(tx *bolt.Tx) error {
		docs += tx.Bucket(s.BucketNames.Docs).Stats().KeyN
		return nil
	})
	if docs != 2 {
		t.Errorf("DBViewAll saw %d documents, want 2", docs)
	}
	if s.docBytes("blog", genID("blog", "a", "en")) == nil || s.docBytes("blog", "missing") != nil {
		t.Error("docBytes")
	}
	if (&serverMetricsStats{s: s}).DBWriteTransactions() == 0 {
		t.Error("the commit counter ignores collection files")
	}
	usage, err := s.storageUsage("blog")
	if err != nil || usage.Collections["blog"].FileBytes == 0 || usage.Collections["blog"].ByCategory["content"] == 0 {
		t.Errorf("storage breakdown of a collection file: %+v %v", usage.Collections["blog"], err)
	}
	if full, _ := s.storageUsage(""); full.Collections["wiki"] == nil || full.Buckets["docs"].Keys != 2 {
		t.Errorf("full breakdown: %+v", full.Buckets["docs"])
	}
}

func TestPerCollectionDeleteRemovesTheFile(t *testing.T) {
	s := perCollectionServer(t)
	addTestDoc(t, s, "gone", "a", "en", "x", nil)
	path := filepath.Join(collectionDir(s.Path), collectionFileName("gone"))
	if _, err := s.deleteCollection("gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("deleting a collection left its file")
	}
}

func TestPerCollectionBackupAndRestore(t *testing.T) {
	s := perCollectionServer(t)
	addTestDoc(t, s, "blog", "a", "en", "kept", nil)
	backup := filepath.Join(testBackupDir(t), "backup.db")
	if err := s.backupTo(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(collectionDir(backup), "blog.db")); err != nil {
		t.Fatalf("the backup has no collection file: %v", err)
	}
	addTestDoc(t, s, "blog", "b", "en", "after the backup", nil)
	if err := s.restoreFromBackup(backup); err != nil {
		t.Fatal(err)
	}
	if got := countDocs(t, s, "blog"); got != 1 {
		t.Errorf("after restore: %d documents, want 1", got)
	}
	if _, err := os.Stat(collectionDir(s.Path) + ".pre-restore"); !os.IsNotExist(err) {
		t.Error("the set-aside collection files were not cleaned up")
	}

	// A backup without collection files cannot be restored into this layout,
	// and the server keeps serving.
	plain := filepath.Join(t.TempDir(), "plain.db")
	if err := copyFile(s.Path, plain); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreFromBackup(plain); err == nil {
		t.Error("restored a backup with no collection files")
	}
	if got := countDocs(t, s, "blog"); got != 1 {
		t.Errorf("after a refused restore: %d documents", got)
	}
}

func TestPerCollectionReplicationRoutesEntries(t *testing.T) {
	s := perCollectionServer(t)
	ra := NewReplicationApplier(s)
	if err := ra.ApplyBatch([]*binlog.BinlogEntry{
		{LSN: 1, Type: binlog.BinlogPut, BucketName: "docs", Key: []byte("doc|blog|1"), Value: []byte("a")},
		{LSN: 2, Type: binlog.BinlogPut, BucketName: "docs", Key: []byte("doc|wiki|1"), Value: []byte("b")},
		{LSN: 3, Type: binlog.BinlogPut, BucketName: "ttlrev", Key: []byte("blog|1"), Value: []byte("9")},
		{LSN: 4, Type: binlog.BinlogPut, BucketName: "custom", Key: []byte("k"), Value: []byte("v")},
		{LSN: 5, Type: binlog.BinlogCheckpoint},
	}); err != nil {
		t.Fatal(err)
	}
	get := func(db *bolt.DB, bucket, key string) string {
		var v []byte
		_ = db.View(func(tx *bolt.Tx) error {
			if b := tx.Bucket([]byte(bucket)); b != nil {
				v = b.Get([]byte(key))
			}
			return nil
		})
		return string(v)
	}
	cf := s.collectionStore()
	if get(cf.get("blog"), "docs", "doc|blog|1") != "a" || get(cf.get("wiki"), "docs", "doc|wiki|1") != "b" ||
		get(cf.get("blog"), "ttlrev", "blog|1") != "9" || get(s.DB, "custom", "k") != "v" {
		t.Error("entries did not land in their files")
	}
	if err := ra.Apply(&binlog.BinlogEntry{LSN: 6, Type: binlog.BinlogDeleteBucket, BucketName: "docs"}); err != nil {
		t.Fatal(err)
	}
	if get(cf.get("blog"), "docs", "doc|blog|1") != "" || get(cf.get("wiki"), "docs", "doc|wiki|1") != "" {
		t.Error("a deleted collection bucket survived in a collection file")
	}
	if ra.LastAppliedLSN() != 6 {
		t.Errorf("last applied %d", ra.LastAppliedLSN())
	}
}

func TestPerCollectionRefusesSnapshots(t *testing.T) {
	s := perCollectionServer(t)
	rc := &ReplicationClient{server: s}
	if err := rc.requestSnapshot(context.Background()); err != errSnapshotPerCollection {
		t.Errorf("follower snapshot: %v", err)
	}
}

func TestPerCollectionTemporalEventsGoToTheirFiles(t *testing.T) {
	s := perCollectionServer(t)
	tm := temporal.NewTemporalManager(s.DB)
	tm.SetRouter(s.router())
	tm.Start()
	tm.RecordAsync("blog", "d1", temporal.EventAccess, "")
	tm.RecordAsync("wiki", "d2", temporal.EventAccess, "")
	hot := func(c string) int {
		entries, _ := tm.GetHotDocs(c, 10, 0)
		return len(entries)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (hot("blog") != 1 || hot("wiki") != 1) {
		time.Sleep(50 * time.Millisecond)
	}
	tm.Stop()
	if hot("blog") != 1 || hot("wiki") != 1 {
		t.Error("temporal events did not reach their collection files")
	}
	if rotationKeyCollection("docs", []byte("doc|blog|1")) != "blog" {
		t.Error("rotationKeyCollection")
	}
}

func TestKeyCollectionForms(t *testing.T) {
	for _, c := range []struct{ bucket, key, want string }{
		{"docs", "doc|blog|1", "blog"},
		{"fts", "ftsstat|blog", "blog"},
		{"ttl", "00000000000000000099|blog|1", "blog"},
		{"ttlrev", "blog|1", "blog"},
		{"vector_provenance", "blog", "blog"},
	} {
		if got, ok := keyCollection(c.bucket, []byte(c.key)); !ok || got != c.want {
			t.Errorf("%s %q -> %q %v", c.bucket, c.key, got, ok)
		}
	}
	for _, c := range [][2]string{{"docs", "nobar"}, {"ttlrev", "|x"}, {"vector_provenance", ""}, {"fts", "tag|"}} {
		if _, ok := keyCollection(c[0], []byte(c[1])); ok {
			t.Errorf("%v accepted", c)
		}
	}
}
