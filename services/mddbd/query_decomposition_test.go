package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	gql "mddb/graphql"
	"mddb/internal/embedding"
	vec "mddb/internal/vector"
	proto "mddb/proto"
)

// SRCH-020: query decomposition.

func TestValidateDecomposition(t *testing.T) {
	for _, mode := range []string{"", QueryDecompositionOff, QueryDecompositionAuto} {
		if err := (DecompositionOptions{QueryDecomposition: mode}).ValidateDecomposition(); err != nil {
			t.Errorf("%q refused: %v", mode, err)
		}
	}
	if err := (DecompositionOptions{QueryDecomposition: "on"}).ValidateDecomposition(); err == nil || !strings.Contains(err.Error(), "off or auto") {
		t.Errorf("on accepted: %v", err)
	}
}

func TestSplitQueryParts(t *testing.T) {
	cases := map[string][]string{
		"take a backup and restore it":            {"take a backup", "restore it"},
		"one sentence. another one; a third":      {"one sentence", "another one", "a third"},
		"backup and restore":                      {"backup and restore"},
		"plain query":                             {"plain query"},
		"and then some":                           {"and then some"},
		"first part, x":                           {"first part, x"},
		"read the logs and check the disk, twice": {"read the logs", "check the disk twice"},
		"a b. c d. e f. g h. i j":                 {"a b", "c d", "e f", "g h i j"},
		"":                                        {""},
	}
	for in, want := range cases {
		if got := splitQueryParts(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitQueryParts(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchQueryVectors_FusesByMaxWithDeterministicTies(t *testing.T) {
	lists := map[float32][]vec.VectorResult{
		1: {{DocID: "a", Score: 0.9}, {DocID: "b", Score: 0.5}, {DocID: "c", Score: 0.4}},
		2: {{DocID: "b", Score: 0.7}, {DocID: "d", Score: 0.7}, {DocID: "a", Score: 0.1}},
	}
	search := func(v []float32) []vec.VectorResult { return lists[v[0]] }
	if got := searchQueryVectors([][]float32{{1}}, 1, search); !reflect.DeepEqual(got, lists[1]) {
		t.Errorf("one vector must be the search untouched, got %+v", got)
	}
	want := []vec.VectorResult{{DocID: "a", Score: 0.9}, {DocID: "b", Score: 0.7}, {DocID: "d", Score: 0.7}}
	if got := searchQueryVectors([][]float32{{1}, {2}}, 3, search); !reflect.DeepEqual(got, want) {
		t.Errorf("fused = %+v, want %+v", got, want)
	}
	if got := searchQueryVectors([][]float32{{1}, {2}}, 0, search); len(got) != 4 {
		t.Errorf("limit 0 must keep every hit, got %+v", got)
	}
}

func TestMergeVectorsAndTextQuery(t *testing.T) {
	src := map[string][]float32{"a": {1}}
	if got := mergeVectors(nil, src); !reflect.DeepEqual(got, src) {
		t.Errorf("nil dst: %v", got)
	}
	dst := map[string][]float32{"b": {2}}
	if got := mergeVectors(dst, src); len(got) != 2 || got["a"][0] != 1 {
		t.Errorf("merge: %v", got)
	}
	if textQuery("q", []float32{1}) != "" || textQuery("q", nil) != "q" {
		t.Error("textQuery")
	}
}

// failingPartEmbedding fails on one text, so a part's embedding can fail
// after the whole query's succeeded.
type failingPartEmbedding struct {
	embedding.Provider
	fail string
}

func (f failingPartEmbedding) Embed(ctx context.Context, text string, role embedding.Role) ([]float32, error) {
	if text == f.fail {
		return nil, errors.New("provider down")
	}
	return f.Provider.Embed(ctx, text, role)
}

func TestQueryVectors(t *testing.T) {
	s := &Server{Embedding: embedding.NewLexicalProvider(64)}
	ctx := context.Background()
	whole := []float32{1, 0}
	auto := DecompositionOptions{QueryDecomposition: QueryDecompositionAuto}

	plan := &SearchPlan{}
	vs, err := s.queryVectors(ctx, DecompositionOptions{}, "pump seal and meeting notes", whole, nil, plan)
	if err != nil || len(vs) != 1 || plan.Decomposition != nil {
		t.Fatalf("off: %v %d %+v", err, len(vs), plan)
	}

	plan = &SearchPlan{}
	if vs, err = s.queryVectors(ctx, auto, "", whole, nil, plan); err != nil || len(vs) != 1 || plan.Decomposition.Parts != 1 ||
		!strings.Contains(strings.Join(plan.Reasons, " "), "query vector was given") {
		t.Fatalf("given vector: %v %d %+v", err, len(vs), plan)
	}

	plan = &SearchPlan{}
	if vs, err = s.queryVectors(ctx, auto, "pump seal", whole, nil, plan); err != nil || len(vs) != 1 || plan.Decomposition.Parts != 1 || plan.Reasons != nil {
		t.Fatalf("unsplit: %v %d %+v", err, len(vs), plan)
	}

	plan = &SearchPlan{}
	raw, _ := s.Embedding.Embed(ctx, "pump seal", embedding.RoleQuery)
	example := make([]float32, len(raw))
	example[len(raw)-1] = 1
	fb := &feedback{mode: FeedbackModeQuery, rocchio: vec.DefaultRocchio, pos: [][]float32{example}, applied: &FeedbackApplied{}}
	vs, err = s.queryVectors(ctx, auto, "pump seal and meeting notes", whole, fb, plan)
	if err != nil || len(vs) != 3 || plan.Decomposition.Parts != 2 ||
		!reflect.DeepEqual(plan.Decomposition.Texts, []string{"pump seal", "meeting notes"}) {
		t.Fatalf("split: %v %d %+v", err, len(vs), plan.Decomposition)
	}
	if reflect.DeepEqual(vs[1], raw) || fb.applied.QueryShift != 0 {
		t.Error("a part must be corrected by query-mode feedback without recording a shift")
	}

	s.Embedding = failingPartEmbedding{Provider: embedding.NewLexicalProvider(64), fail: "meeting notes"}
	if _, err = s.queryVectors(ctx, auto, "pump seal and meeting notes", whole, nil, &SearchPlan{}); !errors.Is(err, errEmbedQuery) {
		t.Fatalf("part failure: %v", err)
	}
}

func TestSearchVectorPath_DiskOnlyRescoresEachVector(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := s.CollectionManager.Set("disk", &CollectionConfig{Quantization: "int8", DiskOnlyVectors: true}); err != nil {
		t.Fatal(err)
	}
	flat := vec.NewVectorIndex()
	for id, v := range map[string][]float32{"x": {1, 0}, "y": {0, 1}} {
		if err := flat.Add("disk", id, v); err != nil {
			t.Fatal(err)
		}
		if err := s.VectorStore.Put("disk", id, v, "m", id); err != nil {
			t.Fatal(err)
		}
	}
	ps := plannedSearch{Searcher: flat, Algorithm: "flat", Plan: &SearchPlan{}}
	results, disk := s.searchVectorPath(ps, indexQuery{Collection: "disk", TopK: 5}, [][]float32{{1, 0}, {0, 1}})
	if len(results) != 2 || results[0].Score < 0.99 || results[1].Score < 0.99 || len(disk) != 2 {
		t.Fatalf("each document keeps its best exact score: %+v, %d vectors", results, len(disk))
	}
}

// autoServer is the golden collection, searched with decomposition on.
func autoServer(t *testing.T) (*Server, func()) {
	t.Helper()
	return goldenServer(t)
}

func TestQueryDecomposition_EverySurface(t *testing.T) {
	s, cleanup := autoServer(t)
	defer cleanup()
	ctx := context.Background()
	q := decompositionGoldenQuery

	decompositionOverREST(t, s, q)
	direct := NewDirectClient(s)
	decompositionOverMCP(ctx, t, direct, q)
	decompositionOverGRPC(ctx, t, s, q)
	decompositionOverGraphQL(ctx, t, s, direct, q)
}

// decompositionOverREST checks REST vector and hybrid search split q in two
// under "auto", and refuse the unknown mode "on".
func decompositionOverREST(t *testing.T, s *Server, q string) {
	t.Helper()
	for name, h := range map[string]http.HandlerFunc{"vector": s.handleVectorSearch, "hybrid": s.handleHybridSearch} {
		rec := doRequest(t, h, map[string]any{"collection": "gold", "query": q, "topK": 3, "explain": true, "queryDecomposition": "auto"})
		if rec.Code != http.StatusOK {
			t.Fatalf("rest %s: %d %s", name, rec.Code, rec.Body.String())
		}
		d, _ := planOf(t, body(t, rec.Body.Bytes()))["decomposition"].(map[string]any)
		if d == nil || d["parts"].(float64) != 2 {
			t.Fatalf("rest %s plan: %s", name, rec.Body.String())
		}
		if rec := doRequest(t, h, map[string]any{"collection": "gold", "query": q, "queryDecomposition": "on"}); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("rest %s: on gave %d", name, rec.Code)
		}
	}
}

// decompositionOverMCP checks the direct client and the MCP tools split q
// in two under "auto", and the client refuses an unknown mode.
func decompositionOverMCP(ctx context.Context, t *testing.T, direct *DirectClient, q string) {
	t.Helper()
	auto := DecompositionOptions{QueryDecomposition: QueryDecompositionAuto}
	v, err := direct.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "gold", Query: q, TopK: 3,
		PlanOptions: PlanOptions{Explain: true}, DecompositionOptions: auto})
	if err != nil || v.Plan.Decomposition.Parts != 2 {
		t.Fatalf("mcp vector: %v %+v", err, v)
	}
	h, err := direct.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "gold", Query: q, TopK: 3,
		PlanOptions: PlanOptions{Explain: true}, DecompositionOptions: auto})
	if err != nil || h.Plan.Decomposition.Parts != 2 {
		t.Fatalf("mcp hybrid: %v %+v", err, h)
	}
	bad := DecompositionOptions{QueryDecomposition: "x"}
	if _, err := direct.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "gold", Query: q, DecompositionOptions: bad}); err == nil {
		t.Fatal("mcp vector: bad mode accepted")
	}
	if _, err := direct.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "gold", Query: q, DecompositionOptions: bad}); err == nil {
		t.Fatal("mcp hybrid: bad mode accepted")
	}

	tools := &MCPToolServer{client: direct}
	for _, tool := range []string{"semantic_search", "hybrid_search"} {
		out, err := tools.mcpCallTool(ctx, tool, map[string]any{"collection": "gold", "query": q, "explain": true, "query_decomposition": "auto"})
		if err != nil || !strings.Contains(out, `"decomposition"`) {
			t.Fatalf("mcp tool %s: %v %s", tool, err, out)
		}
	}
}

