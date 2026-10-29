package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"mddb/internal/embedding"
	vec "mddb/internal/vector"
	proto "mddb/proto"
)

// RAG-009 on cross-collection search: each target applies its own
// summaryMode. "sum" is the summaryServer corpus; "plain" has a document whose
// summary answers the general question too, but summaries are off there.

const depotSummary = "Main hazards overview for the depot yard."

// crossSummaryServer is summaryServer plus "plain", summaries off.
func crossSummaryServer(t *testing.T, profile *RetrievalProfileDef) (*Server, func()) {
	t.Helper()
	s, _, cleanup := summaryServer(t, profile)
	if err := s.CollectionManager.Set("plain", &CollectionConfig{Embedding: &EmbeddingChunkingDef{ChunkSize: 100},
		Retrieval: &RetrievalProfileDef{SummaryMode: SummaryModeOff}}); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "plain", "depot", "en", "Forklift charging bays are swept every morning before the first delivery.",
		map[string][]string{"summary": {depotSummary}})
	addTestDoc(t, s, "plain", "pumps", "en", "Pump seal kits for line four are kept in the depot stores.", nil)
	waitSummaries(t, s, "plain", 1)
	return s, cleanup
}

// restCross runs a REST cross search over sum and plain and returns the raw
// body.
func restCross(t *testing.T, s *Server, req map[string]any) string {
	t.Helper()
	req["targetCollections"] = []string{"sum", "plain"}
	rec := doRequest(t, s.handleCrossSearch, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// crossHits is "collection/key" per result, or per passage.
func crossHits(t *testing.T, raw string) []string {
	t.Helper()
	m := body(t, []byte(raw))
	var out []string
	if ps, ok := m["passages"].([]any); ok {
		for _, p := range ps {
			pm := p.(map[string]any)
			out = append(out, pm["collection"].(string)+"/"+pm["key"].(string))
		}
		return out
	}
	for _, r := range m["results"].([]any) {
		rm := r.(map[string]any)
		out = append(out, rm["collection"].(string)+"/"+rm["document"].(map[string]any)["key"].(string))
	}
	return out
}

// crossOffResponses runs the cross vector searches the golden compares.
// Hybrid is left out: its RRF ties between near-zero vector hits order
// differently from run to run, summaries or not.
func crossOffResponses(t *testing.T, s *Server) []string {
	t.Helper()
	q := "pump seal main hazards overview"
	return []string{
		restCross(t, s, map[string]any{"query": q, "threshold": 0.1, "explain": true}),
		restCross(t, s, map[string]any{"query": q, "threshold": 0.1, "algorithm": "auto", "explain": true}),
		restCross(t, s, map[string]any{"query": q, "threshold": 0.1, "format": "passages"}),
	}
}

// Every target off, or without summaries: the same bytes as a server that
// never heard of summaries.
func TestSummaryCross_OffIsGolden(t *testing.T) {
	t.Setenv("MDDB_SEARCH_STATS", "false")
	s, cleanup := crossSummaryServer(t, nil)
	defer cleanup()
	noProfile := crossOffResponses(t, s)
	setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: SummaryModeOff, SummaryWeight: floatPtr(9)})
	off := crossOffResponses(t, s)
	s.summaries().reset()
	today := crossOffResponses(t, s)
	for i := range today {
		if noProfile[i] != today[i] || off[i] != today[i] {
			t.Fatalf("cross search %d differs:\nno profile %s\noff        %s\ntoday      %s", i, noProfile[i], off[i], today[i])
		}
	}
	if strings.Contains(strings.Join(today, ""), "summaryMode") {
		t.Fatal("an off search explained summaries")
	}
	// Not vacuous: with summaries on for sum, the vector searches differ.
	s.loadVectorIndex()
	setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: SummaryModeExpand})
	for i, got := range crossOffResponses(t, s) {
		if got == today[i] {
			t.Fatalf("cross search %d is the same with summaries on: %s", i, got)
		}
	}
}

// Blend on sum, off on plain: plant joins by its summary, depot does not.
func TestSummaryCross_BlendPerTargetREST(t *testing.T) {
	s, cleanup := crossSummaryServer(t, &RetrievalProfileDef{SummaryMode: SummaryModeBlend})
	defer cleanup()
	raw := restCross(t, s, map[string]any{"query": generalQuery, "threshold": 0.3, "explain": true})
	if hits := crossHits(t, raw); strings.Join(hits, ",") != "sum/plant" {
		t.Fatalf("blend + off: %v", hits)
	}
	// A named algorithm has one plan for every target: the line names sum.
	reasons := strings.Join(toStrings(planOf(t, body(t, []byte(raw)))["reasons"]), " | ")
	if !strings.Contains(reasons, "sum: summaryMode blend: 1 documents") || strings.Contains(reasons, "plain:") {
		t.Fatalf("explain: %s", reasons)
	}
	// auto: each target's own plan carries its own line.
	plan := planOf(t, body(t, []byte(restCross(t, s, map[string]any{"query": generalQuery, "threshold": 0.3,
		"algorithm": "auto", "explain": true}))))
	colls := plan["collections"].(map[string]any)
	if r := strings.Join(toStrings(colls["sum"].(map[string]any)["reasons"]), " "); !strings.Contains(r, "summaryMode blend") {
		t.Fatalf("auto sum: %s", r)
	}
	if r := strings.Join(toStrings(colls["plain"].(map[string]any)["reasons"]), " "); strings.Contains(r, "summaryMode") {
		t.Fatalf("auto plain: %s", r)
	}
	// Blend in passages: one passage, its best chunk.
	if hits := crossHits(t, restCross(t, s, map[string]any{"query": generalQuery, "threshold": 0.3, "format": "passages"})); strings.Join(hits, ",") != "sum/plant" {
		t.Fatalf("blend passages: %v", hits)
	}
	// Switch the targets round: plain's summary now answers, sum's does not.
	setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: SummaryModeOff})
	cfg, _ := s.CollectionManager.Get("plain")
	next := *cfg
	next.Retrieval = &RetrievalProfileDef{SummaryMode: SummaryModeBlend}
	if err := s.CollectionManager.Set("plain", &next); err != nil {
		t.Fatal(err)
	}
	if hits := crossHits(t, restCross(t, s, map[string]any{"query": generalQuery, "threshold": 0.3})); strings.Join(hits, ",") != "plain/depot" {
		t.Fatalf("off + blend: %v", hits)
	}
}

