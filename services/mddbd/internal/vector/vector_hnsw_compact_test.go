package vector

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// GO-029. Deleting a large share of an HNSW collection used to make the next
// search dereference a nil node inside coder/hnsw and panic — reproducibly
// from about half of a 1000-vector collection, and always once everything was
// deleted. That is the shape agent memory and TTL-expiring collections reach
// on their own.

func compactTestIndex(t testing.TB, total int, deleteRatio float64) (*HNSWIndex, []string) {
	t.Helper()
	r := rand.New(rand.NewPCG(11, 22)) //nolint:gosec // G404: deterministic test data is the point here
	idx := NewHNSWIndex(16, 0, 100)
	ids := make([]string, total)
	for i := range total {
		ids[i] = fmt.Sprintf("doc-%05d", i)
		v := make([]float32, 32)
		for j := range v {
			v[j] = r.Float32()
		}
		idx.Add("c", ids[i], v)
	}
	cut := int(float64(total) * deleteRatio)
	for i := range cut {
		idx.Remove("c", ids[i])
	}
	return idx, ids[cut:]
}

func query32(seed uint64) []float32 {
	r := rand.New(rand.NewPCG(seed, seed)) //nolint:gosec // G404: deterministic test data is the point here
	v := make([]float32, 32)
	for i := range v {
		v[i] = r.Float32()
	}
	return v
}

func TestSearchSurvivesHeavyDeletion(t *testing.T) {
	for _, ratio := range []float64{0.5, 0.75, 0.9} {
		t.Run(fmt.Sprintf("deleted-%.0f%%", ratio*100), func(t *testing.T) {
			idx, live := compactTestIndex(t, 1000, ratio)
			res := idx.Search("c", query32(3), 10, 0, CosineSimilarity)
			if len(res) == 0 {
				t.Fatalf("search returned nothing with %d live vectors", len(live))
			}
			liveSet := map[string]bool{}
			for _, id := range live {
				liveSet[id] = true
			}
			for _, r := range res {
				if !liveSet[r.DocID] {
					t.Errorf("search returned deleted document %q", r.DocID)
				}
			}
		})
	}
}

func TestSearchOnFullyDeletedCollection(t *testing.T) {
	idx, _ := compactTestIndex(t, 200, 1.0)
	if res := idx.Search("c", query32(4), 10, 0, CosineSimilarity); len(res) != 0 {
		t.Errorf("a collection with nothing left should return nothing, got %d", len(res))
	}
	if got := idx.CollectionSize("c"); got != 0 {
		t.Errorf("CollectionSize = %d, want 0", got)
	}
}

func TestCompactionTriggersAtThreshold(t *testing.T) {
	idx, _ := compactTestIndex(t, 100, 0)
	if got := idx.DeletedSince("c"); got != 0 {
		t.Fatalf("a fresh collection has no deletion debt, got %d", got)
	}

	// Below the threshold the debt accumulates...
	for i := range 10 {
		idx.Remove("c", fmt.Sprintf("doc-%05d", i))
	}
	if got := idx.DeletedSince("c"); got != 10 {
		t.Errorf("DeletedSince = %d, want 10", got)
	}

	// ...and crossing it rebuilds. With a 20% threshold the rebuild happens on
	// the 20th deletion (80 live, 20 gone), which resets the debt; the five
	// deletions after that start accumulating the next round.
	for i := 10; i < 25; i++ {
		idx.Remove("c", fmt.Sprintf("doc-%05d", i))
	}
	if got := idx.DeletedSince("c"); got != 5 {
		t.Errorf("DeletedSince after 25 deletions with a rebuild at 20 = %d, want 5", got)
	}
	if got := idx.CollectionSize("c"); got != 75 {
		t.Errorf("the rebuilt graph should hold the 75 live vectors, got %d", got)
	}
}

func TestRemovingAnAbsentDocumentIsNotDebt(t *testing.T) {
	idx, _ := compactTestIndex(t, 50, 0)
	idx.Remove("c", "never-existed")
	if got := idx.DeletedSince("c"); got != 0 {
		t.Errorf("removing something that was never there is not deletion debt, got %d", got)
	}
}

