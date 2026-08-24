package main

import (
	"context"
	"fmt"
	"mddb/internal/testsync"
	"mddb/internal/vector"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// embWorkerMockProvider is a mock embedding provider for worker tests.
type embWorkerMockProvider struct {
	mu        sync.Mutex
	callCount int
	failUntil int // fail the first N calls
	model     string
	dims      int
	vector    []float32
}

func (m *embWorkerMockProvider) Embed(_ context.Context, _ string) ([]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	if m.callCount <= m.failUntil {
		return nil, fmt.Errorf("mock embed error (call %d)", m.callCount)
	}
	vec := make([]float32, len(m.vector))
	copy(vec, m.vector)
	return vec, nil
}

func (m *embWorkerMockProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i := range texts {
		v, err := m.Embed(context.Background(), texts[i])
		if err != nil {
			return nil, err
		}
		results[i] = v
	}
	return results, nil
}

func (m *embWorkerMockProvider) Model() string   { return m.model }
func (m *embWorkerMockProvider) Dimensions() int { return m.dims }

func (m *embWorkerMockProvider) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// embWorkerSetup creates a VectorStore, VectorIndex, and opens a temp DB.
func embWorkerSetup(t *testing.T) (*bolt.DB, *vector.VectorStore, *vector.VectorIndex) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "worker_test.db")
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatalf("bolt.Open: %v", err)
	}

	vs := vector.NewVectorStore(db)
	if err := vs.EnsureBucket(); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}

	vi := vector.NewVectorIndex()
	vi.SetReady()

	t.Cleanup(func() { _ = db.Close() })
	return db, vs, vi
}

func TestEmbeddingWorker_NewAndStartStop(t *testing.T) {
	_, vs, vi := embWorkerSetup(t)
	provider := &embWorkerMockProvider{
		model:  "test-model",
		dims:   3,
		vector: []float32{1.0, 2.0, 3.0},
	}

	w := NewEmbeddingWorker(provider, vs, vi, 10)
	if w == nil {
		t.Fatal("NewEmbeddingWorker returned nil")
	}

	w.Start(2)
	w.Stop()
}

func TestEmbeddingWorker_Enqueue(t *testing.T) {
	_, vs, vi := embWorkerSetup(t)
	provider := &embWorkerMockProvider{
		model:  "test-model",
		dims:   3,
		vector: []float32{0.1, 0.2, 0.3},
	}

	w := NewEmbeddingWorker(provider, vs, vi, 10)
	w.Start(1)
	defer w.Stop()

	ok := w.Enqueue(EmbeddingJob{
		Collection: "test-col",
		DocID:      "doc-1",
		ContentMD:  "Hello world",
	})
	if !ok {
		t.Error("Enqueue returned false, expected true")
	}

	// The record, not the error: Get returns (nil, nil) for a document that
	// has not been embedded yet, so waiting on err == nil would return before
	// the worker had done anything.
	testsync.Wait(t, "the embedding to be stored", func() bool {
		rec, err := vs.Get("test-col", "doc-1")
		return err == nil && rec != nil
	})

	// Verify the embedding was stored
	rec, err := vs.Get("test-col", "doc-1")
	if err != nil {
		t.Fatalf("VectorStore.Get: %v", err)
	}
	if rec == nil {
		t.Fatal("expected embedding record, got nil")
		return
	}
	if rec.Model != "test-model" {
		t.Errorf("Model = %q, want %q", rec.Model, "test-model")
	}
	if len(rec.Vector) != 3 {
		t.Errorf("Vector length = %d, want 3", len(rec.Vector))
	}

	// Verify in-memory index was updated
	size := vi.CollectionSize("test-col")
	if size != 1 {
		t.Errorf("VectorIndex size = %d, want 1", size)
	}
}

func TestEmbeddingWorker_EnqueueFull(t *testing.T) {
	_, vs, vi := embWorkerSetup(t)
	provider := &embWorkerMockProvider{
		model:  "test-model",
		dims:   3,
		vector: []float32{0.1, 0.2, 0.3},
	}

	// Buffer size of 1 and no workers started to fill the queue
	w := NewEmbeddingWorker(provider, vs, vi, 1)
	// Don't start workers, so the queue stays full

	// First enqueue should succeed (fills the buffer)
	ok1 := w.Enqueue(EmbeddingJob{Collection: "c", DocID: "d1", ContentMD: "text1"})
	if !ok1 {
		t.Error("first Enqueue should succeed")
	}

	// Second enqueue should fail (buffer full, no workers draining)
	ok2 := w.Enqueue(EmbeddingJob{Collection: "c", DocID: "d2", ContentMD: "text2"})
	if ok2 {
		t.Error("second Enqueue should fail when queue is full")
	}
}

