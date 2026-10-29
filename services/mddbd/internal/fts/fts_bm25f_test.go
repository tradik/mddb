package fts

import (
	"testing"
)

func TestBM25FBasicSearch(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Doc1: term "golang" in title only
	if err := indexWithFields(idx, "blog", "doc1", map[string]string{
		"meta.title": "golang tutorial",
		"content":    "this is a programming language tutorial for beginners",
	}); err != nil {
		t.Fatalf("IndexFields doc1: %v", err)
	}
	// Doc2: term "golang" in content only
	if err := indexWithFields(idx, "blog", "doc2", map[string]string{
		"meta.title": "programming tutorial",
		"content":    "golang is great for building systems",
	}); err != nil {
		t.Fatalf("IndexFields doc2: %v", err)
	}

	tokens := idx.Tokenize("golang")
	results, err := idx.SearchBM25F("blog", tokens, 10, nil, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25F: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// doc1 should rank higher — title match with default weight 3.0 > content weight 1.0
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1 (title match) to rank first, got %s (scores: doc1=%f, doc2=%f)",
			results[0].DocID, results[0].Score, results[1].Score)
	}
}

func TestBM25FDefaultWeights(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index document with content and multiple meta fields
	if err := indexWithFields(idx, "col", "d1", map[string]string{
		"content":          "advanced database systems",
		"meta.title":       "database performance guide",
		"meta.tags":        "database optimization",
		"meta.description": "comprehensive database tuning tips",
	}); err != nil {
		t.Fatalf("IndexFields: %v", err)
	}

	tokens := idx.Tokenize("database")
	results, err := idx.SearchBM25F("col", tokens, 10, nil, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25F: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Verify that score is positive and reflects multiple field matches
	if results[0].Score <= 0 {
		t.Error("expected positive score")
	}
}

func TestBM25FCustomWeights(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Doc1: term in tags
	if err := indexWithFields(idx, "col", "d1", map[string]string{
		"meta.tags": "golang performance",
		"content":   "some other content here about nothing specific",
	}); err != nil {
		t.Fatalf("IndexFields d1: %v", err)
	}
	// Doc2: term in content
	if err := indexWithFields(idx, "col", "d2", map[string]string{
		"meta.tags": "python scripting",
		"content":   "golang is great for performance",
	}); err != nil {
		t.Fatalf("IndexFields d2: %v", err)
	}

	tokens := idx.Tokenize("golang")

	// With custom weights: tags=10.0, content=1.0 → doc1 should win
	customWeights := map[string]float64{
		"meta.tags": 10.0,
		"content":   1.0,
	}
	results, err := idx.SearchBM25F("col", tokens, 10, customWeights, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25F custom: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].DocID != "d1" {
		t.Errorf("expected d1 (tags match) to rank first with custom weights, got %s", results[0].DocID)
	}

	// Now flip weights: content=10.0, tags=1.0 → doc2 should win
	flippedWeights := map[string]float64{
		"meta.tags": 1.0,
		"content":   10.0,
	}
	results2, err := idx.SearchBM25F("col", tokens, 10, flippedWeights, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25F flipped: %v", err)
	}
	if len(results2) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results2))
	}
	if results2[0].DocID != "d2" {
		t.Errorf("expected d2 (content match) to rank first with flipped weights, got %s", results2[0].DocID)
	}
}

func TestBM25FContentWithoutFieldData(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index using only the regular (non-field) Index method
	if err := idx.Index("blog", "doc1", "golang programming"); err != nil {
		t.Fatalf("Index: %v", err)
	}

	tokens := idx.Tokenize("golang")
	results, err := idx.SearchBM25F("blog", tokens, 10, nil, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25F: %v", err)
	}
	// No per-field data, but the body is in the main index, which serves
	// the "content" field (#317): the document is found through it.
	if len(results) != 1 || results[0].DocID != "doc1" {
		t.Errorf("expected doc1 through the content field, got %v", results)
	}
}

