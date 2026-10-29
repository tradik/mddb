package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	proto "mddb/proto"
)

// SRCH-018: k1/b per query and per collection profile, on every surface.
func bm25Server(t *testing.T) (*Server, func()) {
	s, cleanup := newHandlerTestServer(t)
	filler := strings.Repeat("other words fill this document with unrelated text ", 20)
	addTestDoc(t, s, "c", "short", "en", "kernel tuning", nil)
	addTestDoc(t, s, "c", "long", "en", "kernel kernel kernel "+filler, nil)
	return s, cleanup
}

func TestBM25_ResolvePrecedence(t *testing.T) {
	s, cleanup := bm25Server(t)
	defer cleanup()
	if p, err := s.ResolveBM25("c", nil, nil); err != nil || p.K1 != 1.2 || p.B != 0.75 {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	k1, b := 2.0, 0.1
	if err := s.CollectionManager.Set("c", &CollectionConfig{Retrieval: &RetrievalProfileDef{BM25K1: &k1}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.ResolveBM25("c", nil, nil); p.K1 != 2 || p.B != 0.75 {
		t.Fatalf("profile k1 only: %+v", p)
	}
	if p, _ := s.ResolveBM25("c", nil, &b); p.K1 != 2 || p.B != 0.1 {
		t.Fatalf("request b over profile: %+v", p)
	}
	bad := 5.0
	if _, err := s.ResolveBM25("c", &bad, nil); err == nil {
		t.Fatal("k1 out of range")
	}
	if err := (&RetrievalProfileDef{BM25B: &bad}).Validate(); err == nil || !strings.Contains(err.Error(), "retrieval.bm25B") {
		t.Fatalf("profile validation: %v", err)
	}
	// The profile travels over gRPC.
	pp := retrievalProfileToProto(&RetrievalProfileDef{BM25K1: &k1, BM25B: &b})
	if back := retrievalProfileFromProto(pp); *back.BM25K1 != 2 || *back.BM25B != 0.1 {
		t.Fatalf("proto round trip: %+v", back)
	}
}

func TestBM25_EverySurface(t *testing.T) {
	s, cleanup := bm25Server(t)
	defer cleanup()
	ctx := context.Background()
	firstREST := func(body map[string]any) string {
		t.Helper()
		rec := doRequest(t, s.handleFTS, body)
		var resp FTSSearchResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || len(resp.Results) == 0 {
			t.Fatalf("fts: %d %s", rec.Code, rec.Body.String())
		}
		return resp.Results[0].Document.Key
	}
	base := map[string]any{"collection": "c", "query": "kernel", "algorithm": "bm25"}
	if got := firstREST(base); got != "short" {
		t.Fatalf("default order: %s", got)
	}
	base["bm25B"] = 0.0
	if got := firstREST(base); got != "long" {
		t.Fatalf("b=0 over REST: %s", got)
	}
	if rec := doRequest(t, s.handleFTS, map[string]any{"collection": "c", "query": "kernel", "bm25K1": 9}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid k1: %d", rec.Code)
	}

	// MCP full_text_search.
	tools := &MCPToolServer{client: NewDirectClient(s)}
	out, err := tools.mcpCallTool(ctx, "full_text_search", map[string]interface{}{"collection": "c", "query": "kernel",
		"algorithm": "bm25", "bm25_b": 0.0, "include_content": false})
	if err != nil || strings.Index(out, `"long"`) > strings.Index(out, `"short"`) {
		t.Fatalf("mcp b=0: %v %s", err, out)
	}
	if mcpGetFloatPtr(map[string]interface{}{}, "x") != nil {
		t.Fatal("absent is nil")
	}

	// gRPC FTS.
	g := &GRPCServer{server: s}
	zero := 0.0
	gr, err := g.FTS(ctx, &proto.FTSRequest{Collection: "c", Query: "kernel", Algorithm: "bm25", Bm25B: &zero})
	if err != nil || gr.Results[0].Document.Key != "long" {
		t.Fatalf("grpc b=0: %v %+v", err, gr)
	}

	// Hybrid (full-text half): accepted over REST and refused when invalid.
	if rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "c", "query": "kernel", "bm25B": 0}); rec.Code != http.StatusOK {
		t.Fatalf("hybrid: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "c", "query": "kernel", "bm25B": 2}); rec.Code != http.StatusBadRequest {
		t.Fatalf("hybrid invalid b: %d", rec.Code)
	}
	if _, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "c", Query: "kernel", Bm25B: &zero}); err != nil {
		t.Fatalf("grpc hybrid: %v", err)
	}

	// Evaluation: a configuration may try parameters, and labels say which.
	q := []EvalQuery{{Query: "kernel", ExpectedKeys: []string{"long"}}}
	res, err := s.RunEval(ctx, EvalRequest{Collection: "c", Queries: q, Configs: []EvalConfig{
		{SearchType: SearchTypeFTS, Algorithm: "bm25"}, {SearchType: SearchTypeFTS, Algorithm: "bm25", BM25K1: floatPtr(1.2), BM25B: &zero}}})
	if err != nil || res.Results[1].Config != "fts/bm25 k1=1.2 b=0" || res.Results[1].MRR <= res.Results[0].MRR {
		t.Fatalf("eval: %v %+v", err, res)
	}
	ge, err := g.RunEval(ctx, &proto.RunEvalRequest{Collection: "c", Queries: []*proto.EvalQueryProto{{Query: "kernel", ExpectedKeys: []string{"long"}}},
		Configs: []*proto.EvalConfigProto{{SearchType: "fts", Algorithm: "bm25", Bm25B: &zero}}})
	if err != nil || ge.Results[0].Mrr != 1 {
		t.Fatalf("grpc eval: %v %+v", err, ge)
	}

	// The advisor adopts parameters only when they measure better: here the
	// set rewards the long document, which only less length normalisation finds first.
	if _, err := s.AddEvalQuery("c", EvalQuery{Query: "kernel", ExpectedKeys: []string{"long"}}); err != nil {
		t.Fatal(err)
	}
	rec, err := s.RecommendSearch("c")
	if err != nil || rec.RetrievalProfile.BM25B == nil || *rec.RetrievalProfile.BM25B != 0.3 {
		t.Fatalf("advisor: %v %+v", err, rec.RetrievalProfile)
	}
}
