package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	proto "mddb/proto"
)

// RAG-009 on every surface, and summaryMode off as a golden: the same bytes as
// a server that never heard of summaries.

// offResponses runs one REST vector, hybrid and bm25f search and returns the
// raw response bodies.
func offResponses(t *testing.T, s *Server) []string {
	t.Helper()
	var out []string
	for _, c := range []struct {
		h   http.HandlerFunc
		req map[string]any
	}{
		{s.handleVectorSearch, map[string]any{"collection": "sum", "query": "pump seal main hazards overview", "topK": 4, "threshold": 0.01}},
		{s.handleVectorSearch, map[string]any{"collection": "sum", "query": "pump seal main hazards overview", "retrievalMode": "chunk", "format": "passages", "threshold": 0.01}},
		{s.handleHybridSearch, map[string]any{"collection": "sum", "query": "hazards seal", "algorithm": "bm25f", "threshold": 0.01}},
		{s.handleFTS, map[string]any{"collection": "sum", "query": "hazards seal", "algorithm": "bm25f"}},
	} {
		rec := doRequest(t, c.h, c.req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		out = append(out, rec.Body.String())
	}
	return out
}

func TestSummary_OffIsGolden(t *testing.T) {
	t.Setenv("MDDB_SEARCH_STATS", "false")
	s, _, cleanup := summaryServer(t, nil)
	defer cleanup()
	noProfile := offResponses(t, s)
	setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: SummaryModeOff, SummaryWeight: floatPtr(9)})
	off := offResponses(t, s)
	// Today's server: the summary vectors do not exist at all.
	s.summaries().reset()
	today := offResponses(t, s)
	for i := range today {
		if noProfile[i] != today[i] || off[i] != today[i] {
			t.Fatalf("search %d differs:\nno profile %s\noff        %s\ntoday      %s", i, noProfile[i], off[i], today[i])
		}
	}
	if strings.Contains(strings.Join(today, ""), "meta.summary") {
		t.Fatal("off weighed the summary field")
	}
	// The golden is not vacuous: with summaries on, every one of these differs.
	s.loadVectorIndex()
	setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: SummaryModeBlend})
	for i, got := range offResponses(t, s) {
		if got == today[i] {
			t.Fatalf("search %d is the same with summaries on: %s", i, got)
		}
	}
}

// The keyword side: meta.summary joins BM25F with the profile's weight while
// summaries are on; a request naming the field keeps its own weight.
func TestSummary_BM25FField(t *testing.T) {
	s, _, cleanup := summaryServer(t, &RetrievalProfileDef{SummaryMode: SummaryModeBlend, SummaryWeight: floatPtr(4)})
	defer cleanup()
	fts := func(req map[string]any) map[string]any {
		req["collection"], req["query"], req["algorithm"] = "sum", "hazards", "bm25f"
		rec := doRequest(t, s.handleFTS, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		return body(t, rec.Body.Bytes())
	}
	m := fts(map[string]any{})
	if keys := sumResultKeys(m); len(keys) != 1 || keys[0] != "plant" {
		t.Fatalf("blend: %v", keys)
	}
	if w := m["fieldWeights"].(map[string]any); w["meta.summary"] != 4.0 || w["content"] != 1.0 {
		t.Fatalf("weights: %v", w)
	}
	if keys := sumResultKeys(fts(map[string]any{"fieldWeights": map[string]any{"content": 1.0, "meta.summary": 0.0}})); len(keys) != 0 {
		t.Fatalf("an explicit weight of 0: %v", keys)
	}
	if w := fts(map[string]any{"fieldWeights": map[string]any{"content": 1.0}})["fieldWeights"].(map[string]any); w["meta.summary"] != 4.0 || len(w) != 2 {
		t.Fatalf("request weights plus the summary: %v", w)
	}
	setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: SummaryModeBlend})
	if w := fts(map[string]any{})["fieldWeights"].(map[string]any); w["meta.summary"] != defaultSummaryWeight {
		t.Fatalf("default weight: %v", w)
	}
	// Hybrid: the full-text half weighs it too, fuzzy or not.
	for _, fuzzy := range []int{0, 1} {
		rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "sum", "query": "hazards", "algorithm": "bm25f", "fuzzy": fuzzy, "alpha": 0.0})
		if keys := sumResultKeys(body(t, rec.Body.Bytes())); len(keys) == 0 || keys[0] != "plant" {
			t.Fatalf("hybrid fuzzy %d: %s", fuzzy, rec.Body.String())
		}
	}
}