// Expand on sum: its summary hit brings its chunks, one passage each, as in
// single-collection search; results stay one per document.
func TestSummaryCross_ExpandREST(t *testing.T) {
	s, cleanup := crossSummaryServer(t, &RetrievalProfileDef{SummaryMode: SummaryModeExpand})
	defer cleanup()
	want := strings.Repeat("sum/plant,", maxSummaryExpandChunks)
	for _, algo := range []string{"", "auto"} {
		raw := restCross(t, s, map[string]any{"query": generalQuery, "threshold": 0.3, "algorithm": algo, "format": "passages"})
		if hits := crossHits(t, raw); strings.Join(hits, ",")+"," != want {
			t.Fatalf("expand passages (%q): %v", algo, hits)
		}
		if text := body(t, []byte(raw))["passages"].([]any)[0].(map[string]any)["content"].(string); !strings.Contains(plantContent, text) {
			t.Fatalf("a passage that is not a chunk of the document: %q", text)
		}
	}
	if hits := crossHits(t, restCross(t, s, map[string]any{"query": generalQuery, "threshold": 0.3})); strings.Join(hits, ",") != "sum/plant" {
		t.Fatalf("expand results: %v", hits)
	}
	// Cross hybrid passages: the same chunks.
	if hits := crossHits(t, restCross(t, s, map[string]any{"query": generalQuery, "searchType": "hybrid", "topK": 1, "format": "passages"})); strings.Join(hits, ",")+"," != want {
		t.Fatalf("hybrid expand passages: %v", hits)
	}
}

func TestSummaryCross_MCPAndGRPC(t *testing.T) {
	s, cleanup := crossSummaryServer(t, nil)
	defer cleanup()
	ctx := context.Background()
	g := &GRPCServer{server: s}
	tools := &MCPToolServer{client: NewDirectClient(s)}
	targets := []any{"sum", "plain"}
	for _, mode := range []string{SummaryModeOff, SummaryModeBlend, SummaryModeExpand} {
		setSummaryMode(t, s, &RetrievalProfileDef{SummaryMode: mode})
		want := mode != SummaryModeOff
		wantPassages := map[string]int{SummaryModeOff: 0, SummaryModeBlend: 1, SummaryModeExpand: maxSummaryExpandChunks}[mode]

		out, err := tools.mcpCallTool(ctx, "cross_search", map[string]any{"target_collections": targets, "query": generalQuery, "threshold": 0.3})
		if err != nil || strings.Contains(out, `"plant"`) != want || strings.Contains(out, `"depot"`) {
			t.Fatalf("mcp %s: %v %s", mode, err, out)
		}
		out, err = tools.mcpCallTool(ctx, "cross_search", map[string]any{"target_collections": targets, "query": generalQuery,
			"threshold": 0.3, "format": "passages"})
		if err != nil || strings.Count(out, `"key": "plant"`) != wantPassages || strings.Contains(out, `"depot"`) {
			t.Fatalf("mcp passages %s: %v %s", mode, err, out)
		}
		cr, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{TargetCollections: []string{"sum", "plain"}, Query: generalQuery,
			Threshold: 0.3, Explain: true})
		if err != nil || (len(cr.Results) == 1 && cr.Results[0].Collection == "sum" && cr.Results[0].Document.Key == "plant") != want {
			t.Fatalf("grpc %s: %v %+v", mode, err, cr)
		}
		if explained := strings.Contains(strings.Join(cr.Plan.Reasons, " "), "sum: summaryMode "+mode); explained != want {
			t.Fatalf("grpc explain %s: %v", mode, cr.Plan.Reasons)
		}
		pr, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{TargetCollections: []string{"sum", "plain"}, Query: generalQuery,
			Threshold: 0.3, Format: "passages", Algorithm: "auto"})
		if err != nil || len(pr.Passages) != wantPassages {
			t.Fatalf("grpc passages %s: %v %d", mode, err, len(pr.Passages))
		}
	}
}

// Without a plan (an internal caller), a target's summaries still apply.
func TestSummaryCross_NoPlan(t *testing.T) {
	s, cleanup := crossSummaryServer(t, &RetrievalProfileDef{SummaryMode: SummaryModeExpand})
	defer cleanup()
	q, _ := s.Embedding.Embed(context.Background(), generalQuery, embedding.RoleQuery)
	items := s.crossSearchItems(crossSearchParams{Searcher: s.VectorSearchers["flat"], QueryVector: q,
		Targets: []string{"sum", "plain"}, SearchTopK: 20, TopK: 5, Threshold: 0.3, Metric: vec.CosineSimilarity})
	if len(items) != 1 || items[0].Document.Key != "plant" || len(items[0].chunks) != maxSummaryExpandChunks {
		t.Fatalf("no plan: %+v", items)
	}
}
