package fts

import (
	"fmt"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func newBulkTestIndex(t testing.TB) *FTSIndex {
	t.Helper()
	db, err := bolt.Open(t.TempDir()+"/fts.db", 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	idx := NewFTSIndex(db)
	if err := idx.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	reg := NewLangRegistry("en")
	RegisterDefaultLanguages(reg)
	idx.SetStemmer(NewPorterStemmer())
	idx.SetLangRegistry(reg)
	return idx
}

// dumpBuckets reads every FTS bucket into a comparable map, so two indexes can
// be checked for byte-level equality rather than "search still finds things".
func dumpBuckets(t testing.TB, idx *FTSIndex) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := idx.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			return b.ForEach(func(k, v []byte) error {
				out[string(name)+"|"+string(k)] = string(v)
				return nil
			})
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func bulkTestDocs(n int) []BulkDoc {
	docs := make([]BulkDoc, n)
	for i := range docs {
		body := fmt.Sprintf("The quick brown fox number %d jumps over the lazy dogs and runs far away", i)
		docs[i] = BulkDoc{
			DocID:   fmt.Sprintf("doc-%03d", i),
			Content: body,
			Lang:    "en",
			Fields: map[string]string{
				"content":   body,
				"meta.tags": "guide docs reference",
			},
		}
	}
	return docs
}

// TestIndexDocsMatchesPerDocumentIndexing is the guarantee the whole
// optimisation rests on: batching may only move the transaction boundary, not
// change what ends up in the index.
func TestIndexDocsMatchesPerDocumentIndexing(t *testing.T) {
	docs := bulkTestDocs(25)

	single := newBulkTestIndex(t)
	for _, d := range docs {
		if err := single.IndexWithLang("c", d.DocID, d.Content, d.Lang); err != nil {
			t.Fatal(err)
		}
		if err := single.IndexPositionsWithLang("c", d.DocID, d.Content, d.Lang); err != nil {
			t.Fatal(err)
		}
		if err := single.IndexFieldsWithLang("c", d.DocID, d.Fields, d.Lang); err != nil {
			t.Fatal(err)
		}
	}

	bulk := newBulkTestIndex(t)
	if err := bulk.IndexDocs("c", docs); err != nil {
		t.Fatal(err)
	}

	want, got := dumpBuckets(t, single), dumpBuckets(t, bulk)
	if len(want) != len(got) {
		t.Fatalf("bucket entry counts differ: per-document %d, bulk %d", len(want), len(got))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("entry %q differs: per-document %q, bulk %q", k, v, got[k])
		}
	}
}

// Re-indexing must replace, not accumulate — the reverse index exists to clear
// a document's previous terms, and the batch path has to honour it too.
func TestIndexDocsReplacesPreviousTerms(t *testing.T) {
	idx := newBulkTestIndex(t)
	first := []BulkDoc{{DocID: "d1", Content: "alpha beta gamma", Lang: "en"}}
	if err := idx.IndexDocs("c", first); err != nil {
		t.Fatal(err)
	}
	second := []BulkDoc{{DocID: "d1", Content: "delta epsilon", Lang: "en"}}
	if err := idx.IndexDocs("c", second); err != nil {
		t.Fatal(err)
	}

	res, err := idx.Search("c", "alpha", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Errorf("the replaced term is still indexed: %v", res)
	}
	res, err = idx.Search("c", "delta", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Errorf("the new term is not searchable: %v", res)
	}
}

func TestIndexDocsSearchable(t *testing.T) {
	idx := newBulkTestIndex(t)
	if err := idx.IndexDocs("c", bulkTestDocs(10)); err != nil {
		t.Fatal(err)
	}
	res, err := idx.Search("c", "quick", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 10 {
		t.Errorf("expected all 10 documents to match, got %d", len(res))
	}
}

func TestIndexDocsHandlesEmptyInput(t *testing.T) {
	idx := newBulkTestIndex(t)
	if err := idx.IndexDocs("c", nil); err != nil {
		t.Errorf("an empty batch should be a no-op, got %v", err)
	}
	if err := idx.IndexDocs("c", []BulkDoc{{DocID: "empty", Content: "", Lang: "en"}}); err != nil {
		t.Errorf("a document with no content should be skipped, got %v", err)
	}
}

func TestIndexDocsIndexesFieldsWithoutContent(t *testing.T) {
	idx := newBulkTestIndex(t)
	docs := []BulkDoc{{
		DocID:  "meta-only",
		Lang:   "en",
		Fields: map[string]string{"meta.tags": "kubernetes observability"},
	}}
	if err := idx.IndexDocs("c", docs); err != nil {
		t.Fatal(err)
	}
	res, err := idx.SearchBM25F("c", idx.TokenizeLang("kubernetes", "en"), 10,
		map[string]float64{"meta.tags": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Errorf("expected the field-only document to be findable, got %v", res)
	}
}

// Phrase search reads the positions index, so this proves the batch wrote it.
func TestIndexDocsWritesPositions(t *testing.T) {
	idx := newBulkTestIndex(t)
	docs := []BulkDoc{{DocID: "p1", Content: "the quick brown fox", Lang: "en"}}
	if err := idx.IndexDocs("c", docs); err != nil {
		t.Fatal(err)
	}
	res, err := idx.SearchPhrase("c", "quick brown", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Errorf("phrase search found nothing, so positions were not written: %v", res)
	}
}

func BenchmarkIndexPerDocument(b *testing.B) {
	docs := bulkTestDocs(200)
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		idx := newBulkTestIndex(b)
		b.StartTimer()
		for _, d := range docs {
			_ = idx.IndexWithLang("c", d.DocID, d.Content, d.Lang)
			_ = idx.IndexPositionsWithLang("c", d.DocID, d.Content, d.Lang)
			_ = idx.IndexFieldsWithLang("c", d.DocID, d.Fields, d.Lang)
		}
	}
}

func BenchmarkIndexDocsBatched(b *testing.B) {
	docs := bulkTestDocs(200)
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		idx := newBulkTestIndex(b)
		b.StartTimer()
		_ = idx.IndexDocs("c", docs)
	}
}
