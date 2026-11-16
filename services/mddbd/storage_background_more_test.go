package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// #333 at its edges: the rebuild started in the background, a prewarm that
// fails, a rebuild that fails, and the progress a refused write reports.

func TestBackgroundFreelistEdges(t *testing.T) {
	var none *backgroundFreelist
	none.start() // nothing to rebuild

	path := newWalkingDB(t)
	t.Setenv("MDDB_FREELIST_BACKGROUND", "")
	t.Setenv("MDDB_PREWARM", "on")
	db, _, bg, err := openDatabase(newStartupClock(), path, getOptimizedBoltOptions())
	if err != nil || bg == nil {
		t.Fatalf("bg=%v err=%v", bg, err)
	}
	defer func() { _ = db.Close() }()
	// A path that is a directory: the prewarm fails, the rebuild goes on.
	bg.path = t.TempDir()
	bg.start()
	deadline := time.Now().Add(10 * time.Second)
	for bg.loading() {
		if time.Now().After(deadline) {
			t.Fatal("the background rebuild never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if bg.failed() != nil {
		t.Fatal(bg.failed())
	}

	// A rebuild on a closed database fails and the database stays read-only.
	closed := newWalkingDB(t)
	db2, _, bg2, err := openDatabase(newStartupClock(), closed, getOptimizedBoltOptions())
	if err != nil || bg2 == nil {
		t.Fatal(err)
	}
	_ = db2.Close()
	bg2.run(db2, closed, bg2.meta)
	s := &Server{freelistLoad: bg2}
	if s.writeBlocked() == nil {
		t.Fatal("writes open after a failed rebuild")
	}
}

func TestARefusedWriteReportsTheRebuildsProgress(t *testing.T) {
	clock := newStartupClock()
	clock.begin("rebuilding freelist")
	clock.setProgress(1, 1000)
	time.Sleep(20 * time.Millisecond)
	clock.setProgress(2, 1000)
	s := &Server{freelistLoad: &backgroundFreelist{clock: clock}}
	rec := httptest.NewRecorder()
	s.writeUnavailable(rec, errFreelistLoading)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "30" {
		t.Fatalf("%d Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	body := map[string]any{}
	s.healthFreelist(body)
	if body["progress"] == nil {
		t.Fatalf("health without progress: %v", body)
	}
}
