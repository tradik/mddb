package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mddb/internal/embedding"
	"mddb/internal/metrics"
	"mddb/internal/storage"
	vec "mddb/internal/vector"

	bolt "go.etcd.io/bbolt"
)

// RAG-009: a summary's vector follows its document — refreshed when the
// summary changes, not when it does not, removed with the summary, the
// document or the collection — and spends the embedding budget.

// summaryFixedEmbedding returns vector v, or err.
type summaryFixedEmbedding struct {
	v     []float32
	err   error
	calls *atomic.Int64
}

func (e summaryFixedEmbedding) Embed(context.Context, string, embedding.Role) ([]float32, error) {
	if e.calls != nil {
		e.calls.Add(1)
	}
	return e.v, e.err
}
func (e summaryFixedEmbedding) EmbedBatch(ctx context.Context, texts []string, r embedding.Role) ([][]float32, error) {
	return nil, e.err
}
func (summaryFixedEmbedding) Model() string   { return "fixed" }
func (summaryFixedEmbedding) Dimensions() int { return 3 }

func TestSummaryLifecycle_UpdateDeleteCollection(t *testing.T) {
	s, calls, cleanup := summaryServer(t, &RetrievalProfileDef{SummaryMode: SummaryModeBlend})
	defer cleanup()
	plantID := ""
	_ = s.DBViewC("sum", func(tx *bolt.Tx) error {
		plantID = string(tx.Bucket([]byte("bykey")).Get(storage.ByKeyKey("sum", "plant", "en")))
		return nil
	})
	if plantID == "" || s.VectorStore.GetSummary("sum", plantID) == nil {
		t.Fatalf("plant's summary was not stored (%q)", plantID)
	}
	settle := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for s.IndexingStatus("sum").PendingEmbeddings > 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Metadata changed, summary did not: nothing is embedded.
	before := calls.Load()
	if _, err := s.updateDocument("sum", "plant", "en", DocPatch{HasMeta: true,
		Meta: map[string][]string{"summary": {plantSummary}, "tag": {"x"}}}); err != nil {
		t.Fatal(err)
	}
	settle()
	if calls.Load() != before {
		t.Fatalf("an unchanged summary was embedded again: %d calls", calls.Load()-before)
	}

	// A new summary: one call, a new vector, and the old words stop matching.
	old := s.VectorStore.GetSummary("sum", plantID)
	if _, err := s.updateDocument("sum", "plant", "en", DocPatch{HasMeta: true,
		Meta: map[string][]string{"summary": {"Weekly repair log for the boiler room."}}}); err != nil {
		t.Fatal(err)
	}
	settle()
	if calls.Load() != before+1 {
		t.Fatalf("a changed summary cost %d calls, want 1", calls.Load()-before)
	}
	if now := s.VectorStore.GetSummary("sum", plantID); now == nil || now.ContentHash == old.ContentHash {
		t.Fatal("the summary vector was not refreshed")
	}
	if keys := sumResultKeys(restVector(t, s, map[string]any{"query": generalQuery, "threshold": 0.3})); len(keys) != 0 {
		t.Fatalf("the old summary still matches: %v", keys)
	}
	rec := doRequest(t, s.handleFTS, map[string]any{"collection": "sum", "query": "boiler", "algorithm": "bm25f"})
	if keys := sumResultKeys(body(t, rec.Body.Bytes())); len(keys) != 1 {
		t.Fatalf("the new summary is not in the full-text index: %s", rec.Body.String())
	}

	// Summary removed: vector and index entry go.
	if _, err := s.updateDocument("sum", "plant", "en", DocPatch{HasMeta: true, Meta: map[string][]string{}}); err != nil {
		t.Fatal(err)
	}
	settle()
	if s.VectorStore.GetSummary("sum", plantID) != nil || s.summaries().has("sum", plantID) {
		t.Fatal("a removed summary kept its vector")
	}

	// Document deleted.
	gardenID := ""
	for id := range s.VectorStore.LoadSummaries("sum") {
		gardenID = id
	}
	if err := s.deleteDocumentInternal("sum", "garden", "en"); err != nil {
		t.Fatal(err)
	}
	if s.VectorStore.GetSummary("sum", gardenID) != nil || s.summaries().size("sum") != 0 {
		t.Fatal("a deleted document kept its summary vector")
	}

	// Collection deleted, with a summary in it.
	addTestDoc(t, s, "sum", "again", "en", "Some text here.", map[string][]string{"summary": {"A summary."}})
	waitSummaries(t, s, "sum", 1)
	if _, err := s.deleteCollection("sum"); err != nil {
		t.Fatal(err)
	}
	if len(s.VectorStore.LoadSummaries("sum")) != 0 || s.summaries().size("sum") != 0 {
		t.Fatal("a deleted collection kept its summary vectors")
	}
}