// decompositionOverGRPC checks gRPC vector and hybrid search split q in two
// under "auto", and vector search refuses the unknown mode "on".
func decompositionOverGRPC(ctx context.Context, t *testing.T, s *Server, q string) {
	t.Helper()
	g := &GRPCServer{server: s}
	gv, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "gold", Query: q, TopK: 3, Explain: true, QueryDecomposition: "auto"})
	if err != nil || gv.Plan.GetDecomposition().GetParts() != 2 || len(gv.Plan.GetDecomposition().GetTexts()) != 2 {
		t.Fatalf("grpc vector: %v %+v", err, gv)
	}
	gh, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "gold", Query: q, TopK: 3, Explain: true, QueryDecomposition: "auto"})
	if err != nil || gh.Plan.GetDecomposition().GetParts() != 2 {
		t.Fatalf("grpc hybrid: %v %+v", err, gh)
	}
	if _, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "gold", Query: q, QueryDecomposition: "on"}); err == nil {
		t.Fatal("grpc vector: bad mode accepted")
	}
}

// decompositionOverGraphQL checks GraphQL splits q in two under "auto" and
// reports a query it cannot split as one part with its texts.
func decompositionOverGraphQL(ctx context.Context, t *testing.T, s *Server, direct *DirectClient, q string) {
	t.Helper()
	a := &GraphQLAdapter{server: s, mcp: direct}
	mode, explain := "auto", true
	qv, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "gold", Query: &q, Explain: &explain, QueryDecomposition: &mode})
	if err != nil || qv.Plan.Decomposition.Parts != 2 || len(qv.Plan.Decomposition.Texts) != 2 {
		t.Fatalf("graphql: %v %+v", err, qv)
	}
	unsplit := "pump seal"
	qv, err = a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "gold", Query: &unsplit, Explain: &explain, QueryDecomposition: &mode})
	if err != nil || qv.Plan.Decomposition.Parts != 1 || qv.Plan.Decomposition.Texts == nil {
		t.Fatalf("graphql unsplit: %v %+v", err, qv)
	}
}

