package main

import (
	"context"
	"strings"
	"testing"
)

// Every search surface feeds the gain counters once per search, and a
// measurement (SRCH-019 eval) feeds them nothing.
func TestGain_EverySearchPathCounts(t *testing.T) {
	s, cleanup := gainServer(t)
	defer cleanup()
	s.VectorIndex.SetReady()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	s.startEmbeddingWorker(s.Embedding)
	defer s.EmbeddingWorker.Stop()
	long := strings.Repeat("filler line about nothing in particular\n", 400) + "the backup snapshot lives here\n"
	doc := addTestDoc(t, s, "kb", "backup.md", "en", long, nil)
	waitForEmbedding(t, s, "kb", doc.ID)

	snapshot := func() GainCounters {
		r, err := s.Gain.Report(1, false)
		if err != nil {
			t.Fatal(err)
		}
		return r.Totals
	}
	expect := func(name, typ string, delta int64, fragment bool, run func()) {
		t.Helper()
		before := snapshot()
		run()
		after := snapshot()
		if got := after.Searches[typ] - before.Searches[typ]; got != delta {
			t.Fatalf("%s: %d %s searches counted, want %d", name, got, typ, delta)
		}
		if fragment && after.TokensAvoided <= before.TokensAvoided {
			t.Fatalf("%s: a fragment must count avoided tokens", name)
		}
		if after.TokensReturned <= before.TokensReturned && delta > 0 {
			t.Fatalf("%s: nothing returned was counted", name)
		}
	}
	ctx := context.Background()
	direct := NewDirectClient(s)

	expect("REST fts", "fts", 1, false, func() {
		doRequest(t, s.handleFTS, map[string]any{"collection": "kb", "query": "backup snapshot"})
	})
	expect("REST fts passages", "fts", 1, true, func() {
		doRequest(t, s.handleFTS, map[string]any{"collection": "kb", "query": "backup snapshot", "format": "passages"})
	})
	expect("MCP fts", "fts", 1, false, func() {
		if _, err := direct.FTSSearch(ctx, &MCPFTSSearchRequest{Collection: "kb", Query: "backup", IncludeContent: true}); err != nil {
			t.Fatal(err)
		}
	})
	expect("REST vector chunk", "vector", 1, true, func() {
		if rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "kb", "query": "backup", "retrievalMode": "chunk"}); rec.Code != 200 {
			t.Fatalf("vector: %d %s", rec.Code, rec.Body.String())
		}
	})
	expect("MCP vector chunk", "vector", 1, true, func() {
		if _, err := direct.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "kb", Query: "backup", RetrievalMode: "chunk"}); err != nil {
			t.Fatal(err)
		}
	})
	expect("REST hybrid", "hybrid", 1, false, func() {
		doRequest(t, s.handleHybridSearch, map[string]any{"collection": "kb", "query": "backup", "includeContent": true})
	})
	expect("MCP hybrid", "hybrid", 1, false, func() {
		if _, err := direct.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "kb", Query: "backup"}); err != nil {
			t.Fatal(err)
		}
	})
	expect("REST cross text", "cross", 1, false, func() {
		doRequest(t, s.handleCrossSearch, map[string]any{"query": "backup", "targetCollections": []string{"kb"},
			"searchType": "fts", "includeContent": true})
	})
	expect("REST cross vector", "cross", 1, false, func() {
		doRequest(t, s.handleCrossSearch, map[string]any{"query": "backup", "targetCollections": []string{"kb"}, "includeContent": true})
	})
	expect("MCP cross", "cross", 2, false, func() {
		for _, st := range []string{"", "fts"} {
			if _, err := direct.CrossSearch(ctx, &MCPCrossSearchRequest{Query: "backup", TargetCollections: []string{"kb"},
				SearchType: st, IncludeContent: true}); err != nil {
				t.Fatal(err)
			}
		}
	})
	before := snapshot()
	if _, err := s.RunEval(ctx, EvalRequest{Collection: "kb", Queries: []EvalQuery{{Query: "backup", ExpectedKeys: []string{"backup.md"}}}}); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after.TokensReturned != before.TokensReturned || len(after.Searches) != len(before.Searches) ||
		after.Searches["vector"] != before.Searches["vector"] || after.Searches["fts"] != before.Searches["fts"] {
		t.Fatalf("eval must not count: %+v -> %+v", before, after)
	}
}