func TestExplicitCompactRebuildsAndKeepsResults(t *testing.T) {
	idx, live := compactTestIndex(t, 300, 0.1)
	before := idx.Search("c", query32(5), 5, 0, CosineSimilarity)

	idx.Compact("c")

	if got := idx.DeletedSince("c"); got != 0 {
		t.Errorf("Compact should clear the deletion debt, got %d", got)
	}
	if got := idx.CollectionSize("c"); got != len(live) {
		t.Errorf("CollectionSize after compaction = %d, want %d", got, len(live))
	}
	after := idx.Search("c", query32(5), 5, 0, CosineSimilarity)
	// Compaction recovers recall rather than merely preserving it: before it,
	// deleted nodes still occupy slots in the graph's top-K and are dropped
	// when the results are checked against the live map, so the caller gets
	// fewer than they asked for. A rebuilt graph holds only live vectors.
	if len(after) < len(before) {
		t.Errorf("compaction lost results: %d before, %d after", len(before), len(after))
	}
	// HNSW is approximate, so a rebuilt graph may legitimately rank a
	// different neighbour first; what must hold is that every answer is a
	// live document.
	liveSet := map[string]bool{}
	for _, id := range live {
		liveSet[id] = true
	}
	for _, r := range after {
		if !liveSet[r.DocID] {
			t.Errorf("compaction returned a deleted document: %q", r.DocID)
		}
	}
}

// TestDeletedDocumentsNeverSurfaceInResults pins the behaviour the graph
// library gets wrong on its own: Delete reports success and Len() drops, yet
// Search keeps handing the node back. Without the live-map check a vector
// search answers with documents the caller deleted.
func TestDeletedDocumentsNeverSurfaceInResults(t *testing.T) {
	idx, _ := compactTestIndex(t, 60, 0)

	// Delete a few, staying under the compaction threshold so the graph keeps
	// its stale nodes — the exact window where the library misbehaves.
	deleted := []string{"doc-00001", "doc-00002", "doc-00003"}
	for _, id := range deleted {
		idx.Remove("c", id)
	}
	if idx.DeletedSince("c") == 0 {
		t.Fatal("the test needs the graph left uncompacted to be meaningful")
	}

	q := query32(9)
	for _, res := range [][]VectorResult{
		idx.Search("c", q, 60, -1, CosineSimilarity),
		idx.SearchWithFilter("c", q, 60, -1, allowAll(idx, "c"), CosineSimilarity),
	} {
		for _, r := range res {
			for _, gone := range deleted {
				if r.DocID == gone {
					t.Errorf("deleted document %q came back in results", gone)
				}
			}
		}
	}
}

func allowAll(idx *HNSWIndex, collection string) map[string]bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := map[string]bool{}
	for k := range idx.vectors[collection] {
		out[BaseDocID(k)] = true
	}
	return out
}

func TestCompactOnUnknownCollectionIsSafe(t *testing.T) {
	idx := NewHNSWIndex(16, 0, 100)
	idx.Compact("nothing-here")
	if got := idx.DeletedSince("nothing-here"); got != 0 {
		t.Errorf("DeletedSince on an unknown collection = %d, want 0", got)
	}
}

// bruteForceLocked is the answer when the graph cannot be searched; it has to
// respect topK, the threshold and the allowed set.
func TestBruteForceFallback(t *testing.T) {
	idx, _ := compactTestIndex(t, 40, 0)
	q := query32(6)

	idx.mu.RLock()
	all := idx.bruteForceLocked("c", q, 5, 0, CosineSimilarity, nil)
	filtered := idx.bruteForceLocked("c", q, 5, 0, CosineSimilarity, map[string]bool{"doc-00001": true})
	thresholded := idx.bruteForceLocked("c", q, 40, 1.1, CosineSimilarity, nil)
	missing := idx.bruteForceLocked("absent", q, 5, 0, CosineSimilarity, nil)
	idx.mu.RUnlock()

	if len(all) != 5 {
		t.Errorf("expected topK=5 results, got %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Score < all[i].Score {
			t.Errorf("results are not sorted by descending score: %v", all)
			break
		}
	}
	if len(filtered) != 1 || filtered[0].DocID != "doc-00001" {
		t.Errorf("the allowed set was not respected: %v", filtered)
	}
	if len(thresholded) != 0 {
		t.Errorf("a threshold above any possible score should exclude everything, got %d", len(thresholded))
	}
	if missing != nil {
		t.Errorf("an unknown collection should yield nothing, got %v", missing)
	}
}
