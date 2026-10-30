package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	json "mddb/internal/jsonx"
	pb "mddb/proto"
)

// #349: /v1/stats answers from a shared snapshot instead of walking the
// database on every call.

func statsBody(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleStats(rec, httptest.NewRequest(http.MethodGet, "/v1/stats", nil))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return out
}

func TestStatsSnapshotStaysCurrentOnASmallDatabase(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "a", "en", "one", map[string][]string{"tag": {"x"}})
	first := s.dbStats(statsWait())
	if first == nil || first.Collections["blog"].Documents != 1 || first.Collections["blog"].MetaIndices == 0 || first.Collections["blog"].Checksum == "" {
		t.Fatalf("first snapshot: %+v", first)
	}
	if again := s.dbStats(statsWait()); again != first {
		t.Error("no write since: the snapshot should be reused, not walked again")
	}
	addTestDoc(t, s, "blog", "b", "en", "two", nil)
	if body := statsBody(t, s); body["totalDocuments"].(float64) != 2 || body["statsPending"] != nil || body["statsComputedAt"] == "" {
		t.Errorf("after a write on a small database: %v", body)
	}
}

func TestStatsSnapshotOnAnExpensiveDatabase(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "a", "en", "one", nil)
	snap := s.dbStats(statsWait())
	s.walks.entries["stats"].took = 2 * cheapWalk // pretend walks are expensive
	addTestDoc(t, s, "blog", "b", "en", "two", nil)

	// Not past the TTL: the old snapshot, at once, and no walk.
	t.Setenv("MDDB_STATS_TTL", "3600")
	if got := s.dbStats(statsWait()); got != snap || got.Collections["blog"].Documents != 1 {
		t.Errorf("an expensive database should get the old snapshot: %+v", got)
	}
	// Past the TTL: still the old snapshot at once; the walk runs behind it.
	t.Setenv("MDDB_STATS_TTL", "0")
	if got := s.dbStats(statsWait()); got != snap {
		t.Error("the caller waited for an expensive walk")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.walks.mu.Lock()
		cur, _ := s.walks.entries["stats"].val.(*dbStatsSnapshot)
		s.walks.mu.Unlock()
		if cur != snap {
			if cur.Collections["blog"].Documents != 2 {
				t.Errorf("background walk: %+v", cur.Collections["blog"])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the background walk never published")
}

func TestStatsPendingAndOtherTransports(t *testing.T) {
	gs, s, cleanup := newTestGRPCServerFull(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "a", "en", "one", nil)

	// No snapshot yet and no time to wait: pending, not a hang.
	t.Setenv("MDDB_STATS_WAIT", "0")
	if body := statsBody(t, s); body["statsPending"] != true {
		t.Errorf("first call without waiting: %v", body)
	}
	if _, err := NewDirectClient(s).Stats(context.Background()); err == nil && s.dbStats(0) == nil {
		t.Error("MCP stats should report a pending snapshot")
	}
	t.Setenv("MDDB_STATS_WAIT", "2")
	resp, err := gs.Stats(context.Background(), &pb.StatsRequest{})
	if err != nil || resp.GetTotalDocuments() != 1 || len(resp.GetCollections()) != 1 {
		t.Errorf("gRPC stats: %v %v", resp, err)
	}
	m, err := NewDirectClient(s).Stats(context.Background())
	if err != nil || m.TotalDocuments != 1 {
		t.Errorf("MCP stats: %+v %v", m, err)
	}
	if ds := (&serverMetricsStats{s: s}).DBStats(); ds.Collections["blog"].Documents != 1 {
		t.Errorf("metrics: %+v", ds.Collections)
	}
	if names := s.dbStats(0).sortedNames(); len(names) != 1 || names[0] != "blog" {
		t.Errorf("names %v", names)
	}
	for k, want := range map[string]bool{"doc|blog|1": true, "nobar": false, "tag||x": false} {
		if _, ok := statsCollection([]byte(k)); ok != want {
			t.Errorf("statsCollection(%q) = %v", k, ok)
		}
	}
}

func TestStatsWalkEdges(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "a", "en", "one", nil)
	if err := s.VectorStore.Put("blog", genID("blog", "a", "en"), []float32{1, 2, 3}, "m", "h"); err != nil {
		t.Fatal(err)
	}
	slowWalk = 0 // log every walk
	defer func() { slowWalk = 10 * time.Second }()
	if snap := s.dbStats(statsWait()); snap == nil || snap.Collections["blog"].Embeddings != 1 {
		t.Fatalf("embeddings in the snapshot: %+v", snap)
	}

	// A walk still running when the wait ends: the caller is not held.
	fresh, cleanup2 := newHandlerTestServer(t)
	defer cleanup2()
	block := make(chan struct{})
	val, _, pending := fresh.cachedWalk("slow", 20*time.Millisecond, func() (any, error) { <-block; return 1, nil })
	if val != nil || !pending {
		t.Errorf("a walk still running: %v %v", val, pending)
	}
	// Wait for the walk to end: it reads slowWalk, which the deferred reset
	// above writes when the test returns.
	fresh.walks.mu.Lock()
	running := fresh.walks.entries["slow"].running
	fresh.walks.mu.Unlock()
	close(block)
	<-running

	// A failed walk keeps what there was; with nothing before, nothing.
	// (Single-file layout: with per-collection files the walk reads only
	// those, and a closed main file is not read at all.)
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	broken, cleanup3 := newHandlerTestServer(t)
	cleanup3()
	if _, err := broken.walkStats(); err == nil {
		t.Error("walking a closed database should fail")
	}
	e := &walkEntry{running: make(chan struct{})}
	s.runWalk(e, 7, func() (any, error) { return nil, context.Canceled })
	if e.val != nil || e.running != nil {
		t.Errorf("a failed walk: %+v", e)
	}
}

func TestStorageAnswersFromTheWalkCache(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "blog", "a", "en", "one", nil)
	rec := httptest.NewRecorder()
	s.handleStorage(rec, httptest.NewRequest(http.MethodGet, "/v1/storage", nil))
	var out storageBreakdown
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.StatsComputedAt == "" || out.Collections["blog"] == nil {
		t.Fatalf("storage: %s", rec.Body.String())
	}
	// Pending while a walk for that key has not finished.
	block := make(chan struct{})
	defer close(block)
	go s.cachedWalk("storage:slow", 0, func() (any, error) { <-block; return &storageBreakdown{}, nil })
	time.Sleep(20 * time.Millisecond)
	t.Setenv("MDDB_STATS_WAIT", "0")
	s.walks.mu.Lock()
	s.walks.entries["storage:slow"].running = make(chan struct{})
	s.walks.mu.Unlock()
	rec = httptest.NewRecorder()
	s.handleStorage(rec, httptest.NewRequest(http.MethodGet, "/v1/storage?collection=slow", nil))
	if !strings.Contains(rec.Body.String(), "statsPending") {
		t.Errorf("pending storage: %s", rec.Body.String())
	}
}

