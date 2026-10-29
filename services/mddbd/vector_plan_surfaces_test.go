package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"mddb/internal/embedding"
	proto "mddb/proto"
)

// wordEmbedding embeds text as a bag of hashed words: documents differ, and a
// query scores highest against the documents sharing its words.
type wordEmbedding struct{ dims int }

func (e wordEmbedding) Embed(_ context.Context, text string, _ embedding.Role) ([]float32, error) {
	v := make([]float32, e.dims)
	v[0] = 0.01
	for _, w := range strings.Fields(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[int(h.Sum32()%32)%e.dims]++
	}
	return v, nil
}

func (e wordEmbedding) EmbedBatch(ctx context.Context, texts []string, role embedding.Role) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i], _ = e.Embed(ctx, t, role)
	}
	return out, nil
}

func (e wordEmbedding) Model() string   { return "words" }
func (e wordEmbedding) Dimensions() int { return e.dims }

// planServer holds 25 embedded documents in "kb", one of them tagged rare.
func planServer(t *testing.T) (*Server, func()) {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	s.VectorIndex.SetReady()
	for _, sr := range s.VectorSearchers {
		sr.SetReady()
	}
	s.Embedding = wordEmbedding{dims: 32}
	s.startEmbeddingWorker(s.Embedding)
	for i := 0; i < 25; i++ {
		meta := map[string][]string{"tag": {"common"}}
		if i == 7 {
			meta["tag"] = []string{"rare"}
		}
		doc := addTestDoc(t, s, "kb", fmt.Sprintf("doc%02d", i), "en",
			fmt.Sprintf("note %d about backup restore topic%d", i, i%5)+strings.Repeat(" filler", i), meta)
		waitForEmbedding(t, s, "kb", doc.ID)
	}
	return s, func() { s.EmbeddingWorker.Stop(); cleanup() }
}

// body decodes a response, dropping the timing that differs between runs and
// ordering matched terms, which full-text search lists in map order.
func body(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	delete(m, "searchStats")
	results, _ := m["results"].([]any)
	for _, r := range results {
		if terms, ok := r.(map[string]any)["matchedTerms"].([]any); ok {
			sort.Slice(terms, func(i, j int) bool { return fmt.Sprint(terms[i]) < fmt.Sprint(terms[j]) })
		}
	}
	return m
}

func planOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	p, ok := m["plan"].(map[string]any)
	if !ok {
		t.Fatalf("no plan in %v", m)
	}
	return p
}

// No algorithm = exactly today's flat search, byte for byte, with no plan.
func TestPlanGolden_NoAlgorithmIsTodaysFlat(t *testing.T) {
	s, cleanup := planServer(t)
	defer cleanup()
	for name, h := range map[string]http.HandlerFunc{"vector": s.handleVectorSearch, "hybrid": s.handleHybridSearch} {
		bare := doRequest(t, h, map[string]any{"collection": "kb", "query": "backup topic3"})
		algo := "algorithm"
		if name == "hybrid" {
			algo = "vectorAlgorithm"
		}
		flat := doRequest(t, h, map[string]any{"collection": "kb", "query": "backup topic3", algo: "flat"})
		a, b := body(t, bare.Body.Bytes()), body(t, flat.Body.Bytes())
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s: no algorithm differs from flat:\n%v\n%v", name, a, b)
		}
		if _, has := a["plan"]; has {
			t.Fatalf("%s: a plan without explain", name)
		}
	}
	bare := doRequest(t, s.handleCrossSearch, map[string]any{"query": "backup topic3", "targetCollections": []string{"kb"}})
	flat := doRequest(t, s.handleCrossSearch, map[string]any{"query": "backup topic3", "targetCollections": []string{"kb"}, "algorithm": "flat"})
	if a, b := body(t, bare.Body.Bytes()), body(t, flat.Body.Bytes()); !reflect.DeepEqual(a, b) || a["plan"] != nil {
		t.Fatalf("cross: %v\n%v", a, b)
	}
}