func TestBM25FMultipleTerms(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	if err := indexWithFields(idx, "col", "d1", map[string]string{
		"meta.title": "golang database tutorial",
		"content":    "learn how to build databases with golang",
	}); err != nil {
		t.Fatalf("IndexFields d1: %v", err)
	}
	if err := indexWithFields(idx, "col", "d2", map[string]string{
		"meta.title": "python web scraping",
		"content":    "golang is used for database management",
	}); err != nil {
		t.Fatalf("IndexFields d2: %v", err)
	}
	if err := indexWithFields(idx, "col", "d3", map[string]string{
		"meta.title": "rust systems programming",
		"content":    "rust and golang are systems languages",
	}); err != nil {
		t.Fatalf("IndexFields d3: %v", err)
	}

	tokens := idx.Tokenize("golang database")
	results, err := idx.SearchBM25F("col", tokens, 10, nil, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25F: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// d1 should rank first — has both terms in title and content
	if results[0].DocID != "d1" {
		t.Errorf("expected d1 to rank first (both terms match), got %s", results[0].DocID)
	}
}

func TestBM25FFuzzy(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	if err := indexWithFields(idx, "col", "d1", map[string]string{
		"meta.title": "golang tutorial",
		"content":    "learn golang programming basics",
	}); err != nil {
		t.Fatalf("IndexFields d1: %v", err)
	}

	// Typo: "golanq" instead of "golang"
	tokens := idx.Tokenize("golanq")
	results, err := idx.SearchBM25FFuzzy("col", tokens, 10, 1, nil, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25FFuzzy: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 fuzzy result, got %d", len(results))
	}
	if results[0].DocID != "d1" {
		t.Errorf("expected d1 in fuzzy result, got %s", results[0].DocID)
	}
}

func TestBM25FEmptyCollection(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	tokens := idx.Tokenize("anything")
	results, err := idx.SearchBM25F("nonexistent", tokens, 10, nil, DefaultBM25)
	if err != nil {
		t.Fatalf("SearchBM25F: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty collection, got %d", len(results))
	}
}

func TestBM25FFieldWeightZero(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index with term in both title and content
	if err := indexWithFields(idx, "col", "d1", map[string]string{
		"meta.title": "golang tutorial",
		"content":    "golang programming basics",
	}); err != nil {
		t.Fatalf("IndexFields: %v", err)
	}

	tokens := idx.Tokenize("golang")

	// Score with both fields active
	both, err := idx.SearchBM25F("col", tokens, 10, map[string]float64{
		"meta.title": 3.0,
		"content":    1.0,
	}, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}

	// Score with title weight = 0 (ignored)
	contentOnly, err := idx.SearchBM25F("col", tokens, 10, map[string]float64{
		"meta.title": 0,
		"content":    1.0,
	}, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}

	if len(both) != 1 || len(contentOnly) != 1 {
		t.Fatalf("expected 1 result each, got %d and %d", len(both), len(contentOnly))
	}

	// Score with title should be higher than content-only
	if both[0].Score <= contentOnly[0].Score {
		t.Errorf("score with title (%.4f) should be > content-only (%.4f)",
			both[0].Score, contentOnly[0].Score)
	}
}

func TestBM25FRemoveCleanup(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	if err := indexWithFields(idx, "col", "d1", map[string]string{
		"meta.title": "golang tutorial",
		"content":    "learn golang programming",
	}); err != nil {
		t.Fatalf("IndexFields: %v", err)
	}

	tokens := idx.Tokenize("golang")
	results, err := idx.SearchBM25F("col", tokens, 10, nil, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result before removal, got %d", len(results))
	}

	// Remove should clean up field data too
	if err := idx.Remove("col", "d1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	results, err = idx.SearchBM25F("col", tokens, 10, nil, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after removal, got %d", len(results))
	}
}

func TestBM25FReindex(t *testing.T) {
	idx, cleanup := newTestFTSIndex(t)
	defer cleanup()

	// Index initially
	if err := indexWithFields(idx, "col", "d1", map[string]string{
		"meta.title": "golang tutorial",
		"content":    "learn programming basics",
	}); err != nil {
		t.Fatalf("IndexFields: %v", err)
	}

	// Re-index with different content
	if err := indexWithFields(idx, "col", "d1", map[string]string{
		"meta.title": "python guide",
		"content":    "learn python scripting",
	}); err != nil {
		t.Fatalf("IndexFields update: %v", err)
	}

	// Old term should not match
	goTokens := idx.Tokenize("golang")
	results, err := idx.SearchBM25F("col", goTokens, 10, nil, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for old term after reindex, got %d", len(results))
	}

	// New term should match
	pyTokens := idx.Tokenize("python")
	results, err = idx.SearchBM25F("col", pyTokens, 10, nil, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for new term after reindex, got %d", len(results))
	}
}

// indexWithFields indexes a document the way the server does: the body
// through the main index, which also serves BM25F's "content" field (#317),
// and every field through the per-field index.
func indexWithFields(idx *FTSIndex, collection, docID string, fields map[string]string) error {
	if body, ok := fields[contentField]; ok {
		if err := idx.Index(collection, docID, body); err != nil {
			return err
		}
	}
	return idx.IndexFields(collection, docID, fields)
}