// TestQueryDecomposition_ScoresAreTheBestPart: each document's auto score is
// the best of its scores for the whole query and for each part.
func TestQueryDecomposition_ScoresAreTheBestPart(t *testing.T) {
	s, cleanup := autoServer(t)
	defer cleanup()
	ctx := context.Background()
	direct := NewDirectClient(s)
	scores := func(query, mode string) map[string]float64 {
		r, err := direct.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "gold", Query: query, TopK: 3,
			DecompositionOptions: DecompositionOptions{QueryDecomposition: mode}})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]float64{}
		for _, x := range r.Results {
			out[x.Document.Key] = float64(x.Score)
		}
		return out
	}
	q := decompositionGoldenQuery
	got := scores(q, QueryDecompositionAuto)
	want := scores(q, "")
	for _, part := range splitQueryParts(q) {
		for k, v := range scores(part, "") {
			want[k] = max(want[k], v)
		}
	}
	for k, v := range want {
		if d := got[k] - v; d > 1e-6 || d < -1e-6 {
			t.Errorf("%s: auto %v, best part %v", k, got[k], v)
		}
	}
}

// TestQueryDecomposition_PartEmbeddingFails: a part that cannot be embedded
// fails the search on every path, as the whole query's failure does.
func TestQueryDecomposition_PartEmbeddingFails(t *testing.T) {
	s, cleanup := autoServer(t)
	defer cleanup()
	ctx := context.Background()
	s.Embedding = failingPartEmbedding{Provider: s.Embedding, fail: splitQueryParts(decompositionGoldenQuery)[1]}
	req := map[string]any{"collection": "gold", "query": decompositionGoldenQuery, "queryDecomposition": "auto"}
	for name, h := range map[string]http.HandlerFunc{"vector": s.handleVectorSearch, "hybrid": s.handleHybridSearch} {
		if rec := doRequest(t, h, req); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "provider down") {
			t.Fatalf("rest %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	auto := DecompositionOptions{QueryDecomposition: QueryDecompositionAuto}
	direct := NewDirectClient(s)
	if _, err := direct.VectorSearch(ctx, &MCPVectorSearchRequest{Collection: "gold", Query: decompositionGoldenQuery,
		DecompositionOptions: auto}); !errors.Is(err, errEmbedQuery) {
		t.Fatalf("mcp vector: %v", err)
	}
	if _, err := direct.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "gold", Query: decompositionGoldenQuery,
		DecompositionOptions: auto}); !errors.Is(err, errEmbedQuery) {
		t.Fatalf("mcp hybrid: %v", err)
	}
}

func TestQueryDecomposition_Eval(t *testing.T) {
	s, cleanup := autoServer(t)
	defer cleanup()
	ctx := context.Background()
	for _, typ := range []string{SearchTypeVector, SearchTypeHybrid} {
		c := EvalConfig{SearchType: typ, QueryDecomposition: QueryDecompositionAuto}
		if keys, _, err := s.evalSearch(ctx, "gold", 3, c, decompositionGoldenQuery); err != nil || len(keys) == 0 {
			t.Fatalf("%s: %v %v", typ, err, keys)
		}
		if !strings.HasSuffix(c.label(), " decomposition=auto") {
			t.Errorf("label %q", c.label())
		}
		c.QueryDecomposition = "on"
		if _, _, err := s.evalSearch(ctx, "gold", 3, c, "x"); err == nil {
			t.Fatalf("%s: bad mode accepted", typ)
		}
	}
}
