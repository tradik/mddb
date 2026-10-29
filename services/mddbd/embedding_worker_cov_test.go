package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mddb/internal/embedding"
	"mddb/internal/metrics"
	vec "mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// covAFailingEmbedding is a provider whose every Embed call fails.
type covAFailingEmbedding struct{ calls atomic.Int32 }

func (f *covAFailingEmbedding) Embed(context.Context, string, embedding.Role) ([]float32, error) {
	f.calls.Add(1)
	return nil, errors.New("provider down")
}

func (f *covAFailingEmbedding) EmbedBatch(context.Context, []string, embedding.Role) ([][]float32, error) {
	return nil, errors.New("provider down")
}

func (f *covAFailingEmbedding) Model() string   { return "failing" }
func (f *covAFailingEmbedding) Dimensions() int { return 4 }

// covAIndexer records what the worker hands it, can refuse chunks, and can
// claim the document no longer exists.
type covAIndexer struct {
	refuse  bool
	exists  bool
	indexed atomic.Int32
}

func (c *covAIndexer) IndexChunk(string, string, []float32) error {
	if c.refuse {
		return errors.New("dimension mismatch")
	}
	c.indexed.Add(1)
	return nil
}

func (c *covAIndexer) Remove(string, string) {}

func (c *covAIndexer) DocumentExists(string, string) bool { return c.exists }

