package audit

import (
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// TestAuditQueryPurgeEdgeBranches drives the rarely-hit branches of Query and
// PurgeOlderThan: the malformed-short-key skip, the result-limit truncation and
// the ToNanos upper-bound continue.
func TestAuditQueryPurgeEdgeBranches(t *testing.T) {
	db := newAuditTestDB(t)
	am := NewAuditManager(db, true, 30)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	am.Start()
	now := time.Now().UnixNano()
	for i := 0; i < 6; i++ {
		am.Record(AuditEvent{Timestamp: now + int64(i), Actor: "admin", Action: "write", Result: "ok"})
	}
	am.Stop() // drain + flush

	// A malformed key shorter than the 8-byte timestamp prefix must be skipped
	// by both Query and PurgeOlderThan rather than mis-parsed.
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAudit).Put([]byte("xx"), []byte("{}"))
	}); err != nil {
		t.Fatal(err)
	}

	// Limit truncation: 6 matching events, limit 2 -> exactly 2 returned.
	got, err := am.Query(QueryFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("limit truncation: got %d events, want 2", len(got))
	}

	// ToNanos below most events exercises the ts > ToNanos continue branch.
	if _, err := am.Query(QueryFilter{ToNanos: now + 1, Limit: 100}); err != nil {
		t.Fatal(err)
	}

	// Purge with the short key present: real events deleted, short key skipped.
	if err := am.PurgeOlderThan(now + 100); err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	remaining, err := am.Query(QueryFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Errorf("after purge: %d events remain, want 0", len(remaining))
	}
}

// TestAuditNilReceiver covers the `a == nil` guard branches: every exported
// method must be a safe no-op on a nil manager (audit is optional).
func TestAuditNilReceiver(t *testing.T) {
	var am *AuditManager
	am.Record(AuditEvent{Actor: "x"}) // must not panic
	if got := am.Exporters(); got != nil {
		t.Errorf("nil Exporters() = %v, want nil", got)
	}
	if got := am.Dropped(); got != 0 {
		t.Errorf("nil Dropped() = %d, want 0", got)
	}
	if got, err := am.Query(QueryFilter{}); err != nil || got != nil {
		t.Errorf("nil Query() = (%v, %v), want (nil, nil)", got, err)
	}
	if err := am.PurgeOlderThan(0); err != nil {
		t.Errorf("nil PurgeOlderThan() = %v, want nil", err)
	}
}

// TestAuditFlushBatchMissingBucket covers flushBatch's bucket-missing error
// branch: writing before EnsureBuckets must surface an error, not panic.
func TestAuditFlushBatchMissingBucket(t *testing.T) {
	db := newAuditTestDB(t)
	am := NewAuditManager(db, true, 30) // no EnsureBuckets
	err := am.flushBatch([]AuditEvent{{Timestamp: time.Now().UnixNano(), Actor: "x", Result: "ok"}})
	if err == nil {
		t.Error("flushBatch without the audit bucket should return an error")
	}
}

