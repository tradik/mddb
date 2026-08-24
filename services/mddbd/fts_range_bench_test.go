package main

import (
	"fmt"
	"testing"

	proto "mddb/proto"
)

// GO-033 baseline. SearchRange scans a collection and, before this was
// changed, collected every matching document into a map, materialised all of
// them into a slice, sorted the lot and only then applied the limit — so peak
// allocation was O(matches) for a request that returns O(limit). Run N of
// those concurrently and the process has nowhere to go.

// seedRangeCollection fills a collection with documents carrying an
// increasing "views" metadata value, so a range filter can match a chosen
// share of them.
func seedRangeCollection(tb testing.TB, s *Server, collection string, n int) {
	tb.Helper()
	docs := make([]*proto.BatchDocument, 0, 500)
	flush := func() {
		if len(docs) == 0 {
			return
		}
		if _, _, err := s.processBatchWithDocs(tb.Context(), collection, docs); err != nil {
			tb.Fatal(err)
		}
		docs = docs[:0]
	}
	for i := range n {
		docs = append(docs, &proto.BatchDocument{
			Key:       fmt.Sprintf("doc-%06d", i),
			Lang:      "en",
			ContentMd: "body",
			Meta: map[string]*proto.MetaValues{
				"views": {Values: []string{fmt.Sprintf("%06d", i)}},
			},
		})
		if len(docs) == 500 {
			flush()
		}
	}
	flush()
}

func benchmarkRange(b *testing.B, total, limit int) {
	s, cleanup := newBenchServer(b)
	defer cleanup()
	seedRangeCollection(b, s, "bench", total)

	// Matches every document, so the scan cannot stop early on the filter —
	// only on the limit.
	ranges := []RangeFilter{{Field: "views", Gte: "000000"}}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := s.SearchRange("bench", ranges, limit)
		if err != nil {
			b.Fatal(err)
		}
		if len(res) != limit {
			b.Fatalf("expected %d results, got %d", limit, len(res))
		}
	}
}

// The shape that matters: a large collection, a small page of results.
func BenchmarkSearchRange10kLimit50(b *testing.B)  { benchmarkRange(b, 10_000, 50) }
func BenchmarkSearchRange10kLimit500(b *testing.B) { benchmarkRange(b, 10_000, 500) }