func TestPlanREST_AutoExplainsItself(t *testing.T) {
	s, cleanup := planServer(t)
	defer cleanup()

	rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "kb", "query": "backup", "algorithm": "auto", "explain": true})
	m := body(t, rec.Body.Bytes())
	if p := planOf(t, m); p["algorithm"] != "flat" || m["algorithm"] != "flat" || p["prefilter"] != false {
		t.Fatalf("auto on 25 vectors: %v", m)
	}
	// One document of 25 vectors is 4%: the filter goes first.
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "kb", "query": "backup", "algorithm": "auto",
		"explain": true, "filterMeta": map[string][]string{"tag": {"rare"}}})
	m = body(t, rec.Body.Bytes())
	if p := planOf(t, m); p["prefilter"] != true || len(m["results"].([]any)) != 1 {
		t.Fatalf("selective filter: %v", m)
	}
	// A filter nothing matches still reports its plan.
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "kb", "query": "backup", "explain": true,
		"filterMeta": map[string][]string{"tag": {"none"}}})
	if p := planOf(t, body(t, rec.Body.Bytes())); !strings.Contains(fmt.Sprint(p["reasons"]), "the default") {
		t.Fatalf("empty filter: %v", p)
	}
	if rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "kb", "query": "x", "latencyTargetMs": -5}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad latency target: %d", rec.Code)
	}
	if rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "kb", "query": "x", "algorithm": "fastest"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown algorithm: %d", rec.Code)
	}

	// Hybrid: the vector half's plan, and the index it chose.
	rec = doRequest(t, s.handleHybridSearch, map[string]any{"collection": "kb", "query": "backup", "vectorAlgorithm": "auto",
		"explain": true, "filterMeta": map[string][]string{"tag": {"rare"}}})
	m = body(t, rec.Body.Bytes())
	if p := planOf(t, m); p["prefilter"] != true || m["vectorAlgorithm"] != "flat" {
		t.Fatalf("hybrid: %v", m)
	}
	if rec := doRequest(t, s.handleHybridSearch, map[string]any{"collection": "kb", "query": "x", "latencyTargetMs": 70000}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("hybrid bad latency target: %d", rec.Code)
	}
	rec = doRequest(t, s.handleHybridSearch, map[string]any{"collection": "kb", "query": "backup", "explain": true,
		"filterMeta": map[string][]string{"tag": {"none"}}})
	if planOf(t, body(t, rec.Body.Bytes()))["algorithm"] != "flat" {
		t.Fatalf("hybrid empty filter: %s", rec.Body.String())
	}

	// Cross: one plan per target.
	rec = doRequest(t, s.handleCrossSearch, map[string]any{"query": "backup", "targetCollections": []string{"kb"},
		"algorithm": "auto", "explain": true})
	m = body(t, rec.Body.Bytes())
	if p := planOf(t, m); m["algorithm"] != "auto" || p["collections"].(map[string]any)["kb"] == nil {
		t.Fatalf("cross: %v", m)
	}
	if rec := doRequest(t, s.handleCrossSearch, map[string]any{"query": "x", "targetCollections": []string{"kb"}, "latencyTargetMs": -1}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross bad latency target: %d", rec.Code)
	}
	if rec := doRequest(t, s.handleCrossSearch, map[string]any{"query": "x", "targetCollections": []string{"kb"}, "algorithm": "nope"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("cross unknown algorithm: %d", rec.Code)
	}
}

// The profile's vectorAlgorithm applies when the request names none.
func TestPlanProfile_AutoFromTheCollection(t *testing.T) {
	s, cleanup := planServer(t)
	defer cleanup()
	if err := s.CollectionManager.Set("kb", &CollectionConfig{Type: "default", Retrieval: &RetrievalProfileDef{VectorAlgorithm: AlgorithmAuto}}); err != nil {
		t.Fatal(err)
	}
	m := body(t, doRequest(t, s.handleVectorSearch, map[string]any{"collection": "kb", "query": "backup", "explain": true}).Body.Bytes())
	if !strings.Contains(fmt.Sprint(planOf(t, m)["reasons"]), "small enough") {
		t.Fatalf("the profile's auto did not plan: %v", m)
	}
	resp, err := NewDirectClient(s).HybridSearch(context.Background(), &MCPHybridSearchRequest{Collection: "kb", Query: "backup",
		PlanOptions: PlanOptions{Explain: true}})
	if err != nil || resp.Plan == nil || resp.VectorAlgorithm != "flat" {
		t.Fatalf("mcp hybrid with the profile: %v %+v", err, resp)
	}
}

