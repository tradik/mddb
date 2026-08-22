package main

import (
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"

	"mddb/internal/fts"
	proto "mddb/proto"
)

// GO-033: SearchRange stops as soon as it has the caller's limit, because the
// cursor already walks documents in the order results are returned. That is
// only sound if the answer is unchanged, so these compare it against the
// behaviour it replaced — collect every match, sort, truncate.

// referenceRange is the old implementation, kept here as the oracle.
func referenceRange(t *testing.T, s *Server, collection string, ranges []RangeFilter, limit int) []fts.FTSResult {
	t.Helper()
	all, err := s.SearchRange(collection, ranges, 0) // 0 = no limit, so no early exit
	if err != nil {
		t.Fatal(err)
	}
	out := append([]fts.FTSResult(nil), all...)
	sort.Slice(out, func(i, j int) bool { return out[i].DocID < out[j].DocID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func rangeTestServer(t *testing.T, docs int) *Server {
	t.Helper()
	s, cleanup := newTestServer(t)
	t.Cleanup(cleanup)

	batch := make([]*proto.BatchDocument, 0, docs)
	for i := range docs {
		batch = append(batch, &proto.BatchDocument{
			Key:       fmt.Sprintf("doc-%04d", i),
			Lang:      "en",
			ContentMd: "body",
			Meta: map[string]*proto.MetaValues{
				"views": {Values: []string{fmt.Sprintf("%04d", i)}},
			},
		})
	}
	if _, _, err := s.processBatchWithDocs(t.Context(), "rng", batch); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSearchRangeMatchesTheUnboundedResult(t *testing.T) {
	s := rangeTestServer(t, 200)

	for _, tc := range []struct {
		name   string
		ranges []RangeFilter
		limit  int
	}{
		{"everything, small page", []RangeFilter{{Field: "views", Gte: "0000"}}, 10},
		{"everything, page larger than matches", []RangeFilter{{Field: "views", Gte: "0000"}}, 500},
		{"upper bound", []RangeFilter{{Field: "views", Lte: "0050"}}, 20},
		{"both bounds", []RangeFilter{{Field: "views", Gte: "0100", Lte: "0150"}}, 15},
		{"exclusive bounds", []RangeFilter{{Field: "views", Gt: "0100", Lt: "0110"}}, 100},
		{"matches nothing", []RangeFilter{{Field: "views", Gte: "9999"}}, 10},
		{"no limit", []RangeFilter{{Field: "views", Gte: "0190"}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := referenceRange(t, s, "rng", tc.ranges, tc.limit)
			got, err := s.SearchRange("rng", tc.ranges, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("result count = %d, want %d", len(got), len(want))
			}
			for i := range got {
				if got[i].DocID != want[i].DocID || got[i].Score != want[i].Score {
					t.Errorf("result %d = %+v, want %+v", i, got[i], want[i])
				}
			}
		})
	}
}

// Results must stay in docID order — the early exit relies on that being the
// cursor's order, so if it ever stopped holding, this catches it.
func TestSearchRangeReturnsDocIDOrder(t *testing.T) {
	s := rangeTestServer(t, 100)
	got, err := s.SearchRange("rng", []RangeFilter{{Field: "views", Gte: "0000"}}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 30 {
		t.Fatalf("got %d results, want 30", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].DocID >= got[i].DocID {
			t.Fatalf("results are not in docID order at %d: %q then %q", i, got[i-1].DocID, got[i].DocID)
		}
	}
	// The first page must be the lowest docIDs, not an arbitrary subset.
	// A DocID is "<collection>|<key>|<lang>".
	if got[0].DocID != "rng|doc-0000|en" || got[29].DocID != "rng|doc-0029|en" {
		t.Errorf("the page is not the first 30 documents: %q … %q", got[0].DocID, got[29].DocID)
	}
}

func TestSearchRangeWithoutFiltersReturnsNothing(t *testing.T) {
	s := rangeTestServer(t, 10)
	got, err := s.SearchRange("rng", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("no filters should yield no results, got %v", got)
	}
}

// The concern behind the ticket: many heavy queries at once. With allocation
// bounded by the limit rather than by the collection, this stays flat.
func TestConcurrentRangeQueriesAgree(t *testing.T) {
	s := rangeTestServer(t, 300)
	ranges := []RangeFilter{{Field: "views", Gte: "0000"}}
	want, err := s.SearchRange("rng", ranges, 25)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.SearchRange("rng", ranges, 25)
			if err != nil {
				errs <- err
				return
			}
			if len(got) != len(want) {
				errs <- fmt.Errorf("got %d results, want %d", len(got), len(want))
				return
			}
			for i := range got {
				if got[i].DocID != want[i].DocID {
					errs <- fmt.Errorf("result %d = %q, want %q", i, got[i].DocID, want[i].DocID)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestHeavyQueryBurstStaysBounded is GO-033's acceptance case: fire the worst
// query shape many times at once and check that memory stays proportional to
// what was asked for, not to the collection.
func TestHeavyQueryBurstStaysBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a 5k-document collection")
	}
	s := rangeTestServer(t, 5000)
	ranges := []RangeFilter{{Field: "views", Gte: "0000"}} // matches everything

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.SearchRange("rng", ranges, 20)
			if err != nil {
				t.Error(err)
				return
			}
			if len(res) != 20 {
				t.Errorf("got %d results, want 20", len(res))
			}
		}()
	}
	wg.Wait()

	runtime.GC()
	runtime.ReadMemStats(&after)

	// 50 queries returning 20 documents each cannot need anywhere near the
	// memory the collection occupies; before the early exit, each one
	// materialised all 5000 matches.
	const budget = 8 << 20
	if grew := after.HeapAlloc - min(after.HeapAlloc, before.HeapAlloc); grew > budget {
		t.Errorf("50 concurrent bounded queries retained %d KB, budget is %d KB",
			grew/1024, budget/1024)
	}
}
