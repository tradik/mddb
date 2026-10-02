package vector

import (
	"strconv"
	"testing"
)

// TestVectorConstructorDefaults exercises each ANN index constructor's
// zero-value default and clamp branches (existing tests pass valid params, so
// the `<=0 -> default` and `>256 -> clamp` paths were uncovered).
func TestVectorConstructorDefaults(t *testing.T) {
	cases := []struct {
		name string
		idx  VectorSearcher
	}{
		{"hnsw", NewHNSWIndex(0, 0, 0)},
		{"ivf", NewIVFIndex(0, 0)},
		{"bq", NewBQIndex(0)},
		{"pq", NewPQIndex(0, 0, 0)},
		{"pq", NewPQIndex(8, 300, 5)}, // codebookSize > 256 -> clamped to 256
		{"opq", NewOPQIndex(0, 0, 0, 0)},
		{"opq", NewOPQIndex(8, 300, 5, 5)}, // codebookSize > 256 -> clamped
		{"sq", NewSQIndex()},
	}
	for _, c := range cases {
		if c.idx == nil {
			t.Errorf("%s constructor returned nil", c.name)
			continue
		}
		if c.idx.Name() != c.name {
			t.Errorf("Name() = %q, want %q", c.idx.Name(), c.name)
		}
	}
}

// TestQuantizedIndexHelpers covers the quantized-index helper branches:
// similarityFor and quantizeQueryAs for every type, resolveQuantType's configured-callback
// path, and baseDocIDQ's chunk-suffix split.
func TestQuantizedIndexHelpers(t *testing.T) {
	qi := &QuantizedVectorIndex{}
	if similarityFor(QuantInt4) == nil || similarityFor(QuantInt8) == nil || similarityFor(QuantNone) == nil {
		t.Error("similarityFor must return a function for every quant type")
	}
	if quantizeQueryAs(QuantNone, []float32{1}, 0, 1) != nil {
		t.Error("an unquantized type has no quantized query")
	}
	if qi.resolveQuantType("c") != QuantNone {
		t.Error("resolveQuantType with no callback should be QuantNone")
	}
	qi.getQuantType = func(string) QuantizationType { return QuantInt8 }
	if qi.resolveQuantType("c") != QuantInt8 {
		t.Error("resolveQuantType should use the configured callback")
	}
	if baseDocIDQ("doc#1") != "doc" || baseDocIDQ("plain") != "plain" {
		t.Error("baseDocIDQ chunk-suffix split")
	}
}

// TestVectorSearchWithFilterCoverage exercises SearchWithFilter's allowed-set
// branch across the no-training searchers (existing tests search with nil filter).
func TestVectorSearchWithFilterCoverage(t *testing.T) {
	vecs := [][]float32{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	for _, s := range []VectorSearcher{NewVectorIndex(), NewHNSWIndex(16, 0, 100), NewSQIndex(), NewBQIndex(0)} {
		for i, v := range vecs {
			if err := s.Add("c", docName(i), v); err != nil {
				t.Fatal(err)
			}
		}
		s.SetReady()
		allowed := map[string]bool{"doc0": true, "doc1": true}
		_ = s.SearchWithFilter("c", []float32{1, 0, 0}, 5, 0, allowed, nil)
	}
}

func docName(i int) string { return "doc" + strconv.Itoa(i) }
