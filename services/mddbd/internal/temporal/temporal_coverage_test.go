package temporal

import (
	"mddb/internal/testsync"
	"testing"
	"time"
)

func TestTemporalQueriesCoverage(t *testing.T) {
	db, cleanup := newTestDB(t)
	defer cleanup()
	tm := NewTemporalManager(db)
	if err := tm.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	tm.SetBinlog(nil)
	tm.Start()
	defer tm.Stop()

	now := time.Now().Unix()
	for _, d := range []string{"d1", "d2", "d3"} {
		tm.RecordAsync("col", d, EventCreate, "admin")
		tm.RecordAsync("col", d, EventAccess, "u1")
	}
	tm.RecordAsync("col", "d1", EventUpdate, "u2")
	tm.RecordAsync("zcol", "z1", EventAccess, "u1") // 2nd collection -> prefix-break branch

	// Poll for the async writer to flush (mirrors the existing tests).
	testsync.Wait(t, "three events to be queryable", func() bool {
		ev, _ := tm.QueryRange("col", "d1", now-60, now+60, "", 100)
		return len(ev) >= 3
	})

	// QueryRange with an event-type filter and a tight limit.
	if _, err := tm.QueryRange("col", "d1", now-60, now+60, "access", 1); err != nil {
		t.Fatalf("QueryRange(filtered): %v", err)
	}
	// GetHotDocs aggregates access counts.
	if _, err := tm.GetHotDocs("col", 2, now-3600); err != nil {
		t.Fatalf("GetHotDocs: %v", err)
	}
	// ComputeHistogram across every interval branch, plus an event-type filter.
	for _, iv := range []string{"day", "week", "month"} {
		if _, err := tm.ComputeHistogram("col", "", iv, now-90*86400, now+86400); err != nil {
			t.Fatalf("ComputeHistogram(%s): %v", iv, err)
		}
	}
	// Default-parameter and empty-collection branches.
	_, _ = tm.QueryRange("col", "d1", now-60, now+60, "", 0)
	_, _ = tm.GetHotDocs("col", 0, 0)
	_, _ = tm.GetHotDocs("nonexistent", 5, now)
	_, _ = tm.GetHotDocs("col", 5, now+86400) // since in future -> skip-branch
	_, _ = tm.ComputeHistogram("nonexistent", "", "day", now-86400, now+86400)
	_, _ = tm.ComputeHistogram("col", "", "day", now-5, now-4) // narrow range -> timestamp-skip branch
	if _, err := tm.ComputeHistogram("col", "access", "day", now-86400, now+86400); err != nil {
		t.Fatalf("ComputeHistogram(filtered): %v", err)
	}
}
