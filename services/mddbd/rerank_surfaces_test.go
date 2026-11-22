package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	gql "mddb/graphql"
	proto "mddb/proto"
)

// RAG-011 on gRPC, MCP and GraphQL: the same stage, reached through each
// surface's own request shape, reporting in each surface's own response.

func protoRerank(explain bool) *proto.RerankOptions {
	on := true
	return &proto.RerankOptions{Enabled: &on, Explain: explain}
}

func TestRerank_GRPC(t *testing.T) {
	s, f := rerankServer(t)
	ctx := context.Background()
	g := &GRPCServer{server: s}
	vr, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "rr", Query: rerankQuery, TopK: 3, Rerank: protoRerank(true)})
	if err != nil || vr.Results[0].Document.Key != "gamma" || vr.Rerank.Model != "fake-rerank" || len(vr.Rerank.Moves) == 0 {
		t.Fatalf("vector: %v %+v", err, vr)
	}
	hr, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "rr", Query: rerankQuery, TopK: 3, Rerank: protoRerank(false)})
	if err != nil || hr.Results[0].Document.Key != "gamma" || hr.Rerank == nil {
		t.Fatalf("hybrid: %v %+v", err, hr)
	}
	fr, err := g.FTS(ctx, &proto.FTSRequest{Collection: "rr", Query: rerankQuery, Limit: 3, Algorithm: "bm25", Rerank: protoRerank(false)})
	if err != nil || fr.Results[0].Document.Key != "gamma" || fr.Rerank == nil {
		t.Fatalf("fts: %v %+v", err, fr)
	}
	cr, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{TargetCollections: []string{"rr"}, Query: rerankQuery, TopK: 3,
		Rerank: protoRerank(true)})
	if err != nil || cr.Results[0].Document.Key != "gamma" || cr.Rerank.Moves[0].Collection != "rr" {
		t.Fatalf("cross: %v %+v", err, cr)
	}
	cp, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{TargetCollections: []string{"rr"}, Query: rerankQuery, TopK: 3,
		Format: "passages", Rerank: protoRerank(false)})
	if err != nil || cp.Passages[0].Key != "gamma" || cp.Rerank == nil {
		t.Fatalf("cross passages: %v %+v", err, cp)
	}
	// A failing model: the search answers, with the reason.
	f.code.Store(http.StatusBadGateway)
	fr, err = g.FTS(ctx, &proto.FTSRequest{Collection: "rr", Query: rerankQuery, Limit: 3, Rerank: protoRerank(false)})
	if err != nil || fr.RerankSkipped != "error" || len(fr.Results) != 3 {
		t.Fatalf("skipped: %v %+v", err, fr)
	}
	// The profile's default round-trips.
	if _, err := g.SetCollectionConfig(ctx, &proto.SetCollectionConfigRequest{Collection: "rr",
		Retrieval: &proto.RetrievalProfileProto{Rerank: &proto.RerankOptions{Enabled: protoRerank(false).Enabled, Candidates: 7, TopN: 2}}}); err != nil {
		t.Fatal(err)
	}
	got, _ := g.GetCollectionConfig(ctx, &proto.GetCollectionConfigRequest{Collection: "rr"})
	if r := got.Config.Retrieval.Rerank; r == nil || !r.GetEnabled() || r.Candidates != 7 || r.TopN != 2 {
		t.Fatalf("profile round trip: %+v", got.Config.Retrieval)
	}
	if _, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "rr", Query: rerankQuery,
		Rerank: &proto.RerankOptions{Candidates: -1}}); err == nil {
		t.Fatal("invalid candidates accepted")
	}
}