func TestPlanMCPAndGRPC_CarryThePlan(t *testing.T) {
	s, cleanup := planServer(t)
	defer cleanup()
	ctx := context.Background()
	tools := &MCPToolServer{client: NewDirectClient(s)}
	for tool, args := range map[string]map[string]any{
		"semantic_search": {"collection": "kb", "query": "backup", "algorithm": "auto", "explain": true},
		"hybrid_search":   {"collection": "kb", "query": "backup", "vector_algorithm": "auto", "explain": true, "latency_target_ms": 5.0},
		"cross_search":    {"query": "backup", "target_collections": []any{"kb"}, "algorithm": "auto", "explain": true},
	} {
		out, err := tools.mcpCallTool(ctx, tool, args)
		if err != nil || !strings.Contains(out, `"plan"`) || !strings.Contains(out, `"reasons"`) {
			t.Fatalf("%s: %v %s", tool, err, out)
		}
	}
	direct := NewDirectClient(s)
	if _, err := direct.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "kb", Query: "x", PlanOptions: PlanOptions{LatencyTargetMs: -1}}); err == nil {
		t.Fatal("mcp vector: bad latency target accepted")
	}
	if _, err := direct.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "kb", Query: "x", PlanOptions: PlanOptions{LatencyTargetMs: -1}}); err == nil {
		t.Fatal("mcp hybrid: bad latency target accepted")
	}
	if _, err := direct.CrossSearch(ctx, &MCPCrossSearchRequest{Query: "x", TargetCollections: []string{"kb"}, PlanOptions: PlanOptions{LatencyTargetMs: -1}}); err == nil {
		t.Fatal("mcp cross: bad latency target accepted")
	}
	if _, err := direct.CrossSearch(ctx, &MCPCrossSearchRequest{Query: "x", TargetCollections: []string{"kb"}, Algorithm: "nope"}); err == nil {
		t.Fatal("mcp cross: unknown algorithm accepted")
	}
	r, err := direct.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "kb", Query: "backup", FilterMeta: map[string][]string{"tag": {"none"}},
		PlanOptions: PlanOptions{Explain: true}})
	if err != nil || r.Plan == nil || len(r.Results) != 0 {
		t.Fatalf("mcp empty filter: %v %+v", err, r)
	}

	g := &GRPCServer{server: s}
	vr, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "kb", Query: "backup", Algorithm: "auto", Explain: true})
	if err != nil || vr.Plan == nil || vr.Plan.Algorithm != "flat" {
		t.Fatalf("grpc vector: %v %+v", err, vr)
	}
	hr, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "kb", Query: "backup", VectorAlgorithm: "auto", Explain: true, LatencyTargetMs: 50})
	if err != nil || hr.Plan == nil {
		t.Fatalf("grpc hybrid: %v %+v", err, hr)
	}
	cr, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{Query: "backup", TargetCollections: []string{"kb"}, Algorithm: "auto", Explain: true})
	if err != nil || cr.Plan == nil || cr.Plan.Collections["kb"] == nil {
		t.Fatalf("grpc cross: %v %+v", err, cr)
	}
	if vr, _ := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "kb", Query: "backup"}); vr.Plan != nil {
		t.Fatal("grpc: a plan without explain")
	}
}

// The advisor reports which indexes are built and recommends auto.
func TestAdvisor_MeasuresBuiltIndexes(t *testing.T) {
	s, cleanup := planServer(t)
	defer cleanup()
	rec, err := s.RecommendSearch("kb")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Profile.BuiltIndexes) < 2 || rec.RetrievalProfile.VectorAlgorithm != AlgorithmAuto {
		t.Fatalf("built %v, profile %+v", rec.Profile.BuiltIndexes, rec.RetrievalProfile)
	}
}