func TestEmbeddingWorker_SkipUpToDateEmbedding(t *testing.T) {
	_, vs, vi := embWorkerSetup(t)
	provider := &embWorkerMockProvider{
		model:  "test-model",
		dims:   3,
		vector: []float32{0.1, 0.2, 0.3},
	}

	content := "Hello world"
	contentHash := vector.ContentHash(content)

	// Pre-store an embedding with the same content hash
	if err := vs.Put("col", "doc-1", []float32{0.1, 0.2, 0.3}, "test-model", contentHash); err != nil {
		t.Fatalf("VectorStore.Put: %v", err)
	}

	w := NewEmbeddingWorker(provider, vs, vi, 10)
	w.Start(1)
	defer w.Stop()

	w.Enqueue(EmbeddingJob{
		Collection: "col",
		DocID:      "doc-1",
		ContentMD:  content,
	})

	// Deliberate: this asserts the provider was NOT called, and absence
	// cannot be polled for — only "not yet". The wait gives the worker room to
	// have made the call it should not make. Kept short because the queue is
	// otherwise idle here (TEST-004 triage: time passage, not synchronisation).
	time.Sleep(200 * time.Millisecond)

	// Provider should not have been called since embedding is up to date
	if provider.getCallCount() != 0 {
		t.Errorf("expected 0 embed calls (up-to-date), got %d", provider.getCallCount())
	}
}

func TestEmbeddingWorker_ReembedOnContentChange(t *testing.T) {
	_, vs, vi := embWorkerSetup(t)
	provider := &embWorkerMockProvider{
		model:  "test-model",
		dims:   3,
		vector: []float32{0.4, 0.5, 0.6},
	}

	// Pre-store an embedding with a different content hash
	if err := vs.Put("col", "doc-1", []float32{0.1, 0.2, 0.3}, "test-model", "old-hash"); err != nil {
		t.Fatalf("VectorStore.Put: %v", err)
	}

	w := NewEmbeddingWorker(provider, vs, vi, 10)
	w.Start(1)
	defer w.Stop()

	w.Enqueue(EmbeddingJob{
		Collection: "col",
		DocID:      "doc-1",
		ContentMD:  "New content",
	})

	testsync.WaitForCount(t, "the provider to be called for the changed content", 1,
		provider.getCallCount)

	// Provider should have been called since content changed
	if provider.getCallCount() == 0 {
		t.Error("expected embed call for changed content")
	}

	// Verify new embedding was stored
	rec, _ := vs.Get("col", "doc-1")
	if rec == nil {
		t.Fatal("expected updated embedding")
		return
	}
	if rec.Vector[0] != 0.4 {
		t.Errorf("Vector[0] = %f, want 0.4", rec.Vector[0])
	}
}

func TestEmbeddingWorker_MultipleJobs(t *testing.T) {
	_, vs, vi := embWorkerSetup(t)
	provider := &embWorkerMockProvider{
		model:  "test-model",
		dims:   2,
		vector: []float32{1.0, 0.0},
	}

	w := NewEmbeddingWorker(provider, vs, vi, 100)
	w.Start(2)
	defer w.Stop()

	for i := 0; i < 10; i++ {
		w.Enqueue(EmbeddingJob{
			Collection: "col",
			DocID:      fmt.Sprintf("doc-%d", i),
			ContentMD:  fmt.Sprintf("Content %d", i),
		})
	}

	testsync.WaitForCount(t, "all ten documents to be embedded", 10,
		func() int { return vi.CollectionSize("col") })

	// All 10 should have been processed
	size := vi.CollectionSize("col")
	if size != 10 {
		t.Errorf("VectorIndex size = %d, want 10", size)
	}
}

func TestEmbeddingWorker_StopDrainsQueue(t *testing.T) {
	_, vs, vi := embWorkerSetup(t)
	provider := &embWorkerMockProvider{
		model:  "test-model",
		dims:   2,
		vector: []float32{1.0, 0.0},
	}

	w := NewEmbeddingWorker(provider, vs, vi, 100)
	// Enqueue before starting workers
	for i := 0; i < 5; i++ {
		w.Enqueue(EmbeddingJob{
			Collection: "drain",
			DocID:      fmt.Sprintf("doc-%d", i),
			ContentMD:  fmt.Sprintf("Content %d", i),
		})
	}

	w.Start(1)
	// Stop should drain remaining
	w.Stop()

	// All should have been processed
	size := vi.CollectionSize("drain")
	if size != 5 {
		t.Errorf("VectorIndex size = %d, want 5 (drain on stop)", size)
	}
}
