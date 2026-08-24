package audit

import (
	"os"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func newSequenceTestManager(t *testing.T) *AuditManager {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "audit_seq_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := NewAuditManager(db, true, 1)
	if err := a.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	return a
}

func (a *AuditManager) countAuditRecords(t *testing.T) int {
	t.Helper()
	n := 0
	if err := a.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketAudit).ForEach(func(_, _ []byte) error {
			n++
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// GO-039: one NextSequence per batch plus a loop offset made consecutive
// batches reuse overlapping sequence ranges — with a repeated timestamp the
// colliding (ts, seq) keys silently overwrote earlier audit events. Every
// event must survive, no matter how coarse the clock.
func TestFlushBatchSameTimestampAcrossBatches_NoOverwrites(t *testing.T) {
	a := newSequenceTestManager(t)

	const ts = int64(1_700_000_000_000_000_000) // one frozen nanosecond
	batch := func(n int) []AuditEvent {
		evs := make([]AuditEvent, n)
		for i := range evs {
			evs[i] = AuditEvent{Timestamp: ts, Action: "write", Result: "ok"}
		}
		return evs
	}

	const batches, perBatch = 4, 5
	for range batches {
		if err := a.flushBatch(batch(perBatch)); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := a.countAuditRecords(t), batches*perBatch; got != want {
		t.Fatalf("audit records = %d, want %d — colliding (ts, seq) keys overwrote events", got, want)
	}
}
