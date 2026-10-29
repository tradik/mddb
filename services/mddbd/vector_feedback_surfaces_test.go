package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	proto "mddb/proto"
)

// SRCH-015 on every surface: hybrid (vector half only), cross, MCP, gRPC.

func hybridVectorScore(t *testing.T, s *Server, extra map[string]any, key string) float64 {
	t.Helper()
	req := map[string]any{"collection": "fb", "query": "release", "topK": 30, "strategy": "alpha"}
	for k, v := range extra {
		req[k] = v
	}
	rec := doRequest(t, s.handleHybridSearch, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	for _, r := range body(t, rec.Body.Bytes())["results"].([]any) {
		m := r.(map[string]any)
		if m["document"].(map[string]any)["key"] == key {
			return m["vectorScore"].(float64)
		}
	}
	return -1
}

func TestFeedbackHybrid_VectorHalfOnly(t *testing.T) {
	s, _, cleanup := feedbackServer(t)
	defer cleanup()
	before := hybridVectorScore(t, s, nil, "f00")
	for _, mode := range []string{"query", "rescore"} {
		after := hybridVectorScore(t, s, map[string]any{"negative": []any{"f00"}, "feedbackMode": mode}, "f00")
		if after < 0 || after >= before {
			t.Fatalf("%s: f00's vector score %v → %v", mode, before, after)
		}
	}
	rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "fb", "query": "release", "explain": true, "positive": []any{"f20"}})
	if planOf(t, body(t, rec.Body.Bytes()))["feedback"] == nil {
		t.Fatalf("hybrid explain: %s", rec.Body.String())
	}
	if rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "fb", "query": "x", "positive": []any{"nope"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("hybrid unknown key: %d", rec.Code)
	}
	if rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "fb", "query": "x", "feedbackMode": "nope", "positive": []any{"f01"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("hybrid bad mode: %d", rec.Code)
	}
	resp, err := NewDirectClient(s).HybridSearch(context.Background(), &MCPHybridSearchRequest{Collection: "fb", Query: "x",
		PlanOptions: PlanOptions{Explain: true}, FeedbackOptions: FeedbackOptions{Negative: []FeedbackExample{{Key: "f00"}}}})
	if err != nil || resp.Plan == nil || resp.Plan.Feedback == nil {
		t.Fatalf("mcp hybrid: %v %+v", err, resp)
	}
	if _, err := NewDirectClient(s).HybridSearch(context.Background(), &MCPHybridSearchRequest{Collection: "fb", Query: "x",
		FeedbackOptions: FeedbackOptions{FeedbackMode: "nope", Positive: []FeedbackExample{{Key: "f00"}}}}); err == nil {
		t.Fatal("mcp hybrid: bad mode accepted")
	}
}

