package main

import (
	"testing"

	"mddb/internal/binlog"
	"mddb/internal/vector"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// covKApply applies one entry and fails the test on error.
func covKApply(t *testing.T, ra *ReplicationApplier, entry *binlog.BinlogEntry) {
	t.Helper()
	if err := ra.Apply(entry); err != nil {
		t.Fatalf("apply %s: %v", entry.BucketName, err)
	}
}

// A write the follower cannot perform stops the batch with an error, and the
// in-memory state and LSN do not advance past it.
func TestCovKApplyBatch_WriteErrorsStopTheBatch(t *testing.T) {
	s, cleanup := applierExtraTestServer(t)
	defer cleanup()
	ra := NewReplicationApplier(s)
	ra.SetLastAppliedLSN(7)
	if ra.LastAppliedLSN() != 7 {
		t.Fatalf("SetLastAppliedLSN: got %d", ra.LastAppliedLSN())
	}

	// A nested bucket under "x" so that deleting its key is incompatible.
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("x"))
		if err != nil {
			return err
		}
		_, err = b.CreateBucket([]byte("nested"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	for name, entry := range map[string]*binlog.BinlogEntry{
		"no bucket name": {LSN: 8, Type: binlog.BinlogPut, BucketName: "", Key: []byte("k"), Value: []byte("v")},
		"empty key":      {LSN: 8, Type: binlog.BinlogPut, BucketName: "x", Key: nil, Value: []byte("v")},
		"delete bucket":  {LSN: 8, Type: binlog.BinlogDelete, BucketName: "x", Key: []byte("nested")},
	} {
		if err := ra.ApplyBatch([]*binlog.BinlogEntry{entry}); err == nil {
			t.Errorf("%s: applied without error", name)
		}
		if ra.LastAppliedLSN() != 7 {
			t.Errorf("%s: LSN advanced to %d on a failed batch", name, ra.LastAppliedLSN())
		}
	}
}

// Deleting a bucket the follower never had is logged, not fatal.
func TestCovKApplyBatch_DeletingAMissingBucketIsTolerated(t *testing.T) {
	s, cleanup := applierExtraTestServer(t)
	defer cleanup()
	ra := NewReplicationApplier(s)
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 3, Type: binlog.BinlogDeleteBucket, BucketName: "never-existed"})
	if ra.LastAppliedLSN() != 3 {
		t.Errorf("LSN = %d, want 3", ra.LastAppliedLSN())
	}
}

// In the per-collection layout a bucket deletion reaches every file; one it
// cannot write fails the batch.
func TestCovKApplyBatch_EverywhereFailsOnAClosedCollectionFile(t *testing.T) {
	s := perCollectionServer(t)
	addTestDoc(t, s, "blog", "a", "en", "x", nil)
	ra := NewReplicationApplier(s)

	if err := s.collectionDB("blog").Close(); err != nil {
		t.Fatal(err)
	}
	err := ra.Apply(&binlog.BinlogEntry{LSN: 1, Type: binlog.BinlogDeleteBucket, BucketName: "docs"})
	if err == nil {
		t.Fatal("bucket deletion over a closed collection file reported success")
	}
}

func TestCovKUpdateInMemoryState_ReloadsManagers(t *testing.T) {
	s, cleanup := applierExtraTestServer(t)
	defer cleanup()
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	s.CollectionManager = NewCollectionManager(s.DB)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	s.AutomationManager = NewAutomationManager(s.DB)
	if err := s.AutomationManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	s.CronScheduler = NewCronScheduler(s)
	ra := NewReplicationApplier(s)

	covKApply(t, ra, &binlog.BinlogEntry{LSN: 1, Type: binlog.BinlogPut, BucketName: "aliases", Key: []byte("latest"), Value: []byte("blog_v2")})
	if got := s.Aliases.Resolve("latest"); got != "blog_v2" {
		t.Errorf("alias after replication resolves to %q", got)
	}

	cfg, err := json.Marshal(&CollectionConfig{Type: "default", Description: "replicated"})
	if err != nil {
		t.Fatal(err)
	}
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 2, Type: binlog.BinlogPut, BucketName: "colmeta", Key: []byte("blog"), Value: cfg})
	if got, found := s.CollectionManager.Get("blog"); !found || got.Description != "replicated" {
		t.Errorf("collection config after replication = %+v, %v", got, found)
	}

	rule, err := json.Marshal(&AutomationRule{ID: "r1", Type: "webhook", Name: "hook", Enabled: true, URL: "https://example.invalid/h"})
	if err != nil {
		t.Fatal(err)
	}
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 3, Type: binlog.BinlogPut, BucketName: "automation", Key: []byte("r1"), Value: rule})
	if got := s.AutomationManager.List("webhook"); len(got) != 1 || got[0].Name != "hook" {
		t.Errorf("automation rules after replication = %+v", got)
	}

	// Without the managers the same entries still apply.
	s.Aliases, s.CollectionManager, s.AutomationManager, s.CronScheduler = nil, nil, nil, nil
	for i, b := range []string{"aliases", "colmeta", "automation"} {
		covKApply(t, ra, &binlog.BinlogEntry{LSN: uint64(10 + i), Type: binlog.BinlogPut, BucketName: b, Key: []byte("k"), Value: []byte("{}")})
	}
	if ra.LastAppliedLSN() != 12 {
		t.Errorf("LSN = %d, want 12", ra.LastAppliedLSN())
	}
}

func TestCovKApplyVector_RefusesUnusableRecords(t *testing.T) {
	s, cleanup := applierExtraTestServer(t)
	defer cleanup()
	ra := NewReplicationApplier(s)

	covKApply(t, ra, &binlog.BinlogEntry{LSN: 1, Type: binlog.BinlogPut, BucketName: "vectors",
		Key: []byte("vec|blog|bad"), Value: []byte("not a record")})
	empty := vector.MarshalEmbeddingRecord(&vector.EmbeddingRecord{DocID: "empty", Model: "m"})
	covKApply(t, ra, &binlog.BinlogEntry{LSN: 2, Type: binlog.BinlogPut, BucketName: "vectors",
		Key: []byte("vec|blog|empty"), Value: empty})

	if n := s.VectorIndex.CollectionSize("blog"); n != 0 {
		t.Errorf("index holds %d vectors, want none from unusable records", n)
	}
}
