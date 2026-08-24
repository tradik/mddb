package temporal

import (
	"mddb/internal/testsync"
	"os"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func newTestDB(t *testing.T) (*bolt.DB, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "mddb-test-*.db")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	db, err := bolt.Open(f.Name(), 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		t.Fatalf("bolt.Open: %v", err)
	}
	return db, func() {
		if err := db.Close(); err != nil {
			t.Logf("db.Close: %v", err)
		}
		if err := os.Remove(f.Name()); err != nil {
			t.Logf("os.Remove: %v", err)
		}
	}
}

func TestTemporalManager_RecordAndQuery(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	tm := NewTemporalManager(db)
	if err := tm.EnsureBuckets(); err != nil {
		t.Fatalf("EnsureBuckets: %v", err)
	}
	tm.Start()
	defer tm.Stop()

	collection := "testcol"
	docID := "doc1"
	now := time.Now().Unix()

	tm.RecordAsync(collection, docID, EventCreate, "admin")
	tm.RecordAsync(collection, docID, EventAccess, "user1")
	tm.RecordAsync(collection, docID, EventAccess, "user2")

	// Poll for flush rather than sleep once — the background writer can take
	// longer than a fixed wait on a congested runner. This loop was already
	// doing the right thing by hand; it now uses the shared helper, which
	// reports "timed out waiting for …" instead of falling through to a
	// count assertion that does not say what went wrong.
	testsync.Wait(t, "all three recorded events to be queryable", func() bool {
		ev, err := tm.QueryRange(collection, docID, now-60, now+60, "", 100)
		return err == nil && len(ev) >= 3
	})

	events, err := tm.QueryRange(collection, docID, now-60, now+60, "", 100)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(events) < 3 {
		t.Errorf("expected at least 3 events, got %d", len(events))
	}
}

func TestTemporalManager_HotDocs(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	tm := NewTemporalManager(db)
	if err := tm.EnsureBuckets(); err != nil {
		t.Fatalf("EnsureBuckets: %v", err)
	}
	tm.Start()
	defer tm.Stop()

	collection := "hotcol"

	for i := 0; i < 5; i++ {
		tm.RecordAsync(collection, "docA", EventAccess, "u")
	}
	for i := 0; i < 2; i++ {
		tm.RecordAsync(collection, "docB", EventAccess, "u")
	}

	// RecordAsync is fire-and-forget, so the test waits for the recorder to
	// have drained rather than for 600 ms to pass (TEST-004).
	testsync.Wait(t, "both documents to appear in the hot list", func() bool {
		got, err := tm.GetHotDocs(collection, 10, 0)
		return err == nil && len(got) >= 2
	})

	entries, err := tm.GetHotDocs(collection, 10, 0)
	if err != nil {
		t.Fatalf("GetHotDocs: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected hot docs, got none")
	}
	if entries[0].DocID != "docA" {
		t.Errorf("expected docA at top, got %s", entries[0].DocID)
	}
	if entries[0].AccessCount < 5 {
		t.Errorf("expected accessCount >= 5, got %d", entries[0].AccessCount)
	}
}

func TestTemporalManager_Histogram(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()

	tm := NewTemporalManager(db)
	if err := tm.EnsureBuckets(); err != nil {
		t.Fatalf("EnsureBuckets: %v", err)
	}
	tm.Start()
	defer tm.Stop()

	collection := "histcol"
	tm.RecordAsync(collection, "d1", EventAccess, "")
	tm.RecordAsync(collection, "d2", EventAccess, "")

	// Wait for the recorder to have flushed, not for a duration.
	testsync.Wait(t, "the recorded events to reach the histogram", func() bool {
		now := time.Now().Unix()
		b, err := tm.ComputeHistogram(collection, "access", "day", now-3600, now+3600)
		return err == nil && len(b) > 0
	})

	now := time.Now().Unix()
	buckets, err := tm.ComputeHistogram(collection, "access", "day", now-3600, now+3600)
	if err != nil {
		t.Fatalf("ComputeHistogram: %v", err)
	}
	if len(buckets) == 0 {
		t.Error("expected at least one histogram bucket")
	}
	if buckets[0].Count < 2 {
		t.Errorf("expected count >= 2, got %d", buckets[0].Count)
	}
}

func TestIsoWeekStart(t *testing.T) {
	// 2026-W14 should start on 2026-03-30 (Monday)
	got := isoWeekStart(2026, 14)
	want := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("isoWeekStart(2026,14) = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}
