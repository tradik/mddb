package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"mddb/internal/fts"
	"mddb/internal/logging"

	bolt "go.etcd.io/bbolt"
)

// GO-027: batching FTS work into one transaction means a single failure would
// take a whole batch down, where the per-document path used to swallow
// individual errors. indexBatchFTS keeps that contract with a fallback, and
// these cover it — the fast path, the fallback, and the cases that do nothing.

func newFTSOnlyServer(t *testing.T, readOnly bool) *Server {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/fts.db"

	// Create and initialise the buckets first; a read-only handle cannot.
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	idx := fts.NewFTSIndex(db)
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = bolt.Open(path, 0600, &bolt.Options{ReadOnly: readOnly})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	idx = fts.NewFTSIndex(db)
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	idx.SetStemmer(fts.NewPorterStemmer())
	idx.SetLangRegistry(reg)
	return &Server{FTSIndex: idx}
}

func batchOf(n int) []fts.BulkDoc {
	docs := make([]fts.BulkDoc, n)
	for i := range docs {
		docs[i] = fts.BulkDoc{
			DocID:   string(rune('a'+i)) + "-doc",
			Content: "the quick brown fox jumps over the lazy dog",
			Lang:    "en",
			Fields:  map[string]string{"content": "the quick brown fox"},
		}
	}
	return docs
}

func TestIndexBatchFTSWritesTheBatch(t *testing.T) {
	s := newFTSOnlyServer(t, false)
	s.indexBatchFTS("c", batchOf(3))

	res, err := s.FTSIndex.Search("c", "quick", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Errorf("expected 3 indexed documents, got %d", len(res))
	}
}

func TestIndexBatchFTSFallsBackWhenTheBatchFails(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	var logged bytes.Buffer
	slog.SetDefault(slog.New(logging.NewHandler(&logged, logging.FormatJSON, slog.LevelDebug)))

	// A read-only database makes every write transaction fail, so the batch
	// cannot commit and the per-document retry runs.
	s := newFTSOnlyServer(t, true)
	s.indexBatchFTS("c", batchOf(2))

	out := logged.String()
	if !strings.Contains(out, "batch FTS indexing failed, retrying per document") {
		t.Errorf("the fallback should announce itself, got %q", out)
	}
	// The retry cannot succeed on a read-only database either; what matters is
	// that each document is reported rather than the batch failing silently.
	if strings.Count(out, "FTS indexing failed") < 2 {
		t.Errorf("each document's failure should be reported, got %q", out)
	}
}

func TestIndexBatchFTSIgnoresNothingToDo(t *testing.T) {
	s := newFTSOnlyServer(t, false)
	s.indexBatchFTS("c", nil)             // empty batch
	s.indexBatchFTS("c", []fts.BulkDoc{}) // explicitly empty

	noIndex := &Server{}
	noIndex.indexBatchFTS("c", batchOf(1)) // no FTS index configured at all
}
