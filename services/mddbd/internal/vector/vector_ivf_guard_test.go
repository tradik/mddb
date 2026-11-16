package vector

import (
	"fmt"
	"testing"
)

// The guards of IVF that no ordinary write reaches: a cluster without a map,
// a probe index past the clusters, a stored vector of another dimension. They
// protect the scoring from a structure restored or edited some other way;
// these tests build that structure by hand.

func ivfWithClusters(t *testing.T, n int) (*IVFIndex, *ivfCollection) {
	t.Helper()
	idx := NewIVFIndex(2, 5)
	vecs := map[string][]float32{}
	for i := range n {
		g := float32(i % 4)
		vecs[fmt.Sprintf("d%03d", i)] = []float32{g, 1 - g, float32(i%7) / 10}
	}
	for id, v := range vecs {
		if err := idx.Add("c", id, v); err != nil {
			t.Fatal(err)
		}
	}
	idx.Train("c", vecs)
	return idx, idx.data["c"]
}

func TestIVFAddMakesAMissingClusterMap(t *testing.T) {
	idx, c := ivfWithClusters(t, 40)
	for i := range c.clusters {
		c.clusters[i] = nil
	}
	if err := idx.Add("c", "late", []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cl := range c.clusters {
		_, ok := cl["late"]
		found = found || ok
	}
	if !found {
		t.Fatal("the vector was not clustered")
	}
}

func TestIVFSearchClustersSkipsWhatItCannotScore(t *testing.T) {
	for _, n := range []int{40, 4_000} { // sequential and parallel paths
		idx, c := ivfWithClusters(t, n)
		c.clusters[0]["odd"] = []float32{1, 2} // another dimension
		probes := []int{len(c.clusters) + 3}   // past the end, then every cluster
		for i := range c.clusters {
			probes = append(probes, i)
		}
		got := idx.searchClusters(c, []float32{0, 1, 0}, 5, -1, CosineSimilarity, probes, nil)
		for _, r := range got {
			if r.DocID == "odd" {
				t.Fatalf("n=%d: a vector of another dimension was scored", n)
			}
		}
		if len(got) == 0 {
			t.Fatalf("n=%d: nothing found", n)
		}
	}
}

func TestIVFClusters(t *testing.T) {
	for n, want := range map[int]int{0: 1, 1: 1, 100: 10, 70_000: 256} {
		if got := ivfClusters(n); got != want {
			t.Errorf("ivfClusters(%d) = %d, want %d", n, got, want)
		}
	}
}
