package vector

import (
	"sync"
	"testing"
)

// Compact builds the new graph without the index's lock (SRCH-023); what
// was written meanwhile is caught up when the new graph takes over.

// withCompactHook runs fn between Compact's build and its takeover.
func withCompactHook(t *testing.T, fn func()) {
	t.Helper()
	hnswCompactBuilt = fn
	t.Cleanup(func() { hnswCompactBuilt = nil })
}

func TestPersistCompactCatchesUpConcurrentWrites(t *testing.T) {
	h := NewHNSWIndex(8, 64, 64)
	vecs := persistVecs(100, 8, 41)
	for id, v := range vecs {
		_ = h.Add("c", id, v)
	}
	h.Remove("c", "d1") // one tombstone to clear
	replacement := persistVecs(1, 8, 42)["d0"]
	withCompactHook(t, func() {
		h.Remove("c", "d2")
		_ = h.Add("c", "d3", replacement)
		_ = h.Add("c", "new", vecs["d4"])
	})
	h.Compact("c")

	if got := h.DeletedSince("c"); got != 2 {
		t.Fatalf("tombstones %d, want d2 and the old d3", got)
	}
	if got := h.CollectionSize("c"); got != 99 {
		t.Fatalf("size %d", got)
	}
	hits := h.Search("c", replacement, 1, -1, nil)
	if len(hits) != 1 || hits[0].DocID != "d3" {
		t.Fatalf("the overwritten vector is not found: %v", hits)
	}
	for _, r := range h.Search("c", vecs["d2"], 100, -1, nil) {
		if r.DocID == "d2" || r.DocID == "d1" {
			t.Fatalf("a removed document came back: %v", r)
		}
	}
	found := false
	for _, r := range h.Search("c", vecs["d4"], 2, -1, nil) {
		found = found || r.DocID == "new"
	}
	if !found {
		t.Fatal("the document added during the build is missing")
	}
}

func TestPersistCompactYieldsToANewerGraph(t *testing.T) {
	h := NewHNSWIndex(8, 64, 64)
	vecs := persistVecs(20, 8, 43)
	for id, v := range vecs {
		_ = h.Add("c", id, v)
	}
	// Removing most of the collection meanwhile rebuilds the graph on its
	// own: that one is newer, and Compact leaves it.
	withCompactHook(t, func() {
		for i := range 15 {
			h.Remove("c", persistDocID(i))
		}
	})
	h.Compact("c")
	if got := h.CollectionSize("c"); got != 5 {
		t.Fatalf("size %d", got)
	}
}

func TestPersistCompactOfAnEmptiedCollection(t *testing.T) {
	h := NewHNSWIndex(8, 64, 64)
	vecs := persistVecs(4, 8, 44)
	for id, v := range vecs {
		_ = h.Add("c", id, v)
	}
	withCompactHook(t, func() { h.DropCollection("c") })
	h.Compact("c")
	if h.CollectionSize("c") != 0 || len(h.Search("c", vecs["d0"], 1, -1, nil)) != 0 {
		t.Fatal("a dropped collection came back")
	}
	h.Compact("never") // nothing alive: nothing built
}

func TestPersistCompactRunsBesideSearches(t *testing.T) {
	h := NewHNSWIndex(8, 64, 64)
	vecs := persistVecs(300, 8, 45)
	for id, v := range vecs {
		_ = h.Add("c", id, v)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 50 {
			_ = h.Search("c", vecs["d7"], 5, -1, nil)
		}
	})
	wg.Go(func() { h.Compact("c") })
	wg.Go(func() { _ = h.persistSnapshot("c") })
	wg.Wait()
	if h.CollectionSize("c") != 300 {
		t.Fatal("size changed")
	}
}

// persistDocID is the key persistVecs gives vector i.
func persistDocID(i int) string { return "d" + itoa(i) }

// itoa formats a small non-negative int.
func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i)) // #nosec G115 -- i is a small non-negative test index
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func TestPersistAutoTrainRecheck(t *testing.T) {
	var mu sync.RWMutex
	due := &trainState{}
	vecs := map[string][]float32{"a": {1}}
	calls := 0
	trained := false
	// The state seen under the write lock differs from the one seen under
	// the read lock: another search got there first.
	for _, second := range []*trainState{nil, {trained: true, trainedOn: 1}} {
		calls = 0
		autoTrain(&mu, func() (*trainState, map[string][]float32) {
			calls++
			if calls == 1 {
				return due, vecs
			}
			return second, vecs
		}, func(map[string][]float32) { trained = true })
		if trained || calls != 2 {
			t.Fatalf("trained %v calls %d", trained, calls)
		}
	}
}

func TestPersistSQ4DefensivePaths(t *testing.T) {
	if lo, hi := clipRange(nil, 0.01); lo != 0 || hi != 0 {
		t.Fatal("clipRange of nothing")
	}
	s := NewSQ4Index()
	vecs := persistVecs(20, 4, 46)
	s.Train("c", vecs)
	// A code without its vector, and a vector of another length, are
	// skipped by the re-rank rather than scored.
	s.mu.Lock()
	c := s.data["c"]
	c.codes["ghost"] = c.codes["d0"]
	c.origVecs["d1"] = []float32{1}
	s.mu.Unlock()
	for _, r := range s.Search("c", vecs["d0"], 20, -1, nil) {
		if r.DocID == "ghost" || r.DocID == "d1" {
			t.Fatalf("%v was scored", r)
		}
	}
}
