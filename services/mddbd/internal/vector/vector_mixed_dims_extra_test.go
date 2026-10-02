package vector

import (
	"fmt"
	"testing"
)

// Found while fixing #269: a re-added document in IVF stayed in the cluster
// of its old vector as well as joining the cluster of its new one, and kept
// answering with the vector it had replaced.
func TestIVFReAddLeavesNoOldClusterEntry(t *testing.T) {
	idx := NewIVFIndex(64, 5)
	all := map[string][]float32{}
	for i := 0; i < 16; i++ {
		all[fmt.Sprint(i)] = unitVec(i, 4)
		_ = idx.Add("c", fmt.Sprint(i), all[fmt.Sprint(i)])
	}
	idx.Train("c", all) // four clusters, one per direction
	before := nearestCentroid(unitVec(0, 4), idx.data["c"].centroids)
	if nearestCentroid(unitVec(1, 4), idx.data["c"].centroids) == before {
		t.Skip("training put both directions in one cluster; nothing to move between")
	}
	_ = idx.Add("c", "0", unitVec(1, 4)) // moves to another cluster

	seen := 0
	for _, cluster := range idx.data["c"].clusters {
		if _, ok := cluster["0"]; ok {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("document 0 is in %d clusters after a re-add, want 1", seen)
	}
}

// Found while fixing #269: the brute-force fallback of a filtered HNSW search
// compared chunk keys ("doc#0") with the filter's document ids ("doc"), and
// so never matched one.
func TestFilteredHNSWFallbackFindsChunks(t *testing.T) {
	h := NewHNSWIndex(16, 200, 100)
	for i := 0; i < 60; i++ {
		_ = h.Add("c", fmt.Sprintf("doc%d#0", i), unitVec(i, 8))
	}
	// One allowed document, far from the query: the graph's oversampled
	// neighbourhood will not contain it, so only the fallback can find it.
	hits := h.SearchWithFilter("c", unitVec(0, 8), 5, -1, map[string]bool{"doc7": true}, nil)
	if len(hits) != 1 || hits[0].DocID != "doc7#0" {
		t.Errorf("hits = %+v, want doc7#0 from the fallback", hits)
	}
}

type panickingTrainer struct{}

func (panickingTrainer) Train(string, map[string][]float32) { panic("bad shape") }

func TestTrainSafelyContainsAPanic(t *testing.T) {
	TrainSafely(panickingTrainer{}, "c", nil) // must return
}

func TestSameDimensionPicksTheMajorityAndBreaksTiesUpward(t *testing.T) {
	v, dim := sameDimension(map[string][]float32{"a": make([]float32, 3), "b": make([]float32, 3), "c": make([]float32, 5)})
	if dim != 3 || len(v) != 2 {
		t.Errorf("majority: dim %d, %d vectors", dim, len(v))
	}
	if _, dim := sameDimension(map[string][]float32{"a": make([]float32, 3), "c": make([]float32, 5)}); dim != 5 {
		t.Errorf("tie: dim %d, want 5", dim)
	}
}
