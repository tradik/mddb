package vector

import (
	"fmt"
	"testing"
)

// The class of #269 across every searcher: a collection holding embeddings
// from two models (384 and 768 dimensions), queried with either, or with a
// dimension it holds none of. A search must not panic, and must not return a
// vector the query cannot be compared with.
//
// HNSW refuses a second dimension at Add (#252), so it holds only the first;
// the rest accept both.
func TestEverySearcherSurvivesAMixedDimensionCollection(t *testing.T) {
	quantized := NewQuantizedVectorIndex(func(string) QuantizationType { return QuantInt8 })
	searchers := map[string]VectorSearcher{
		"flat": NewVectorIndex(), "hnsw": NewHNSWIndex(16, 200, 100), "ivf": NewIVFIndex(4, 5),
		"pq": NewPQIndex(8, 16, 5), "opq": NewOPQIndex(8, 16, 5, 1), "sq": NewSQIndex(),
		"sq4": NewSQ4Index(), "bq": NewBQIndex(10), "quantized": quantized,
	}
	for name, s := range searchers {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked: %v", r)
				}
			}()
			add := func(prefix string, dim, n int) {
				for i := 0; i < n; i++ {
					_ = s.Add("c", fmt.Sprintf("%s%d", prefix, i), vec(dim, float32(i)/10))
				}
			}
			// The old model's vectors, trained on; then the new model's, added
			// to a trained index; then a search, which may retrain.
			add("small", 384, 6)
			s.Search("c", vec(384, 0), 3, -1, nil)
			add("large", 768, 6)

			for _, dim := range []int{384, 768, 100} {
				for _, hits := range [][]VectorResult{
					s.Search("c", vec(dim, 0.1), 20, -1, nil),
					s.SearchWithFilter("c", vec(dim, 0.1), 20, -1, map[string]bool{"small0": true, "large0": true}, nil),
				} {
					for _, h := range hits {
						want := "small"
						if dim == 768 {
							want = "large"
						}
						if dim == 100 || h.DocID[:len(want)] != want {
							t.Errorf("a %d-dimension query returned %s", dim, h.DocID)
						}
					}
				}
			}
		})
	}
}