func TestFeedbackCross_KeysFromTheTargets(t *testing.T) {
	s, _, cleanup := feedbackServer(t)
	defer cleanup()
	first := func(raw []byte) string {
		return body(t, raw)["results"].([]any)[0].(map[string]any)["document"].(map[string]any)["key"].(string)
	}
	for _, mode := range []string{"query", "rescore"} {
		rec := doRequest(t, s.handleCrossSearch, map[string]any{"query": "x", "targetCollections": []string{"fb"},
			"topK": 5, "oversample": 5.0, "positive": []any{"f20"}, "feedbackMode": mode, "explain": true})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"f20"`) || planOf(t, body(t, rec.Body.Bytes()))["feedback"] == nil {
			t.Fatalf("%s: %d %s", mode, rec.Code, rec.Body.String())
		}
	}
	rec := doRequest(t, s.handleCrossSearch, map[string]any{"query": "x", "targetCollections": []string{"fb"}, "sourceCollection": "fb",
		"negative": []any{"f00"}})
	if first(rec.Body.Bytes()) == "f00" {
		t.Fatalf("cross negative: %s", rec.Body.String())
	}
	if rec := doRequest(t, s.handleCrossSearch, map[string]any{"query": "x", "targetCollections": []string{"fb"}, "positive": []any{"nope"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross unknown key: %d", rec.Code)
	}
	if rec := doRequest(t, s.handleCrossSearch, map[string]any{"query": "x", "targetCollections": []string{"fb"}, "feedbackWeight": -1, "positive": []any{"f01"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross bad weight: %d", rec.Code)
	}
	direct := NewDirectClient(s)
	cr, err := direct.CrossSearch(context.Background(), &MCPCrossSearchRequest{Query: "x", TargetCollections: []string{"fb"},
		FeedbackOptions: FeedbackOptions{Negative: []FeedbackExample{{Key: "f00"}}, FeedbackMode: "rescore"}})
	if err != nil || cr.Results[0].Document.Key == "f00" {
		t.Fatalf("mcp cross: %v %+v", err, cr)
	}
	if _, err := direct.CrossSearch(context.Background(), &MCPCrossSearchRequest{Query: "x", TargetCollections: []string{"fb"},
		FeedbackOptions: FeedbackOptions{Positive: []FeedbackExample{{Key: "nope"}}}}); err == nil {
		t.Fatal("mcp cross: unknown key accepted")
	}
	if _, err := direct.CrossSearch(context.Background(), &MCPCrossSearchRequest{Query: "x", TargetCollections: []string{"fb"},
		FeedbackOptions: FeedbackOptions{FeedbackMode: "x", Positive: []FeedbackExample{{Key: "f00"}}}}); err == nil {
		t.Fatal("mcp cross: bad mode accepted")
	}
}

func TestFeedbackMCPAndGRPC(t *testing.T) {
	s, _, cleanup := feedbackServer(t)
	defer cleanup()
	ctx := context.Background()
	tools := &MCPToolServer{client: NewDirectClient(s)}
	for tool, args := range map[string]map[string]any{
		"semantic_search": {"collection": "fb", "query": "x", "top_k": 5.0, "positive": []any{"f20"}, "explain": true},
		"hybrid_search":   {"collection": "fb", "query": "release", "negative": []any{"f00"}, "feedback_mode": "rescore", "explain": true},
		"cross_search":    {"query": "x", "target_collections": []any{"fb"}, "positive": []any{"f20"}, "rocchio": map[string]any{"beta": 2.0}, "explain": true},
	} {
		out, err := tools.mcpCallTool(ctx, tool, args)
		if err != nil || !strings.Contains(out, `"feedback"`) {
			t.Fatalf("%s: %v %s", tool, err, out)
		}
		args["positive"] = []any{true}
		if _, err := tools.mcpCallTool(ctx, tool, args); err == nil {
			t.Fatalf("%s: a boolean example was accepted", tool)
		}
	}
	direct := NewDirectClient(s)
	if _, err := direct.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "fb", Query: "x",
		FeedbackOptions: FeedbackOptions{Positive: []FeedbackExample{{Key: "nope"}}}}); err == nil {
		t.Fatal("mcp vector: unknown key accepted")
	}
	if _, err := direct.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "fb", Query: "x",
		FeedbackOptions: FeedbackOptions{FeedbackMode: "x", Positive: []FeedbackExample{{Key: "f00"}}}}); err == nil {
		t.Fatal("mcp vector: bad mode accepted")
	}

	g := &GRPCServer{server: s}
	beta := 1.0
	vr, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "fb", Query: "x", TopK: 5, Explain: true,
		Positive: []*proto.FeedbackExample{{Key: "f20"}}, Rocchio: &proto.RocchioWeights{Beta: &beta}})
	if err != nil || vr.Plan.GetFeedback().GetBeta() != 1 || vr.Results[0].Document.Key != "f20" {
		t.Fatalf("grpc vector: %v %+v", err, vr)
	}
	hr, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "fb", Query: "release", Explain: true,
		Negative: []*proto.FeedbackExample{{Vector: corpusVector(0)}}, FeedbackMode: "rescore", FeedbackWeight: 1})
	if err != nil || hr.Plan.GetFeedback().GetWeight() != 1 {
		t.Fatalf("grpc hybrid: %v %+v", err, hr)
	}
	cr, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{Query: "x", TargetCollections: []string{"fb"}, Explain: true,
		Negative: []*proto.FeedbackExample{{Key: "f00"}}})
	if err != nil || cr.Plan.GetFeedback().GetNegative() != 1 {
		t.Fatalf("grpc cross: %v %+v", err, cr)
	}
}
