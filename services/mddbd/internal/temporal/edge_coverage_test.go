package temporal

import (
	"mddb/internal/testsync"
	"testing"
	"time"
)

// TestTemporalQueryRangeExcludesOutOfWindow covers the timestamp-filter continue
// branch in QueryRange/scanCollectionEvents: events outside [from,to) are skipped.
func TestTemporalQueryRangeExcludesOutOfWindow(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	tm := NewTemporalManager(db)
	if err := tm.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	tm.Start()
	defer tm.Stop()

	now := time.Now().Unix()
	tm.RecordAsync("col", "d1", EventAccess, "u")
	testsync.Wait(t, "the recorded event to be queryable", func() bool {
		ev, _ := tm.QueryRange("col", "d1", now-60, now+60, "", 100)
		return len(ev) >= 1
	})

	// A future window excludes the just-recorded event (ts < from -> continue).
	ev, err := tm.QueryRange("col", "d1", now+1000, now+2000, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 0 {
		t.Errorf("future window should exclude all events, got %d", len(ev))
	}
}