func TestRerank_MCP(t *testing.T) {
	s, _ := rerankServer(t)
	ctx := context.Background()
	tools := &MCPToolServer{client: NewDirectClient(s)}
	on := map[string]any{"enabled": true, "explain": true}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"semantic_search", map[string]any{"collection": "rr", "query": rerankQuery, "top_k": 3}},
		{"hybrid_search", map[string]any{"collection": "rr", "query": rerankQuery, "top_k": 3}},
		{"full_text_search", map[string]any{"collection": "rr", "query": rerankQuery, "limit": 3, "include_content": true}},
		{"cross_search", map[string]any{"target_collections": []any{"rr"}, "query": rerankQuery, "top_k": 3}},
		{"cross_search", map[string]any{"target_collections": []any{"rr"}, "query": rerankQuery, "top_k": 3, "search_type": "fts"}},
	} {
		c.args["rerank"] = on
		out, err := tools.mcpCallTool(ctx, c.tool, c.args)
		if err != nil || !strings.Contains(out, `"model":"fake-rerank"`) || !strings.Contains(out, `"moves"`) {
			t.Fatalf("%s: %v %s", c.tool, err, out)
		}
		if strings.Index(out, `"gamma"`) > strings.Index(out, `"alpha"`) && strings.Contains(out, `"alpha"`) {
			t.Fatalf("%s: gamma not first: %s", c.tool, out)
		}
		c.args["format"] = "passages"
		out, err = tools.mcpCallTool(ctx, c.tool, c.args)
		if err != nil || !strings.Contains(out, `"rerank"`) {
			t.Fatalf("%s passages: %v %s", c.tool, err, out)
		}
		c.args["rerank"] = "yes please"
		if _, err := tools.mcpCallTool(ctx, c.tool, c.args); err == nil {
			t.Fatalf("%s accepted a rerank that is not an object", c.tool)
		}
	}
	if r, err := mcpGetRerank(map[string]any{}); err != nil || r.Rerank != nil {
		t.Fatalf("absent: %+v %v", r, err)
	}
}

func TestRerank_GraphQL(t *testing.T) {
	s, f := rerankServer(t)
	a := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	ctx := context.Background()
	on, explain, three := true, true, 3
	in := &gql.RerankInput{Enabled: &on, Explain: &explain, TopN: &three}
	q := rerankQuery
	vr, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "rr", Query: &q, TopK: &three, Rerank: in})
	if err != nil || vr.Results[0].Document.Key != "gamma" || vr.Rerank.Model != "fake-rerank" || len(vr.Rerank.Moves) == 0 {
		t.Fatalf("vector: %v %+v", err, vr)
	}
	fr, err := a.FullTextSearch(ctx, gql.FTSInput{Collection: "rr", Query: rerankQuery, Limit: &three, Rerank: in})
	if err != nil || fr.Results[0].Document.Key != "gamma" || fr.Rerank == nil || fr.RerankSkipped != nil {
		t.Fatalf("fts: %v %+v", err, fr)
	}
	passages := "passages"
	pr, err := a.FullTextSearch(ctx, gql.FTSInput{Collection: "rr", Query: rerankQuery, Format: &passages, Rerank: in})
	if err != nil || pr.Passages[0].Key != "gamma" || pr.Rerank == nil {
		t.Fatalf("fts passages: %v %+v", err, pr)
	}
	vp, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "rr", Query: &q, Format: &passages, Rerank: in})
	if err != nil || vp.Passages[0].Key != "gamma" || vp.Rerank == nil {
		t.Fatalf("vector passages: %v %+v", err, vp)
	}
	f.body.Store("garbage")
	fr, err = a.FullTextSearch(ctx, gql.FTSInput{Collection: "rr", Query: rerankQuery, Rerank: in})
	if err != nil || fr.RerankSkipped == nil || *fr.RerankSkipped != "invalidResponse" || fr.Rerank != nil {
		t.Fatalf("skipped: %v %+v", err, fr)
	}
	if r := rerankFromGQL(nil); r.Rerank != nil {
		t.Fatal("nil input")
	}
	if rep, reason := rerankToGQL(RerankResult{}); rep != nil || reason != nil {
		t.Fatal("empty result")
	}
}

func TestRerank_SurfaceEdges(t *testing.T) {
	s, _ := rerankServer(t)
	ctx := context.Background()
	if _, err := NewDirectClient(s).HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "rr", Query: rerankQuery,
		RerankRequest: RerankRequest{Rerank: &RerankOptions{TopN: -1}}}); err == nil {
		t.Fatal("hybrid accepted an invalid rerank")
	}
	rep, _ := rerankToGQL(RerankResult{Rerank: &RerankReport{Moves: []RerankMove{{ID: "a", Collection: "rr"}, {ID: "b"}}}})
	if rep.Moves[0].Collection == nil || *rep.Moves[0].Collection != "rr" || rep.Moves[1].Collection != nil {
		t.Fatalf("moves: %+v", rep.Moves)
	}
	// A target whose hybrid search fails adds nothing to a cross search.
	s.Embedding = &covCFailEmbedding{}
	m := restSearch(t, s.handleCrossSearch, map[string]any{"targetCollections": []string{"rr"}, "query": rerankQuery,
		"searchType": "hybrid", "rerank": rrEnabled()})
	if results, _ := m["results"].([]any); len(results) != 0 {
		t.Fatalf("failing target: %v", m)
	}
}
