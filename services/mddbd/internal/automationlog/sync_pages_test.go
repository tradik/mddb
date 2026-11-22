package automationlog

import (
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// INT-023: an issues sync's cursor and listing pages are stored with its
// entry and read back.
func TestSyncCursorAndPagesRoundTrip(t *testing.T) {
	ls, done := setupLogStoreTest(t, time.Hour)
	defer done()
	pages := SyncPages{"https://forge.example/issues?page=1": {ETag: `"e1"`, Next: "https://forge.example/issues?page=2"}}
	if err := ls.Log(Entry{RuleID: "r", Status: "success", Sync: &SyncCounts{Cursor: "2026-03-01T10:00:00Z", Pages: &pages}}); err != nil {
		t.Fatal(err)
	}
	got, _, err := ls.List(1, "", "r", "")
	if err != nil || len(got) != 1 || got[0].Sync.Cursor != "2026-03-01T10:00:00Z" ||
		(*got[0].Sync.Pages)["https://forge.example/issues?page=1"].Next != "https://forge.example/issues?page=2" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

// Stop twice, and a cleanup of a database without the bucket, do nothing.
func TestStopTwiceAndCleanupWithoutBucket(t *testing.T) {
	db, err := bolt.Open(filepath.Join(t.TempDir(), "nb.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ls := NewStore(db, time.Hour) // no EnsureBucket
	ls.cleanup()
	ls.Stop()
	ls.Stop()
}
