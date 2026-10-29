package fts

import (
	"os"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"abc", "ab", 1},
		{"abc", "abcd", 1},
		{"abc", "axc", 1},
		{"kitten", "sitting", 3},
		{"javascript", "javascrip", 1},
		{"javascript", "javasript", 1},
		{"javascript", "javasrip", 2},
		{"golang", "golng", 1},
		{"golang", "golag", 1},
		{"hello", "hola", 3},
	}
	for _, tt := range tests {
		got := levenshteinDistance(tt.a, tt.b)
		if got != tt.expected {
			t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.expected)
		}
	}
}

func TestLevenshteinDistance_Symmetric(t *testing.T) {
	pairs := [][2]string{
		{"abc", "axc"},
		{"kitten", "sitting"},
		{"javascript", "javascrip"},
	}
	for _, p := range pairs {
		d1 := levenshteinDistance(p[0], p[1])
		d2 := levenshteinDistance(p[1], p[0])
		if d1 != d2 {
			t.Errorf("levenshteinDistance(%q,%q)=%d != levenshteinDistance(%q,%q)=%d", p[0], p[1], d1, p[1], p[0], d2)
		}
	}
}

func newTestFTSForFuzzy(t *testing.T) (*FTSIndex, func()) {
	t.Helper()
	f, err := os.CreateTemp("", "fts_fuzzy_*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	db, err := bolt.Open(f.Name(), 0600, nil)
	if err != nil {
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	fts := NewFTSIndex(db)
	if err := fts.EnsureBuckets(); err != nil {
		_ = db.Close()
		_ = os.Remove(f.Name())
		t.Fatal(err)
	}
	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(f.Name())
	}
	return fts, cleanup
}

func TestSearchFuzzy_ExactMatch(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	_ = fts.Index("docs", "doc1", "javascript programming tutorial")
	_ = fts.Index("docs", "doc2", "python programming guide")

	results, err := fts.SearchFuzzy("docs", "javascript", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for exact match with fuzzy=1")
	}
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1, got %s", results[0].DocID)
	}
}

func TestSearchFuzzy_OneTypo(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	_ = fts.Index("docs", "doc1", "javascript programming tutorial")
	_ = fts.Index("docs", "doc2", "python programming guide")

	// "javascrip" is 1 edit away from "javascript"
	results, err := fts.SearchFuzzy("docs", "javascrip", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for 1-typo fuzzy search")
	}
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1, got %s", results[0].DocID)
	}
	// Check matched terms contain fuzzy indicator
	found := false
	for _, mt := range results[0].MatchedTerms {
		if mt == "javascrip~javascript" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fuzzy match indicator in matchedTerms, got %v", results[0].MatchedTerms)
	}
}

func TestSearchFuzzy_TwoTypos(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	_ = fts.Index("docs", "doc1", "javascript programming tutorial")

	// "javasrip" is 2 edits away from "javascript"
	results, err := fts.SearchFuzzy("docs", "javasrip", 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for 2-typo fuzzy search")
	}
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1, got %s", results[0].DocID)
	}
}

func TestSearchFuzzy_NoResults(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	_ = fts.Index("docs", "doc1", "javascript programming tutorial")

	// "xyz" is too far from any indexed term
	results, err := fts.SearchFuzzy("docs", "xyz", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
}

func TestSearchFuzzy_FuzzyOneDoesNotMatchTwoEdits(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	_ = fts.Index("docs", "doc1", "javascript programming tutorial")

	// "javasrip" is 2 edits away, fuzzy=1 should not match
	results, err := fts.SearchFuzzy("docs", "javasrip", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected no results with fuzzy=1 for 2-edit term, got %d", len(results))
	}
}

func TestSearchBM25Fuzzy(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	_ = fts.Index("docs", "doc1", "javascript programming tutorial")
	_ = fts.Index("docs", "doc2", "python programming guide")

	results, err := fts.SearchBM25Fuzzy("docs", "javascrip", 10, 1, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for BM25 fuzzy search")
	}
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1, got %s", results[0].DocID)
	}
}

func TestSearchFuzzy_ScorePenalty(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	_ = fts.Index("docs", "doc1", "javascript programming tutorial")

	// Exact match
	exactResults, err := fts.SearchFuzzy("docs", "javascript", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Fuzzy match
	fuzzyResults, err := fts.SearchFuzzy("docs", "javascrip", 10, 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(exactResults) == 0 || len(fuzzyResults) == 0 {
		t.Fatal("expected results for both searches")
	}

	if fuzzyResults[0].Score >= exactResults[0].Score {
		t.Errorf("fuzzy score (%f) should be lower than exact score (%f)", fuzzyResults[0].Score, exactResults[0].Score)
	}
}

func TestSearchFuzzy_EmptyQuery(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	_ = fts.Index("docs", "doc1", "javascript programming tutorial")

	results, err := fts.SearchFuzzy("docs", "", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected no results for empty query, got %d", len(results))
	}
}

func TestSearchFuzzy_EmptyCollection(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	results, err := fts.SearchFuzzy("empty", "javascript", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected no results for empty collection, got %d", len(results))
	}
}

func TestSearchBM25Fuzzy_EmptyQuery(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	results, err := fts.SearchBM25Fuzzy("docs", "", 10, 1, DefaultBM25)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected no results for empty BM25 fuzzy query, got %d", len(results))
	}
}

func TestSearchFuzzy_MultipleTerms(t *testing.T) {
	fts, cleanup := newTestFTSForFuzzy(t)
	defer cleanup()

	_ = fts.Index("docs", "doc1", "javascript programming tutorial for beginners")
	_ = fts.Index("docs", "doc2", "python programming tutorial advanced")

	// Both terms with typos
	results, err := fts.SearchFuzzy("docs", "javascrip programing", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results for multi-term fuzzy search")
	}
	// doc1 should rank higher (matches both fuzzy terms)
	if results[0].DocID != "doc1" {
		t.Errorf("expected doc1 first, got %s", results[0].DocID)
	}
}
