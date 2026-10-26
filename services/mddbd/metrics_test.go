package main

import (
	"mddb/internal/metrics"
	"mddb/internal/schema"
	"mddb/internal/vector"
	"mddb/internal/webhooks"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func newTestServerForMetrics(t *testing.T) (*Server, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "mddb-metrics-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	s := &Server{
		DB:   db,
		Path: f.Name(),
		Mode: ModeRW,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
	}
	if err := s.ensureBuckets(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	s.VectorStore = vector.NewVectorStore(db)
	_ = s.VectorStore.EnsureBucket()
	s.VectorIndex = vector.NewVectorIndex()
	s.VectorSearchers = map[string]vector.VectorSearcher{
		"flat": s.VectorIndex,
	}
	s.WebhookManager = webhooks.NewWebhookManager(db)
	_ = s.WebhookManager.EnsureBucket()
	_ = s.WebhookManager.LoadAll()
	s.SchemaManager = schema.NewSchemaManager(db)
	_ = s.SchemaManager.EnsureBucket()
	_ = s.SchemaManager.LoadAll()

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return s, cleanup
}

func TestMetricsEndpointReturnsPrometheusFormat(t *testing.T) {
	s, cleanup := newTestServerForMetrics(t)
	defer cleanup()

	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	s.Metrics.HandleMetrics(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain content-type, got %q", ct)
	}

	body := rec.Body.String()

	// Check required metrics are present
	required := []string{
		"mddb_info",
		"mddb_uptime_seconds",
		"mddb_database_size_bytes",
		"mddb_documents_total",
		"mddb_vector_index_ready",
		"mddb_embedding_provider_configured",
		"mddb_webhooks_total",
		"mddb_schemas_total",
		"go_goroutines",
		"go_memstats_alloc_bytes",
		"go_memstats_sys_bytes",
		"go_gc_completed_total",
	}
	for _, m := range required {
		if !strings.Contains(body, m) {
			t.Errorf("missing metric: %s", m)
		}
	}
}

func TestMetricsMiddlewareRecordsRequests(t *testing.T) {
	s, cleanup := newTestServerForMetrics(t)
	defer cleanup()

	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})

	// Create a simple handler to wrap
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	})

	wrapped := s.Metrics.Middleware(inner)

	// Simulate some requests
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/v1/add", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
	}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/v1/search", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
	}

	// Now check /metrics output
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	s.Metrics.HandleMetrics(rec, req)
	body := rec.Body.String()

	if !strings.Contains(body, `mddb_http_requests_total{method="POST",path="/v1/add",status="200"} 5`) {
		t.Error("expected 5 requests to /v1/add")
		t.Log(body)
	}
	if !strings.Contains(body, `mddb_http_requests_total{method="POST",path="/v1/search",status="200"} 3`) {
		t.Error("expected 3 requests to /v1/search")
	}

	// Check histogram is present
	if !strings.Contains(body, "mddb_http_request_duration_seconds_bucket") {
		t.Error("expected histogram buckets")
	}
	if !strings.Contains(body, "mddb_http_request_duration_seconds_sum") {
		t.Error("expected histogram sum")
	}
	if !strings.Contains(body, "mddb_http_request_duration_seconds_count") {
		t.Error("expected histogram count")
	}
}

func TestMetricsDisabled(t *testing.T) {
	s, cleanup := newTestServerForMetrics(t)
	defer cleanup()

	s.Metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})

	// Handler should return 404 when disabled
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	s.Metrics.HandleMetrics(rec, req)
	if rec.Code != 404 {
		t.Fatalf("expected 404 when metrics disabled, got %d", rec.Code)
	}

	// Middleware should pass through without recording
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	wrapped := s.Metrics.Middleware(inner)
	req = httptest.NewRequest("GET", "/health", nil)
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected passthrough 200, got %d", rec.Code)
	}
}

func TestMetricsDBStatsCache(t *testing.T) {
	s, cleanup := newTestServerForMetrics(t)
	defer cleanup()

	// DB-stats gathering + 15s caching now live in the serverMetricsStats adapter.
	a := &serverMetricsStats{s: s}

	_ = a.DBStats() // first call populates the cache
	a.cacheMu.Lock()
	cached := a.cachedDB
	a.cacheMu.Unlock()
	if cached == nil {
		t.Fatal("expected cached dbStats after first DBStats()")
	}

	// Force cache expiry; the next call must re-scan and refresh the stamp.
	a.cacheMu.Lock()
	a.cacheStamp = time.Now().Add(-20 * time.Second)
	a.cacheMu.Unlock()
	_ = a.DBStats()
	a.cacheMu.Lock()
	fresh := time.Since(a.cacheStamp) < 5*time.Second
	a.cacheMu.Unlock()
	if !fresh {
		t.Error("expected cache stamp refreshed after expiry")
	}
}

func BenchmarkMetricsHandler(b *testing.B) {
	f, err := os.CreateTemp("", "mddb-metrics-bench-*.db")
	if err != nil {
		b.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}()

	s := &Server{
		DB:   db,
		Path: f.Name(),
		Mode: ModeRW,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
	}
	if err := s.ensureBuckets(); err != nil {
		b.Fatal(err)
	}
	s.VectorStore = vector.NewVectorStore(db)
	_ = s.VectorStore.EnsureBucket()
	s.VectorIndex = vector.NewVectorIndex()
	s.VectorSearchers = map[string]vector.VectorSearcher{
		"flat": s.VectorIndex,
	}
	s.WebhookManager = webhooks.NewWebhookManager(db)
	_ = s.WebhookManager.EnsureBucket()
	_ = s.WebhookManager.LoadAll()
	s.SchemaManager = schema.NewSchemaManager(db)
	_ = s.SchemaManager.EnsureBucket()
	_ = s.SchemaManager.LoadAll()
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})

	// Simulate some traffic through the public middleware (populates the
	// request counters/histograms without poking internal state).
	mw := s.Metrics.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for i := 0; i < 1000; i++ {
		mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/add", nil))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/metrics", nil)
		rec := httptest.NewRecorder()
		s.Metrics.HandleMetrics(rec, req)
	}
}

// #306. The write-transaction counter is what shows, without dtrace, how many
// commits — each an fsync — a request costs. It must count every commit and
// nothing else.
func TestDBWriteTransactionsCountsCommits(t *testing.T) {
	s, cleanup := newTestServerForMetrics(t)
	defer cleanup()
	stats := &serverMetricsStats{s: s}

	before := stats.DBWriteTransactions()
	for i := 0; i < 3; i++ {
		if err := s.DB.Update(func(tx *bolt.Tx) error {
			_, err := tx.CreateBucketIfNotExists([]byte("commit-counter"))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.DB.View(func(*bolt.Tx) error { return nil })

	if got := stats.DBWriteTransactions() - before; got != 3 {
		t.Errorf("three commits and one read moved the counter by %d, want 3", got)
	}
}
