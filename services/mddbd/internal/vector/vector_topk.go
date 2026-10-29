package vector

import "sort"

// topK keeps the k best results seen so far in a bounded min-heap (SRCH-013).
//
// The exact scans collected every vector above the threshold and sorted them
// all to return k. With the default threshold of 0 that is most of the
// collection: at 4,000 admitted vectors the sort and its allocations were most
// of a filtered search's cost, and at 200,000 most of a flat one's. A heap of k
// makes the scan O(n log k) with one allocation.
type topK struct {
	k     int
	items []VectorResult // a min-heap: the worst kept result on top
}

// topKPrealloc caps the capacity reserved up front. k is the caller's topK, a
// request value: reserving all of it let topK=1e9 ask for gigabytes before a
// single vector was scored. The heap still holds k results; past the cap it
// grows by append, as far as there are results to keep.
const topKPrealloc = 1024

// newTopK collects the k best results; k <= 0 keeps every result.
func newTopK(k int) *topK {
	if k <= 0 {
		return &topK{}
	}
	capacity := k
	if capacity > topKPrealloc {
		capacity = topKPrealloc
	}
	return &topK{k: k, items: make([]VectorResult, 0, capacity)}
}

// worse orders results the way rankings do not want them: lower score first,
// and on a tie the later document ID, so the kept set is deterministic.
func worse(a, b VectorResult) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.DocID > b.DocID
}

// add offers one result.
func (t *topK) add(r VectorResult) {
	if t.k <= 0 {
		t.items = append(t.items, r)
		return
	}
	if len(t.items) < t.k {
		t.items = append(t.items, r)
		t.up(len(t.items) - 1)
		return
	}
	if !worse(t.items[0], r) {
		return
	}
	t.items[0] = r
	t.down(0)
}

// mergeTop merges the workers' partial results into the topK best, best
// first, ties by docID ascending so equal scores come back in one order.
func mergeTop(partials [][]VectorResult, topK int) []VectorResult {
	total := 0
	for _, p := range partials {
		total += len(p)
	}
	merged := make([]VectorResult, 0, total)
	for _, p := range partials {
		merged = append(merged, p...)
	}
	sort.Slice(merged, func(i, j int) bool { return worse(merged[j], merged[i]) })
	if topK > 0 && len(merged) > topK {
		merged = merged[:topK]
	}
	return merged
}

// results returns what was kept, in no particular order.
func (t *topK) results() []VectorResult { return t.items }

func (t *topK) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !worse(t.items[i], t.items[p]) {
			return
		}
		t.items[i], t.items[p] = t.items[p], t.items[i]
		i = p
	}
}

func (t *topK) down(i int) {
	n := len(t.items)
	for {
		l, r, m := 2*i+1, 2*i+2, i
		if l < n && worse(t.items[l], t.items[m]) {
			m = l
		}
		if r < n && worse(t.items[r], t.items[m]) {
			m = r
		}
		if m == i {
			return
		}
		t.items[i], t.items[m] = t.items[m], t.items[i]
		i = m
	}
}
