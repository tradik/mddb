package fts

import "testing"

// RAG-009 adds meta.summary to a copy of the defaults; the defaults must not
// change under it.
func TestDefaultBM25FWeightsIsACopy(t *testing.T) {
	w := DefaultBM25FWeights()
	if w["content"] != 1.0 || w["meta.title"] != 3.0 {
		t.Fatalf("defaults: %v", w)
	}
	w["meta.summary"] = 2
	if _, leaked := defaultBM25FWeights["meta.summary"]; leaked {
		t.Fatal("the shared defaults were modified")
	}
}
