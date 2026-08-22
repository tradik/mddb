package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubStats struct{}

func (stubStats) Mode() string                    { return "rw" }
func (stubStats) VectorIndexReady() bool          { return true }
func (stubStats) EmbeddingConfigured() bool       { return true }
func (stubStats) EmbeddingQueueSize() (int, bool) { return 3, true }
func (stubStats) EmbeddingCacheStats() (uint64, uint64, int, bool) {
	return 42, 7, 12, true
}
func (stubStats) ReplicationRole() string { return "leader" }
func (stubStats) BinlogStats() (BinlogStatsView, bool) {
	return BinlogStatsView{CurrentLSN: 5, FileSize: 100, OldestLSN: 1, Subscribers: 2}, true
}
func (stubStats) DBStats() DBStats {
	return DBStats{
		SizeBytes:    1024,
		Collections:  map[string]CollectionStats{"blog": {Documents: 2, Revisions: 1, MetaIndex: 3, Embeddings: 1}},
		WebhookCount: 1,
		SchemaCount:  1,
	}
}

func TestHandleMetricsOutput(t *testing.T) {
	m := NewMetrics(true, stubStats{})

	// Generate some traffic through the middleware, plus an op counter.
	mw := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/add", nil))
	mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/metrics", nil)) // passthrough, not recorded
	m.IncOp("fts_search", "bm25")

	rec := httptest.NewRecorder()
	m.HandleMetrics(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"mddb_info{mode=\"rw\"}", "mddb_uptime_seconds",
		`mddb_http_requests_total{method="POST",path="/v1/add",status="200"} 1`,
		"mddb_http_request_duration_seconds_bucket", "mddb_http_request_duration_seconds_sum",
		`mddb_operations_total{operation="fts_search",label="bm25"} 1`,
		"mddb_database_size_bytes 1024", `mddb_documents_total{collection="blog"} 2`,
		`mddb_revisions_total{collection="blog"} 1`, `mddb_meta_indices_total{collection="blog"} 3`,
		`mddb_vector_embeddings_total{collection="blog"} 1`,
		"mddb_vector_index_ready 1", "mddb_embedding_provider_configured 1",
		"mddb_embedding_queue_size 3", "mddb_webhooks_total 1", "mddb_schemas_total 1",
		"mddb_replication_role 1", "mddb_replication_lsn 5", "mddb_binlog_size_bytes 100",
		"mddb_binlog_oldest_lsn 1", "mddb_replication_followers_connected 2",
		"go_goroutines", "go_memstats_alloc_bytes", "go_gc_completed_total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestHandleMetricsDisabled(t *testing.T) {
	m := NewMetrics(false, stubStats{})
	rec := httptest.NewRecorder()
	m.HandleMetrics(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("disabled HandleMetrics = %d, want 404", rec.Code)
	}
	// Disabled middleware is a passthrough that records nothing.
	mw := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201) }))
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, httptest.NewRequest("GET", "/health", nil))
	if rec2.Code != 201 {
		t.Errorf("disabled middleware passthrough = %d, want 201", rec2.Code)
	}
	m.IncOp("x", "y") // no-op when disabled
}

func TestStatusRecorderWrite(t *testing.T) {
	// Middleware over a handler that Writes without an explicit WriteHeader.
	m := NewMetrics(true, stubStats{})
	mw := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("body"))
	}))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest("GET", "/other", nil))
	if rec.Body.String() != "body" {
		t.Errorf("body = %q", rec.Body.String())
	}
}
