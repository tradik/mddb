package fts

import (
	"os"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestBM25IndexAndSearch(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index several documents with different lengths
	if err := idx.Index("blog", "short1", "golang tutorial"); err != nil {
		t.Fatalf("Index short1: %v", err)
	}
	if err := idx.Index("blog", "short2", "golang programming"); err != nil {
		t.Fatalf("Index short2: %v", err)
	}
	if err := idx.Index("blog", "long1", "golang programming language tutorial advanced topics systems design patterns implementation guide reference"); err != nil {
		t.Fatalf("Index long1: %v", err)
	}
	if err := idx.Index("blog", "long2", "python programming language tutorial advanced topics machine learning data science artificial intelligence deep learning neural networks"); err != nil {
		t.Fatalf("Index long2: %v", err)
	}

	// Test single-term query
	results, err := idx.SearchBM25("blog", "golang", 10, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results for 'golang', got %d", len(results))
	}

	// Verify that shorter documents rank higher (BM25 length normalization)
	// short1 and short2 should rank higher than long1 for the same term frequency
	var shortScore, longScore float64
	for _, r := range results {
		if r.DocID == "short1" || r.DocID == "short2" {
			if shortScore == 0 || r.Score > shortScore {
				shortScore = r.Score
			}
		}
		if r.DocID == "long1" {
			longScore = r.Score
		}
	}

	if shortScore <= longScore {
		t.Errorf("expected shorter docs to score higher: short=%f, long=%f", shortScore, longScore)
	}
}

func TestBM25MultiTermQuery(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	if err := idx.Index("docs", "d1", "golang programming language"); err != nil {
		t.Fatalf("Index d1: %v", err)
	}
	if err := idx.Index("docs", "d2", "golang tutorial advanced"); err != nil {
		t.Fatalf("Index d2: %v", err)
	}
	if err := idx.Index("docs", "d3", "python programming tutorial"); err != nil {
		t.Fatalf("Index d3: %v", err)
	}

	// Multi-term query: "golang tutorial"
	results, err := idx.SearchBM25("docs", "golang tutorial", 10, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// d2 should rank highest (has both terms)
	if results[0].DocID != "d2" {
		t.Errorf("expected d2 to rank first (has both terms), got %s", results[0].DocID)
	}

	// Verify matched terms
	matched := make(map[string]bool)
	for _, term := range results[0].MatchedTerms {
		matched[term] = true
	}
	if !matched["golang"] || !matched["tutorial"] {
		t.Error("expected both 'golang' and 'tutorial' in matched terms")
	}
}

func TestBM25EmptyCollection(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	results, err := idx.SearchBM25("empty", "query", 10, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results for empty collection, got %d", len(results))
	}
}

func TestBM25EmptyQuery(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	if err := idx.Index("blog", "doc1", "golang programming"); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := idx.SearchBM25("blog", "", 10, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if results != nil {
		t.Error("expected nil results for empty query")
	}
}

func TestBM25StopWordsQuery(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	if err := idx.Index("blog", "doc1", "golang programming"); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Query with only stop words should return nil
	results, err := idx.SearchBM25("blog", "the and or", 10, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if results != nil {
		t.Error("expected nil results for stop words query")
	}
}

func TestBM25Limit(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index many documents with the same term
	for i := 0; i < 20; i++ {
		docID := string(rune('a' + i))
		if err := idx.Index("col", docID, "common term repeated"); err != nil {
			t.Fatalf("Index %s: %v", docID, err)
		}
	}

	// Request only 5 results
	results, err := idx.SearchBM25("col", "common", 5, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if len(results) != 5 {
		t.Errorf("expected 5 results with limit=5, got %d", len(results))
	}
}

func TestBM25SortedByScore(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Create docs with varying term frequencies and lengths
	if err := idx.Index("col", "d1", "golang"); err != nil {
		t.Fatalf("Index d1: %v", err)
	}
	if err := idx.Index("col", "d2", "golang golang"); err != nil {
		t.Fatalf("Index d2: %v", err)
	}
	if err := idx.Index("col", "d3", "golang golang golang"); err != nil {
		t.Fatalf("Index d3: %v", err)
	}

	results, err := idx.SearchBM25("col", "golang", 10, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify results are sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: [%d].Score=%f > [%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestBM25DocumentLengthNormalization(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Short doc with term appearing once
	if err := idx.Index("test", "short", "golang"); err != nil {
		t.Fatalf("Index short: %v", err)
	}

	// Long doc with term appearing once (surrounded by many other words)
	longContent := "golang " + "word word word word word word word word word word " +
		"word word word word word word word word word word"
	if err := idx.Index("test", "long", longContent); err != nil {
		t.Fatalf("Index long: %v", err)
	}

	results, err := idx.SearchBM25("test", "golang", 10, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Short doc should rank higher due to BM25 length normalization
	if results[0].DocID != "short" {
		t.Errorf("expected 'short' to rank first, got %s", results[0].DocID)
	}
}

func TestBM25MetaIndexing(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Test IndexBM25Meta directly
	err := idx.db.Update(func(tx *bolt.Tx) error {
		return idx.IndexBM25Meta(tx, "test", "doc1", 100)
	})
	if err != nil {
		t.Fatalf("IndexBM25Meta failed: %v", err)
	}

	// Verify metadata was stored
	err = idx.db.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(bucketFTSRev)
		if bRev == nil {
			t.Fatal("ftsrev bucket not found")
		}

		metaKey := ftsMetaKey("test", "doc1")
		val := bRev.Get(metaKey)
		if val == nil {
			t.Error("document metadata not stored")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBM25MetaUpdate(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index a document
	if err := idx.Index("test", "doc1", "golang programming"); err != nil {
		t.Fatalf("Index doc1: %v", err)
	}

	// Update with different content (different length)
	if err := idx.Index("test", "doc1", "python machine learning data science"); err != nil {
		t.Fatalf("Index doc1 update: %v", err)
	}

	// Search should reflect the updated metadata
	results, err := idx.SearchBM25("test", "python", 10, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1, got %s", results[0].DocID)
	}
}

func TestBM25MetaRemoval(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index a document
	if err := idx.Index("test", "doc1", "golang programming"); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Verify metadata exists
	var metaExists bool
	err := idx.db.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(bucketFTSRev)
		metaKey := ftsMetaKey("test", "doc1")
		metaExists = bRev.Get(metaKey) != nil
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !metaExists {
		t.Fatal("metadata should exist after indexing")
	}

	// Remove the document
	if err := idx.Remove("test", "doc1"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Verify metadata is removed
	err = idx.db.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(bucketFTSRev)
		metaKey := ftsMetaKey("test", "doc1")
		metaExists = bRev.Get(metaKey) != nil
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if metaExists {
		t.Error("metadata should be removed after document removal")
	}
}

func TestBM25DifferentCollections(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index same term in different collections
	if err := idx.Index("blog", "doc1", "golang programming"); err != nil {
		t.Fatalf("Index blog: %v", err)
	}
	if err := idx.Index("docs", "doc1", "golang tutorial"); err != nil {
		t.Fatalf("Index docs: %v", err)
	}

	// Search in blog collection only
	results, err := idx.SearchBM25("blog", "golang", 10, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result in blog, got %d", len(results))
	}

	// Search in docs collection only
	results, err = idx.SearchBM25("docs", "golang", 10, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result in docs, got %d", len(results))
	}
}

func TestBM25NoMatch(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	if err := idx.Index("blog", "doc1", "golang programming"); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Query for non-existent term
	results, err := idx.SearchBM25("blog", "javascript", 10, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25 failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results for non-matching query, got %d", len(results))
	}
}

func TestBM25CollectionStats(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index multiple documents
	if err := idx.Index("test", "d1", "golang programming language"); err != nil {
		t.Fatalf("Index d1: %v", err)
	}
	if err := idx.Index("test", "d2", "python scripting"); err != nil {
		t.Fatalf("Index d2: %v", err)
	}

	// Verify collection stats are stored
	err := idx.db.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(bucketFTSRev)
		if bRev == nil {
			t.Fatal("ftsrev bucket not found")
		}

		statKey := ftsStatKey("test")
		raw := bRev.Get(statKey)
		if raw == nil {
			t.Fatal("collection stats not found")
		}

		stats := decodeCollectionStats(raw)
		if stats.TotalDocs != 2 {
			t.Errorf("expected TotalDocs=2, got %d", stats.TotalDocs)
		}
		if stats.TotalTerms == 0 {
			t.Error("expected TotalTerms > 0")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBM25RemoveUpdatesStats(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index documents
	if err := idx.Index("test", "d1", "golang programming"); err != nil {
		t.Fatalf("Index d1: %v", err)
	}
	if err := idx.Index("test", "d2", "python scripting"); err != nil {
		t.Fatalf("Index d2: %v", err)
	}

	// Get initial stats
	var initialDocs uint32
	err := idx.db.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(bucketFTSRev)
		statKey := ftsStatKey("test")
		raw := bRev.Get(statKey)
		if raw != nil {
			stats := decodeCollectionStats(raw)
			initialDocs = stats.TotalDocs
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if initialDocs != 2 {
		t.Fatalf("expected 2 docs initially, got %d", initialDocs)
	}

	// Remove one document
	if err := idx.Remove("test", "d1"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Verify stats updated
	err = idx.db.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(bucketFTSRev)
		statKey := ftsStatKey("test")
		raw := bRev.Get(statKey)
		if raw != nil {
			stats := decodeCollectionStats(raw)
			if stats.TotalDocs != 1 {
				t.Errorf("expected TotalDocs=1 after removal, got %d", stats.TotalDocs)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBM25Helper(t *testing.T) {
	// Create a temp database for direct testing
	f, err := os.CreateTemp("", "bm25_helper_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	defer func() { _ = os.Remove(f.Name()) }()

	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	idx := NewFTSIndex(db)
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}

	// Test IndexBM25Meta and RemoveBM25Meta
	err = db.Update(func(tx *bolt.Tx) error {
		// Index metadata for a document
		if err := idx.IndexBM25Meta(tx, "test", "doc1", 50); err != nil {
			return err
		}

		// Update the same document
		if err := idx.IndexBM25Meta(tx, "test", "doc1", 75); err != nil {
			return err
		}

		// Remove metadata
		return idx.RemoveBM25Meta(tx, "test", "doc1")
	})
	if err != nil {
		t.Fatalf("BM25 metadata operations failed: %v", err)
	}

	// Verify metadata was removed
	err = db.View(func(tx *bolt.Tx) error {
		bRev := tx.Bucket(bucketFTSRev)
		metaKey := ftsMetaKey("test", "doc1")
		if bRev.Get(metaKey) != nil {
			t.Error("metadata should be removed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