// TestAuditTrimOnce covers the retention trimmer logic (extracted from the
// hourly ticker): events older than retentionDays are purged, newer ones kept.
func TestAuditTrimOnce(t *testing.T) {
	db := newAuditTestDB(t)
	am := NewAuditManager(db, true, 1) // 1-day retention
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	am.Start()
	now := time.Now().UnixNano()
	old := now - int64(48*time.Hour) // 2 days ago -> beyond retention
	am.Record(AuditEvent{Timestamp: old, Actor: "admin", Action: "old", Result: "ok"})
	am.Record(AuditEvent{Timestamp: now, Actor: "admin", Action: "fresh", Result: "ok"})
	am.Stop()

	am.trimOnce()

	got, err := am.Query(QueryFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Action != "fresh" {
		t.Errorf("trimOnce should keep only the fresh event, got %d: %+v", len(got), got)
	}
}

// TestAuditRecordAutoFills covers Record's defaulting branches: a zero timestamp
// is stamped with now and an empty result defaults to "ok".
func TestAuditRecordAutoFills(t *testing.T) {
	db := newAuditTestDB(t)
	am := NewAuditManager(db, true, 30)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	am.Start()
	am.Record(AuditEvent{Actor: "admin", Action: "login"}) // no Timestamp, no Result
	am.Stop()

	got, err := am.Query(QueryFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Timestamp == 0 {
		t.Error("Record should stamp a zero timestamp with now")
	}
	if got[0].Result != "ok" {
		t.Errorf("Record should default empty result to ok, got %q", got[0].Result)
	}
}

// TestAuditRecordDropsWhenBufferFull covers Record's non-blocking drop: with no
// writer draining the buffer, the event past its capacity is counted, not queued.
func TestAuditRecordDropsWhenBufferFull(t *testing.T) {
	am := NewAuditManager(newAuditTestDB(t), true, 30) // not started
	for i := 0; i <= cap(am.ch); i++ {
		am.Record(AuditEvent{Actor: "x"})
	}
	if got := am.Dropped(); got != 1 {
		t.Errorf("Dropped() = %d, want 1", got)
	}
}

// TestAuditWriterFlushesFullBatchAndSurvivesErrors covers the writer flushing
// as soon as a batch reaches 64 events, and logging (not dying on) a failed flush.
func TestAuditWriterFlushesFullBatchAndSurvivesErrors(t *testing.T) {
	db := newAuditTestDB(t)
	am := NewAuditManager(db, true, 30)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	am.Start()
	now := time.Now().UnixNano()
	for i := 0; i < 64; i++ {
		am.Record(AuditEvent{Timestamp: now + int64(i), Action: "write"})
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := am.Query(QueryFilter{Limit: 100})
		if err == nil && len(got) == 64 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("full batch never flushed: %d events, %v", len(got), err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	am.Stop()

	broken := NewAuditManager(newAuditTestDB(t), true, 30) // no bucket: every flush fails
	broken.Start()
	broken.Record(AuditEvent{Action: "write"})
	broken.Stop() // drains, the flush fails and is logged
}

// TestWriteAuditBatchReadOnlyBucket covers the NextSequence error: a bucket
// from a read-only transaction cannot hand out sequence numbers.
func TestWriteAuditBatchReadOnlyBucket(t *testing.T) {
	db := newAuditTestDB(t)
	am := NewAuditManager(db, true, 30)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	err := db.View(func(tx *bolt.Tx) error {
		return writeAuditBatch(tx.Bucket(bucketAudit), []AuditEvent{{Action: "write"}})
	})
	if err == nil {
		t.Error("writeAuditBatch in a read-only transaction should fail")
	}
}

// TestAuditMissingBucketAndClosedDB covers Query and PurgeOlderThan before the
// bucket exists (no-ops) and trimOnce logging a purge that fails.
func TestAuditMissingBucketAndClosedDB(t *testing.T) {
	db := newAuditTestDB(t)
	am := NewAuditManager(db, true, 30) // no EnsureBuckets
	if got, err := am.Query(QueryFilter{}); err != nil || got != nil {
		t.Errorf("Query without bucket = (%v, %v), want (nil, nil)", got, err)
	}
	if err := am.PurgeOlderThan(time.Now().UnixNano()); err != nil {
		t.Errorf("PurgeOlderThan without bucket = %v, want nil", err)
	}
	_ = db.Close()
	am.trimOnce() // the purge fails on the closed database; logged, no panic
}

// TestAuditQuerySkipsUndecodableAndFiltersResult covers Query skipping a value
// that is not JSON and filtering on Result.
func TestAuditQuerySkipsUndecodableAndFiltersResult(t *testing.T) {
	db := newAuditTestDB(t)
	am := NewAuditManager(db, true, 30)
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixNano()
	if err := am.flushBatch([]AuditEvent{
		{Timestamp: now, Action: "login", Result: "ok"},
		{Timestamp: now + 1, Action: "login", Result: "fail"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAudit).Put(auditKey(now+2, 999), []byte("not json"))
	}); err != nil {
		t.Fatal(err)
	}
	got, err := am.Query(QueryFilter{Result: "fail", Limit: 10})
	if err != nil || len(got) != 1 || got[0].Result != "fail" {
		t.Errorf("Query(result=fail) = %+v, %v", got, err)
	}
}
