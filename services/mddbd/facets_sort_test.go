package main

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"mddb/internal/storage"
)

// A facet key with very many distinct values (facetScope "all" over an id-like
// field) is sorted in O(n log n): the insertion sort it replaced took minutes
// here (#368 follow-up). The order is count desc, then value asc.
func TestSortFacetBucketsManyValues(t *testing.T) {
	const n = 200_000
	r := rand.New(rand.NewPCG(1, 2))
	buckets := make([]FacetBucket, n)
	for i := range buckets {
		buckets[i] = FacetBucket{Value: fmt.Sprintf("v%07d", i), Count: r.IntN(50)}
	}
	r.Shuffle(n, func(i, j int) { buckets[i], buckets[j] = buckets[j], buckets[i] })

	start := time.Now()
	sortFacetBuckets(buckets)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("sorting %d buckets took %v", n, d)
	}
	for i := 1; i < n; i++ {
		if facetLess(buckets[i], buckets[i-1]) {
			t.Fatalf("out of order at %d: %+v before %+v", i, buckets[i-1], buckets[i])
		}
	}
}

// Facets skip a document without metadata, and over an empty match set every
// requested key is present and empty.
func TestFacetsWithoutMetaOrMatches(t *testing.T) {
	got := computeFacets([]storage.Doc{{ID: "a"}, {ID: "b", Meta: map[string][]string{"k": {"v"}}}}, []string{"k"}, 0)
	if k := got["k"]; len(k) != 1 || k[0].Count != 1 {
		t.Errorf("k = %v, want v=1 (a has no meta)", k)
	}

	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	_ = addTestDoc(t, s, "ports", "a", "en", "x", map[string][]string{"arch": {"amd64"}})
	got = s.computeFacetsOverMatches("ports", map[string]bool{}, []string{"arch"}, 0)
	if a, ok := got["arch"]; !ok || len(a) != 0 {
		t.Errorf("arch over no matches = %v (present=%v), want present and empty", a, ok)
	}
}
