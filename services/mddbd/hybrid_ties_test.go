package main

import (
	"fmt"
	"strings"
	"testing"

	"mddb/internal/fts"
	"mddb/internal/vector"
)

// Fused results with equal scores come back in one order, the same on every
// call, and a topK cut inside a tie keeps the same documents.
func TestFusionBreaksTiesByDocumentID(t *testing.T) {
	// Disjoint lists: f<i> is rank i in full-text only, v<i> rank i in vector
	// only, so under RRF f<i> and v<i> tie; under alpha every f ties, as
	// equal full-text scores normalise to the same value.
	var ftsHits []fts.FTSResult
	var vecHits []vector.VectorResult
	for i := 0; i < 10; i++ {
		ftsHits = append(ftsHits, fts.FTSResult{DocID: fmt.Sprintf("f%d", i), Score: 1})
		vecHits = append(vecHits, vector.VectorResult{DocID: fmt.Sprintf("v%d", i), Score: 0.5})
	}
	cases := []struct {
		name  string
		merge func() []HybridSearchResultItem
		want  string
	}{
		{"rrf", func() []HybridSearchResultItem { return mergeRRF(ftsHits, vecHits, 60, 5) }, "f0 v0 f1 v1 f2"},
		{"alpha", func() []HybridSearchResultItem { return mergeAlpha(ftsHits, vecHits, 0.5, 5) }, "f0 f1 f2 f3 f4"},
	}
	ids := func(rs []HybridSearchResultItem) string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.Document.ID
		}
		return strings.Join(out, " ")
	}
	for _, c := range cases {
		for run := 0; run < 50; run++ {
			if got := ids(c.merge()); got != c.want {
				t.Fatalf("%s run %d: %s, want %s", c.name, run, got, c.want)
			}
		}
	}
}
