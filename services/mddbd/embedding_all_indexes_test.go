package main

import (
	"strconv"
	"testing"
	"time"

	"mddb/internal/vector"
)

// A document embedded after startup has to be findable by every search
// algorithm, not by flat alone.
//
// Measured on the released 2.15.1 image: three documents ingested with no
// reindex, then searched with each algorithm. flat found all three; hnsw, ivf,
// sq and bq found none — each answering from the index it was asked for, with
// no error and no fallback. After a restart all five found all three, because
// startup reloads every index from the store; a document added after that
// restart was again visible to flat alone. The worker held the flat index and
// nothing else.

// waitForEmbedding polls until the worker has put a document's first chunk
// into the flat index, which it reaches last in no particular order but always
// reaches. Embedding is asynchronous; the test must not race it.
func waitForEmbedding(t *testing.T, s *Server, collection, docID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.VectorIndex.GetVector(collection, docID+"#0") != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s was never embedded", docID)
}

func TestADocumentEmbeddedAfterStartupReachesEveryIndex(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.startEmbeddingWorker(s.Embedding)
	defer s.EmbeddingWorker.Stop()

	doc := addTestDoc(t, s, "blog", "fresh", "en", "# Written after startup", nil)
	waitForEmbedding(t, s, "blog", doc.ID)

	// Searched, not counted: in 2.15.2 every index held the vector, and IVF,
	// PQ, OPQ, SQ and SQ4 still found nothing, because a collection created
	// after startup had never been trained and an untrained index answers
	// every query with nothing. Counting the vectors would have passed.
	for name, searcher := range s.VectorSearchers {
		if name == "quantized" {
			continue // holds only collections configured for quantization
		}
		t.Run(name, func(t *testing.T) {
			if hits := searcher.Search("blog", []float32{0.1, 0.1, 0.1}, 5, -1, nil); len(hits) == 0 {
				t.Errorf("a %s search found nothing for a document embedded after startup", name)
			}
		})
	}
}

// A document that shrinks leaves chunks it no longer has. They were removed
// from the flat index only, and the other algorithms went on returning
// passages of text that no longer existed.
func TestStaleChunksLeaveEveryIndex(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ix := serverIndexes{s}

	chunks := []vector.ChunkEmbedding{
		{ChunkIndex: 0, Vector: []float32{1, 0, 0}},
		{ChunkIndex: 1, Vector: []float32{0, 1, 0}},
	}
	if err := s.VectorStore.PutChunks("blog", "doc", chunks, "test-model", "h"); err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if err := ix.IndexChunk("blog", "doc#"+strconv.Itoa(c.ChunkIndex), c.Vector); err != nil {
			t.Fatal(err)
		}
	}

	// The document now has one chunk.
	s.VectorStore.CleanStaleChunks("blog", "doc", 1, ix)

	for name, searcher := range s.VectorSearchers {
		if name == "quantized" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			for _, hit := range searcher.Search("blog", []float32{0, 1, 0}, 5, 0, nil) {
				if hit.DocID == "doc#1" {
					t.Errorf("%s still returns a chunk the document no longer has", name)
				}
			}
		})
	}
}
