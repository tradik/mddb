package vector

import (
	"fmt"
	"testing"
)

// Vectors under the configured 8 subspaces: Train clamps the subspace count to
// the dimension, and adding a vector afterwards used to encode with the
// configured count, run past the trained codebooks and panic.
func TestOPQLowDimensionAddAfterTraining(t *testing.T) {
	o := NewOPQIndex(8, 16, 5, 2)
	vecs := map[string][]float32{}
	for i := 0; i < 40; i++ {
		vecs[fmt.Sprintf("d%d", i)] = []float32{float32(i%5) + 1, float32(i%3) + 1, float32(i%7) + 1}
	}
	o.Train("c", vecs)
	if err := o.Add("c", "late", []float32{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	res := o.Search("c", []float32{1, 2, 3}, 3, 0, nil)
	if len(res) == 0 {
		t.Fatal("no results from a trained low-dimension index")
	}
}