// Hybrid: blend ranks the summary hit; expand's passages are its chunks.
func TestSummary_HybridREST(t *testing.T) {
	s, _, cleanup := summaryServer(t, &RetrievalProfileDef{SummaryMode: SummaryModeExpand})
	defer cleanup()
	rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "sum", "query": generalQuery, "alpha": 1.0, "topK": 1})
	if keys := sumResultKeys(body(t, rec.Body.Bytes())); len(keys) != 1 || keys[0] != "plant" {
		t.Fatalf("hybrid: %s", rec.Body.String())
	}
	rec = doRequest(t, s.handleHybridSearch, map[string]any{"collection": "sum", "query": generalQuery, "alpha": 1.0, "topK": 1, "format": "passages"})
	if keys := sumPassageKeys(body(t, rec.Body.Bytes())); strings.Join(keys, ",") != "plant,plant,plant" {
		t.Fatalf("hybrid passages: %s", rec.Body.String())
	}
}

func TestSummary_MCPAndGRPC(t *testing.T) {
	s, _, cleanup := summaryServer(t, nil)
	defer cleanup()
	ctx := context.Background()
	g := &GRPCServer{server: s}
	tools := &MCPToolServer{client: NewDirectClient(s)}
	setMode := func(mode string) {
		t.Helper()
		if _, err := g.SetCollectionConfig(ctx, &proto.SetCollectionConfigRequest{Collection: "sum",
			Retrieval: &proto.RetrievalProfileProto{SummaryMode: mode}}); err != nil {
			t.Fatal(err)
		}
		if got, _ := g.GetCollectionConfig(ctx, &proto.GetCollectionConfigRequest{Collection: "sum"}); got.Config.Retrieval.SummaryMode != mode {
			t.Fatalf("round trip: %+v", got.Config.Retrieval)
		}
	}
	for _, mode := range []string{SummaryModeOff, SummaryModeBlend, SummaryModeExpand} {
		setMode(mode)
		want := mode != SummaryModeOff

		out, err := tools.mcpCallTool(ctx, "semantic_search", map[string]any{"collection": "sum", "query": generalQuery, "threshold": 0.3})
		if err != nil || strings.Contains(out, `"plant"`) != want {
			t.Fatalf("mcp %s: %v %s", mode, err, out)
		}
		vr, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "sum", Query: generalQuery, Threshold: 0.3})
		if err != nil || (len(vr.Results) == 1 && vr.Results[0].Document.Key == "plant") != want {
			t.Fatalf("grpc %s: %v %+v", mode, err, vr)
		}
		pr, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "sum", Query: generalQuery, Threshold: 0.3, Format: "passages"})
		wantPassages := map[string]int{SummaryModeOff: 0, SummaryModeBlend: 1, SummaryModeExpand: maxSummaryExpandChunks}[mode]
		if err != nil || len(pr.Passages) != wantPassages {
			t.Fatalf("grpc passages %s: %v %d", mode, err, len(pr.Passages))
		}
		out, err = tools.mcpCallTool(ctx, "semantic_search", map[string]any{"collection": "sum", "query": generalQuery, "threshold": 0.3, "format": "passages"})
		if err != nil || strings.Count(out, `"key": "plant"`) != wantPassages {
			t.Fatalf("mcp passages %s: %v %s", mode, err, out)
		}
		hp, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "sum", Query: generalQuery, TopK: 1, Format: "passages"})
		if mode == SummaryModeExpand && (err != nil || len(hp.Passages) != maxSummaryExpandChunks) {
			t.Fatalf("grpc hybrid passages: %v %+v", err, hp)
		}
	}
	if _, err := g.SetCollectionConfig(ctx, &proto.SetCollectionConfigRequest{Collection: "sum",
		Retrieval: &proto.RetrievalProfileProto{SummaryMode: "sometimes"}}); err == nil {
		t.Fatal("an unknown summary mode was stored")
	}
}