// covAVectorStore opens a fresh bolt file; withBucket controls whether the
// vectors bucket exists (without it every PutChunks fails).
func covAVectorStore(t *testing.T, withBucket bool) *vec.VectorStore {
	t.Helper()
	db, err := bolt.Open(filepath.Join(t.TempDir(), "w.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	vs := vec.NewVectorStore(db)
	if withBucket {
		if err := vs.EnsureBucket(); err != nil {
			t.Fatal(err)
		}
	}
	return vs
}

// covAOpsText scrapes the operation counters of m.
func covAOpsText(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.HandleMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d", rec.Code)
	}
	return rec.Body.String()
}

// covAMetrics returns an enabled collector backed by a handler test server.
func covAMetrics(t *testing.T) *metrics.Metrics {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	return metrics.NewMetrics(true, &serverMetricsStats{s: s})
}

func TestCovAFlatIndexerRemove(t *testing.T) {
	idx := vec.NewVectorIndex()
	f := flatIndexer{idx}
	if err := f.IndexChunk("c", "d#0", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if idx.CollectionSize("c") != 1 {
		t.Fatalf("size = %d, want 1", idx.CollectionSize("c"))
	}
	f.Remove("c", "d#0")
	if idx.CollectionSize("c") != 0 {
		t.Fatalf("size after remove = %d, want 0", idx.CollectionSize("c"))
	}
}

func TestCovAEmbeddingEnqueueWaitBlank(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_QUEUE_WAIT", "   ")
	if got := embeddingEnqueueWait(); got != 5*time.Second {
		t.Fatalf("blank wait = %v, want 5s", got)
	}
}

func TestCovADropJobCountsMetric(t *testing.T) {
	t.Setenv("MDDB_EMBEDDING_QUEUE_WAIT", "0")
	w := NewEmbeddingWorker(&mockEmbedding{dims: 4, model: "m"}, covAVectorStore(t, true), vec.NewVectorIndex(), 1)
	w.metrics = covAMetrics(t)
	if !w.Enqueue(EmbeddingJob{Collection: "c", DocID: "a"}) {
		t.Fatal("first job should fit")
	}
	if w.Enqueue(EmbeddingJob{Collection: "c", DocID: "b"}) {
		t.Fatal("second job should be dropped")
	}
	if _, _, dropped, _ := w.QueueStats(); dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if !strings.Contains(covAOpsText(t, w.metrics), `operation="embedding",label="dropped"`) {
		t.Fatal("dropped job was not counted in metrics")
	}
}

func TestCovAProcessJobChunkingDisabled(t *testing.T) {
	store := covAVectorStore(t, true)
	idx := vec.NewVectorIndex()
	w := NewEmbeddingWorker(&mockEmbedding{dims: 4, model: "m"}, store, idx, 1)
	w.chunkEnabled = false
	w.metrics = covAMetrics(t)
	w.processJob(EmbeddingJob{Collection: "c", DocID: "d", ContentMD: strings.Repeat("word ", 1000)})

	rec, err := store.Get("c", "d")
	if err != nil || rec == nil {
		t.Fatalf("expected a stored embedding, got %v, %v", rec, err)
	}
	if idx.CollectionSize("c") != 1 {
		t.Fatalf("unchunked document should be one vector, got %d", idx.CollectionSize("c"))
	}
	if !strings.Contains(covAOpsText(t, w.metrics), `operation="embedding",label="completed"`) {
		t.Fatal("completed job was not counted")
	}
}

func TestCovAProcessJobEmptyContent(t *testing.T) {
	store := covAVectorStore(t, true)
	idx := vec.NewVectorIndex()
	w := NewEmbeddingWorker(&mockEmbedding{dims: 4, model: "m"}, store, idx, 1)
	w.processJob(EmbeddingJob{Collection: "c", DocID: "d", ContentMD: ""})
	if rec, _ := store.Get("c", "d"); rec != nil {
		t.Fatal("empty content must not produce an embedding")
	}
	if idx.CollectionSize("c") != 0 {
		t.Fatal("empty content must not be indexed")
	}
}

func TestCovAProcessJobProviderFails(t *testing.T) {
	old := embeddingRetryDelay
	embeddingRetryDelay = time.Millisecond
	t.Cleanup(func() { embeddingRetryDelay = old })

	store := covAVectorStore(t, true)
	p := &covAFailingEmbedding{}
	w := NewEmbeddingWorker(p, store, vec.NewVectorIndex(), 1)
	w.metrics = covAMetrics(t)
	w.processJob(EmbeddingJob{Collection: "c", DocID: "d", ContentMD: "hello"})

	if got := p.calls.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if rec, _ := store.Get("c", "d"); rec != nil {
		t.Fatal("a failed embedding must store nothing")
	}
	if !strings.Contains(covAOpsText(t, w.metrics), `operation="embedding",label="error"`) {
		t.Fatal("failure was not counted")
	}
}

func TestCovAProcessJobDocumentDeleted(t *testing.T) {
	store := covAVectorStore(t, true)
	w := NewEmbeddingWorker(&mockEmbedding{dims: 4, model: "m"}, store, vec.NewVectorIndex(), 1)
	ix := &covAIndexer{exists: false}
	w.SetIndexer(ix)
	w.processJob(EmbeddingJob{Collection: "c", DocID: "gone", ContentMD: "hello"})
	if rec, _ := store.Get("c", "gone"); rec != nil {
		t.Fatal("vectors of a deleted document must be discarded")
	}
	if ix.indexed.Load() != 0 {
		t.Fatal("nothing should reach the index")
	}
}

func TestCovAProcessJobStoreFails(t *testing.T) {
	idx := vec.NewVectorIndex()
	w := NewEmbeddingWorker(&mockEmbedding{dims: 4, model: "m"}, covAVectorStore(t, false), idx, 1)
	w.processJob(EmbeddingJob{Collection: "c", DocID: "d", ContentMD: "hello"})
	if idx.CollectionSize("c") != 0 {
		t.Fatal("a chunk that could not be stored must not be indexed")
	}
}

func TestCovAProcessJobIndexRefused(t *testing.T) {
	store := covAVectorStore(t, true)
	w := NewEmbeddingWorker(&mockEmbedding{dims: 4, model: "m"}, store, vec.NewVectorIndex(), 1)
	w.metrics = covAMetrics(t)
	w.SetIndexer(&covAIndexer{refuse: true, exists: true})
	w.processJob(EmbeddingJob{Collection: "c", DocID: "d", ContentMD: "hello"})

	// Stored, but refused by the index: counted as both refused and completed.
	if rec, _ := store.Get("c", "d"); rec == nil {
		t.Fatal("the chunk is stored even when the index refuses it")
	}
	ops := covAOpsText(t, w.metrics)
	for _, want := range []string{`label="index_refused"`, `label="completed"`} {
		if !strings.Contains(ops, want) {
			t.Fatalf("metrics missing %s:\n%s", want, ops)
		}
	}
}
