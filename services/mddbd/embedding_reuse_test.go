package main

import (
	"context"
	"mddb/internal/embedding"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	vec "mddb/internal/vector"
)

// RAG-003. The provider is the expensive part — a network round trip and a
// token charge per call — so these count calls, not results.

type callCountingProvider struct {
	calls atomic.Int64
	texts atomic.Int64
}

func (p *callCountingProvider) Embed(_ context.Context, text string, _ embedding.Role) ([]float32, error) {
	p.calls.Add(1)
	p.texts.Add(1)
	var sum float32
	for _, r := range text {
		sum += float32(r)
	}
	return []float32{sum, sum + 1, sum + 2}, nil
}

func (p *callCountingProvider) EmbedBatch(ctx context.Context, texts []string, _ embedding.Role) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		v, err := p.Embed(ctx, t, embedding.RoleDocument)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (p *callCountingProvider) Model() string   { return "test-model" }
func (p *callCountingProvider) Dimensions() int { return 3 }

func reuseWorker(t *testing.T) (*EmbeddingWorker, *callCountingProvider, func()) {
	t.Helper()

	f, err := os.CreateTemp("", "embed_reuse_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	db, err := bolt.Open(f.Name(), 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	store := vec.NewVectorStore(db)
	if err := store.EnsureBucket(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}

	provider := &callCountingProvider{}
	w := NewEmbeddingWorker(provider, store, vec.NewVectorIndex(), 4)
	// Small chunks so a multi-chunk document is easy to build.
	w.chunkSize = 40
	w.chunkEnabled = true

	return w, provider, func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
}

// paragraphs builds a document whose chunks are stable and easy to edit.
func paragraphs(bodies ...string) string {
	return strings.Join(bodies, "\n\n")
}

// The document-level check: nothing changed, nothing embedded.
func TestReindexOfAnUnchangedDocumentEmbedsNothing(t *testing.T) {
	w, provider, cleanup := reuseWorker(t)
	defer cleanup()

	content := paragraphs("first paragraph here", "second paragraph here")
	job := EmbeddingJob{Collection: "docs", DocID: "doc1", ContentMD: content}

	w.processJob(job)
	first := provider.calls.Load()
	if first == 0 {
		t.Fatal("the first pass embedded nothing")
	}

	w.processJob(job)
	if got := provider.calls.Load(); got != first {
		t.Errorf("reindexing unchanged content called the provider %d extra times", got-first)
	}
}

// The chunk-level check, which is what RAG-003 adds: editing one paragraph used
// to re-embed the whole document, because the document hash changed.
func TestEditingOneParagraphOnlyEmbedsThatChunk(t *testing.T) {
	w, provider, cleanup := reuseWorker(t)
	defer cleanup()

	before := paragraphs(
		"alpha paragraph about the header",
		"beta paragraph about the footer",
		"gamma paragraph about the sidebar",
		"delta paragraph about the navigation",
	)
	w.processJob(EmbeddingJob{Collection: "docs", DocID: "doc1", ContentMD: before})
	firstPass := provider.texts.Load()
	if firstPass < 4 {
		t.Fatalf("expected at least 4 chunks embedded, got %d", firstPass)
	}

	provider.texts.Store(0)

	after := paragraphs(
		"alpha paragraph about the header",
		"beta paragraph about the footer",
		"gamma paragraph REWRITTEN entirely",
		"delta paragraph about the navigation",
	)
	w.processJob(EmbeddingJob{Collection: "docs", DocID: "doc1", ContentMD: after})

	secondPass := provider.texts.Load()
	if secondPass == 0 {
		t.Fatal("the changed paragraph was not re-embedded")
	}
	if secondPass >= firstPass {
		t.Errorf("editing one paragraph embedded %d chunks, the same as a full pass (%d)",
			secondPass, firstPass)
	}
}

// A paragraph inserted at the top shifts every chunk index below it. Reuse is
// keyed by hash precisely so the unchanged text is still recognised.
func TestInsertingAtTheTopStillReusesTheTail(t *testing.T) {
	w, provider, cleanup := reuseWorker(t)
	defer cleanup()

	tail := paragraphs(
		"beta paragraph about the footer",
		"gamma paragraph about the sidebar",
		"delta paragraph about the navigation",
	)
	w.processJob(EmbeddingJob{Collection: "docs", DocID: "doc1", ContentMD: tail})
	fullPass := provider.texts.Load()

	provider.texts.Store(0)
	w.processJob(EmbeddingJob{
		Collection: "docs", DocID: "doc1",
		ContentMD: paragraphs("brand new opening paragraph", tail),
	})

	secondPass := provider.texts.Load()
	if secondPass >= fullPass {
		t.Errorf("inserting one paragraph re-embedded %d chunks of %d — the shifted tail was not reused",
			secondPass, fullPass)
	}
}

// Reuse must never hand back a vector for different text.
func TestReusedVectorsMatchTheirChunks(t *testing.T) {
	w, provider, cleanup := reuseWorker(t)
	defer cleanup()

	content := paragraphs("alpha text here", "beta text here", "gamma text here")
	w.processJob(EmbeddingJob{Collection: "docs", DocID: "doc1", ContentMD: content})

	byHash := w.vectorStore.ChunkVectorsByHash("docs", "doc1")
	chunks := chunkTextsMode(content, w.chunkSize, ChunkModeProse)
	for _, chunk := range chunks {
		stored, ok := byHash[vec.ContentHash(chunk)]
		if !ok {
			t.Errorf("chunk %q has no stored vector", chunk)
			continue
		}
		want, err := provider.Embed(context.Background(), chunk, embedding.RoleDocument)
		if err != nil {
			t.Fatal(err)
		}
		if stored[0] != want[0] {
			t.Errorf("chunk %q stored the wrong vector: %v vs %v", chunk, stored, want)
		}
	}
}