func TestSummaryRefresh_Branches(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	old := embeddingRetryDelay
	embeddingRetryDelay = 0
	defer func() { embeddingRetryDelay = old }()
	ix := serverIndexes{s}
	calls := &atomic.Int64{}
	r := summaryRefresh{provider: summaryFixedEmbedding{v: []float32{1, 0, 0}, calls: calls}, store: s.VectorStore, index: ix, attempts: 2}
	ctx := context.Background()

	if out, err := r.run(ctx, "c", "d", "", false); out != jobDone || err != nil || calls.Load() != 0 {
		t.Fatalf("no summary: %v %v", out, err)
	}
	if out, err := r.run(ctx, "c", "d", "one", false); out != jobDone || err != nil || !ix.HasSummary("c", "d") {
		t.Fatalf("new summary: %v %v", out, err)
	}
	if _, _ = r.run(ctx, "c", "d", "one", false); calls.Load() != 1 {
		t.Fatalf("unchanged summary re-embedded: %d", calls.Load())
	}
	ix.RemoveSummary("c", "d") // stored, not indexed: indexed again without a call
	if _, _ = r.run(ctx, "c", "d", "one", false); calls.Load() != 1 || !ix.HasSummary("c", "d") {
		t.Fatal("a stored summary was not indexed from the store")
	}
	if _, _ = r.run(ctx, "c", "d", "one", true); calls.Load() != 2 {
		t.Fatal("force did not re-embed")
	}
	if out, _ := r.run(ctx, "c", "d", "", false); out != jobDone || s.VectorStore.GetSummary("c", "d") != nil {
		t.Fatal("a removed summary kept its vector")
	}

	gone := r
	gone.exists = func() bool { return false }
	if out, _ := gone.run(ctx, "c", "e", "two", false); out != jobDone || s.VectorStore.GetSummary("c", "e") != nil {
		t.Fatal("a deleted document got a summary vector")
	}

	failing := r
	failing.provider = summaryFixedEmbedding{err: errors.New("down"), calls: calls}
	before := calls.Load()
	if out, err := failing.run(ctx, "c", "f", "three", false); out != jobFailed || err == nil || calls.Load()-before != 2 {
		t.Fatalf("provider failure: %v %v after %d calls", out, err, calls.Load()-before)
	}

	empty := r
	empty.provider = summaryFixedEmbedding{v: []float32{}}
	if out, err := empty.run(ctx, "c", "g", "four", false); out != jobFailed || err == nil || !strings.Contains(err.Error(), "index") {
		t.Fatalf("index refusal: %v %v", out, err)
	}

	noBucket := r
	noBucket.store = vec.NewVectorStore(bareDB(t))
	if out, err := noBucket.run(ctx, "c", "h", "five", false); out != jobFailed || err == nil || !strings.Contains(err.Error(), "store") {
		t.Fatalf("store failure: %v %v", out, err)
	}

	_ = s.CollectionManager.Set("q", &CollectionConfig{Quota: &CollectionQuota{EmbeddingCallsPerDay: 1}})
	budgeted := r
	budgeted.budget = s.quotas().budget
	if out, _ := budgeted.run(ctx, "q", "a", "six", false); out != jobDone {
		t.Fatal("the first call was refused")
	}
	if out, _ := budgeted.run(ctx, "q", "b", "seven", false); out != jobParked {
		t.Fatalf("over budget: %v, want parked", out)
	}
}

// bareDB is a database without the vectors bucket.
func bareDB(t *testing.T) *bolt.DB {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "bare*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSummaryWorker_ParksAndSkips(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	w := NewEmbeddingWorker(summaryFixedEmbedding{v: []float32{1, 0, 0}}, s.VectorStore, s.VectorIndex, 4)
	// A worker without the server's indexes has nowhere to put a summary.
	if out := w.processSummary(EmbeddingJob{Collection: "c", DocID: "d", Summary: "x"}); out != jobDone {
		t.Fatalf("flat-only worker: %v", out)
	}
	w.SetIndexer(serverIndexes{s})
	w.metrics = metrics.NewMetrics(false, &serverMetricsStats{s: s})
	w.provider = summaryFixedEmbedding{err: errors.New("down")}
	old := embeddingRetryDelay
	embeddingRetryDelay = 0
	defer func() { embeddingRetryDelay = old }()
	if out := w.processSummary(EmbeddingJob{Collection: "c", DocID: "d", Summary: "x"}); out != jobFailed {
		t.Fatalf("provider down: %v", out)
	}

	// Over budget after the chunks: the job parks, still pending.
	_ = s.CollectionManager.Set("q", &CollectionConfig{Quota: &CollectionQuota{EmbeddingCallsPerDay: 1}})
	w.provider = summaryFixedEmbedding{v: []float32{1, 0, 0}}
	w.budget = s.quotas().budget
	doc := addTestDoc(t, s, "q", "k", "en", "short text", map[string][]string{"summary": {"its summary"}})
	if out := w.processJob(EmbeddingJob{Collection: "q", DocID: doc.ID, ContentMD: doc.ContentMD, Summary: "its summary"}); out != jobParked {
		t.Fatalf("summary over budget: %v, want parked", out)
	}
	if s.summaries().has("q", doc.ID) {
		t.Fatal("a parked summary was indexed")
	}
}

func TestSummaryReindex(t *testing.T) {
	s, calls, cleanup := summaryServer(t, nil)
	defer cleanup()
	before := calls.Load()
	res, err := s.reindexVectors(context.Background(), "sum", true, nil)
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	// Force re-embeds the chunks and both summaries.
	if got := calls.Load() - before; got != int64(res.TotalChunks)+2 {
		t.Fatalf("%d calls for %d chunks and 2 summaries", got, res.TotalChunks)
	}

	var errs vectorReindexResult
	s.Embedding = summaryFixedEmbedding{err: errors.New("down")}
	s.reindexSummary(context.Background(), "sum", "x", "a summary", true, &errs)
	_ = s.CollectionManager.Set("q", &CollectionConfig{Quota: &CollectionQuota{EmbeddingCallsPerDay: 1}})
	s.quotas().budget.spend("q", "x")
	s.reindexSummary(context.Background(), "q", "y", "a summary", true, &errs)
	if len(errs.Errors) != 2 || !strings.Contains(errs.Errors[0], "down") || !strings.Contains(errs.Errors[1], "budget") {
		t.Fatalf("%v", errs.Errors)
	}
}