// After a restore the statistics describe the restored database, not the
// one before it. The expensive path served the old snapshot for up to the
// TTL, with its age, as if nothing had happened.
func TestStatsForgetTheOldDatabaseAfterARestore(t *testing.T) {
	t.Setenv("MDDB_STORAGE_LAYOUT", "single")
	s, cleanup := newTestServerForCollectionConfig(t)
	defer cleanup()
	defer func() { _ = s.DB.Close() }() // the restore opens a handle cleanup does not know

	backup := filepath.Join(testBackupDir(t), "b.db")
	if err := s.backupTo(backup); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "later", "a", "en", "written after the backup", nil)
	if snap := s.dbStats(time.Second); snap == nil || snap.Collections["later"] == nil {
		t.Fatal("no snapshot of the current database")
	}
	s.walks.entries["stats"].took = 2 * cheapWalk // pretend walks are expensive

	if err := s.restoreFromBackup(backup); err != nil {
		t.Fatal(err)
	}
	snap := s.dbStats(2 * time.Second)
	if snap == nil {
		t.Fatal("no snapshot after the restore")
	}
	if snap.Collections["later"] != nil {
		t.Error("the snapshot still describes the database before the restore")
	}
}

// A tenant user sees only its namespace in vector-stats and storage, as in
// stats (MULTI_TENANCY.md). They listed every collection, with its counts
// and bytes.
func TestVectorStatsAndStorageAreScopedToTheTenant(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "acme/docs", "a", "en", "tenant text", nil)
	addTestDoc(t, s, "other/docs", "a", "en", "another tenant", nil)
	addTestDoc(t, s, "global", "a", "en", "no tenant", nil)

	asTenant := func(method, target string) *http.Request {
		r := httptest.NewRequest(method, target, nil)
		return r.WithContext(context.WithValue(r.Context(), authContextKey, &JWTClaims{Username: "u", Tenant: "acme"}))
	}
	keysOf := func(body []byte, field string) map[string]bool {
		var out map[string]json.RawMessage
		_ = json.Unmarshal(body, &out)
		var m map[string]json.RawMessage
		_ = json.Unmarshal(out[field], &m)
		seen := map[string]bool{}
		for k := range m {
			seen[k] = true
		}
		return seen
	}

	w := httptest.NewRecorder()
	s.handleVectorStats(w, asTenant(http.MethodGet, "/v1/vector-stats"))
	if got := keysOf(w.Body.Bytes(), "collections"); !got["acme/docs"] || got["other/docs"] || got["global"] {
		t.Errorf("vector-stats as a tenant: %v", got)
	}
	w = httptest.NewRecorder()
	s.handleVectorStats(w, httptest.NewRequest(http.MethodGet, "/v1/vector-stats", nil))
	if got := keysOf(w.Body.Bytes(), "collections"); !got["acme/docs"] || !got["other/docs"] || !got["global"] {
		t.Errorf("vector-stats without a tenant: %v", got)
	}
	if resp, err := (&GRPCServer{server: s}).VectorStats(
		context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "u", Tenant: "acme"}), nil); err != nil ||
		resp.Collections["acme/docs"] == nil || resp.Collections["other/docs"] != nil {
		t.Errorf("gRPC vector stats as a tenant: %v %v", resp.GetCollections(), err)
	}

	w = httptest.NewRecorder()
	s.handleStorage(w, asTenant(http.MethodGet, "/v1/storage"))
	if got := keysOf(w.Body.Bytes(), "collections"); w.Code != 200 || !got["acme/docs"] || got["other/docs"] || got["global"] {
		t.Errorf("storage as a tenant: %d %v", w.Code, got)
	}
	w = httptest.NewRecorder()
	s.handleStorage(w, asTenant(http.MethodGet, "/v1/storage?collection=other/docs"))
	if w.Code != http.StatusForbidden {
		t.Errorf("another tenant's collection: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleStorage(w, httptest.NewRequest(http.MethodGet, "/v1/storage", nil))
	if got := keysOf(w.Body.Bytes(), "collections"); !got["other/docs"] || !got["global"] {
		t.Errorf("storage without a tenant: %v", got)
	}
}
