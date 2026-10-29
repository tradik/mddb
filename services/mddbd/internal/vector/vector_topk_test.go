package vector

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"testing"
)

// The bounded heap keeps exactly what sorting everything and cutting at k
// keeps, ties included.
func TestTopKKeepsWhatAFullSortKeeps(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 9)) //nolint:gosec // G404: deterministic test data
	var all []VectorResult
	for i := range 500 {
		// Few distinct scores, so ties are common and the docID tie-break
		// decides membership.
		all = append(all, VectorResult{DocID: fmt.Sprintf("d%03d", i), Score: float32(r.IntN(20))})
	}
	rank := func(rs []VectorResult) []VectorResult {
		out := append([]VectorResult(nil), rs...)
		sort.Slice(out, func(i, j int) bool { return worse(out[j], out[i]) })
		return out
	}
	for _, k := range []int{1, 7, 64, 500, 900} {
		best := newTopK(k)
		for _, res := range all {
			best.add(res)
		}
		want := rank(all)[:min(k, len(all))]
		if got := rank(best.results()); !reflect.DeepEqual(got, want) {
			t.Fatalf("k=%d: kept %v,\nwant %v", k, got[:min(5, len(got))], want[:min(5, len(want))])
		}
	}
}

func TestTopKWithoutALimitKeepsEverything(t *testing.T) {
	best := newTopK(0)
	for i := range 3 {
		best.add(VectorResult{DocID: fmt.Sprint(i)})
	}
	if len(best.results()) != 3 {
		t.Fatalf("kept %d of 3", len(best.results()))
	}
}

// A huge topK from a request reserves at most topKPrealloc up front, and past
// that the heap still keeps exactly k results.
func TestTopKBoundsItsPreallocation(t *testing.T) {
	if got := cap(newTopK(1_000_000_000).items); got != topKPrealloc {
		t.Fatalf("capacity for k=1e9 = %d, want %d", got, topKPrealloc)
	}
	k := topKPrealloc + 500
	best := newTopK(k)
	for i := 0; i < k+700; i++ {
		best.add(VectorResult{DocID: fmt.Sprintf("d%05d", i), Score: float32(i)})
	}
	if n := len(best.results()); n != k {
		t.Fatalf("kept %d results, want %d", n, k)
	}
}
