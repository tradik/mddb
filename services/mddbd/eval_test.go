package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

func TestScoreQuery_Metrics(t *testing.T) {
	q := EvalQuery{Query: "x", ExpectedKeys: []string{"a", "b"}}
	r := scoreQuery(q, []string{"z", "a", "b"}, 10)
	if r.Recall != 1 || r.RR != 0.5 {
		t.Fatalf("recall/rr: %+v", r)
	}
	// DCG = 1/log2(3) + 1/log2(4); IDCG = 1 + 1/log2(3).
	want := (1/math.Log2(3) + 0.5) / (1 + 1/math.Log2(3))
	if math.Abs(r.NDCG-want) > 1e-9 {
		t.Fatalf("ndcg %v want %v", r.NDCG, want)
	}
	// The cut-off applies before scoring.
	if r = scoreQuery(q, []string{"z", "a", "b"}, 1); r.Recall != 0 || r.RR != 0 || len(r.Returned) != 1 {
		t.Fatalf("k=1: %+v", r)
	}
	// Grades weigh nDCG: the grade-3 key first is perfect, second is not.
	g := EvalQuery{Query: "x", ExpectedKeys: []string{"a", "b"}, Grades: map[string]int{"a": 3}}
	if scoreQuery(g, []string{"a", "b"}, 10).NDCG != 1 || scoreQuery(g, []string{"b", "a"}, 10).NDCG >= 1 {
		t.Fatal("graded ndcg")
	}
	// A grade of 0 means "listed but irrelevant": no nDCG to earn.
	if scoreQuery(EvalQuery{Query: "x", ExpectedKeys: []string{"a"}, Grades: map[string]int{"a": 0}}, []string{"a"}, 10).NDCG != 0 {
		t.Fatal("zero ideal")
	}
	if percentile([]float64{3, 1, 2}, 0.5) != 2 || percentile([]float64{3, 1, 2}, 0.99) != 3 || percentile([]float64{5}, 0) != 5 {
		t.Fatal("percentile")
	}
	if (EvalConfig{SearchType: "hybrid", Alpha: floatPtr(0.3)}).label() != "hybrid alpha=0.30" ||
		(EvalConfig{Name: "n"}).label() != "n" || (EvalConfig{SearchType: "fts", Algorithm: "bm25"}).label() != "fts/bm25" {
		t.Fatal("label")
	}
	if (EvalQuery{Query: "x"}).Validate() == nil || (EvalQuery{Query: "x", ExpectedKeys: []string{"a"}, Grades: map[string]int{"a": 4}}).Validate() == nil {
		t.Fatal("validate")
	}
}

func evalFixture(t *testing.T) (*Server, func()) {
	s, cleanup := newHandlerTestServer(t)
	addTestDoc(t, s, "kb", "install.md", "en", "# Install\nRun the installer with docker compose up.", nil)
	addTestDoc(t, s, "kb", "backup.md", "en", "# Backup\nCreate a snapshot backup of the database file.", nil)
	addTestDoc(t, s, "kb", "auth.md", "en", "# Auth\nJWT tokens and API keys protect every endpoint.", nil)
	return s, cleanup
}

func TestEvalSet_StoredAndRun(t *testing.T) {
	s, cleanup := evalFixture(t)
	defer cleanup()
	if _, err := s.RunEval(context.Background(), EvalRequest{Collection: "kb"}); err == nil {
		t.Fatal("no set")
	}
	q, err := s.AddEvalQuery("kb", EvalQuery{Query: "snapshot backup", ExpectedKeys: []string{"backup.md"}})
	if err != nil || q.ID == "" {
		t.Fatal(err)
	}
	if _, err := s.AddEvalQuery("kb", EvalQuery{ID: "jwt", Query: "jwt tokens", ExpectedKeys: []string{"auth.md"}}); err != nil {
		t.Fatal(err)
	}
	// Same ID replaces.
	if _, err := s.AddEvalQuery("kb", EvalQuery{ID: "jwt", Query: "api keys", ExpectedKeys: []string{"auth.md"}}); err != nil {
		t.Fatal(err)
	}
	if set := s.evalSet("kb"); len(set) != 2 || set[1].Query != "api keys" {
		t.Fatalf("set: %+v", set)
	}
	// A config rewrite through PUT keeps the set.
	rec := httptest.NewRecorder()
	s.handleCollectionConfig(rec, httptest.NewRequest(http.MethodPut, "/v1/collection-config",
		strings.NewReader(`{"collection":"kb","type":"documents","description":"d"}`)))
	if rec.Code != 200 || len(s.evalSet("kb")) != 2 {
		t.Fatalf("put kept the set: %d %s", rec.Code, rec.Body.String())
	}

	res, err := s.RunEval(context.Background(), EvalRequest{Collection: "kb", K: 500, Details: true,
		Configs: []EvalConfig{{SearchType: SearchTypeFTS, Algorithm: "bm25"}, {SearchType: SearchTypeVector}, {SearchType: "nope"}}})
	if err != nil || res.K != maxEvalK || res.Queries != 2 {
		t.Fatalf("run: %v %+v", err, res)
	}
	fts := res.Results[0]
	if fts.Recall != 1 || fts.MRR != 1 || fts.Tokens == 0 || len(fts.PerQuery) != 2 || fts.Error != "" {
		t.Fatalf("fts: %+v", fts)
	}
	if res.Results[1].Error == "" || !strings.Contains(res.Results[2].Error, "searchType") {
		t.Fatalf("errors per config: %+v", res.Results[1:])
	}

	if found, _ := s.DeleteEvalQuery("kb", "jwt"); !found {
		t.Fatal("delete")
	}
	if found, _ := s.DeleteEvalQuery("kb", "jwt"); found {
		t.Fatal("delete twice")
	}
	// Inline queries, and the bounds.
	if _, err := s.RunEval(context.Background(), EvalRequest{Collection: "kb", Queries: []EvalQuery{{Query: "x"}}}); err == nil {
		t.Fatal("invalid inline query")
	}
	if _, err := s.RunEval(context.Background(), EvalRequest{Collection: "kb", Configs: make([]EvalConfig, maxEvalConfigs+1)}); err == nil {
		t.Fatal("too many configs")
	}
	if _, err := s.RunEval(context.Background(), EvalRequest{}); err == nil {
		t.Fatal("collection")
	}
	if _, err := s.AddEvalQuery("", EvalQuery{}); err == nil {
		t.Fatal("add without collection")
	}
	if _, err := s.AddEvalQuery("kb", EvalQuery{Query: "q"}); err == nil {
		t.Fatal("add invalid")
	}
	for i := len(s.evalSet("kb")); i < maxEvalQueries; i++ {
		if _, err := s.AddEvalQuery("kb", EvalQuery{ID: itoa(i), Query: "q", ExpectedKeys: []string{"a"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddEvalQuery("kb", EvalQuery{Query: "q", ExpectedKeys: []string{"a"}}); err == nil {
		t.Fatal("full set")
	}
}

func TestEval_HybridAndAdvisor(t *testing.T) {
	s, cleanup := evalFixture(t)
	defer cleanup()
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	if _, err := s.AddEvalQuery("kb", EvalQuery{Query: "snapshot backup", ExpectedKeys: []string{"backup.md"}}); err != nil {
		t.Fatal(err)
	}
	res, err := s.RunEval(context.Background(), EvalRequest{Collection: "kb", Configs: []EvalConfig{{SearchType: SearchTypeHybrid}}})
	if err != nil || res.Results[0].Error != "" {
		t.Fatalf("hybrid: %v %+v", err, res)
	}
	rec, err := s.RecommendSearch("kb")
	if err != nil || rec.Measured == nil || !strings.HasPrefix(rec.Reasons[0], "Measured on 1") || rec.RetrievalProfile.DefaultSearchType != rec.SearchType {
		t.Fatalf("advisor: %v %+v", err, rec)
	}
	// Without an embedder only keyword configurations are measured.
	s.Embedding = nil
	if rec, _ = s.RecommendSearch("kb"); rec.SearchType != SearchTypeFTS || len(rec.Measured.Results) != 4 || rec.RetrievalProfile.BM25B != nil {
		t.Fatalf("advisor fts: %+v", rec)
	}
	// A set that cannot run is a warning, not a failure.
	if _, err := s.AddEvalQuery("kb", EvalQuery{ID: "bad", Query: "q", ExpectedKeys: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := s.CollectionManager.Get("kb")
	cfg.EvalSet[0].ExpectedKeys = nil
	if rec, _ = s.RecommendSearch("kb"); rec.Measured != nil || len(rec.Warnings) == 0 {
		t.Fatalf("broken set: %+v", rec)
	}
	if b, _ := bestMeasured(&EvalResult{Results: []EvalConfigResult{{Error: "x"}}}, []EvalConfig{{}}); b != nil {
		t.Fatal("all failed")
	}
	if b, _ := bestMeasured(&EvalResult{}, []EvalConfig{{}}); b != nil {
		t.Fatal("fewer results than configs")
	}
	// Hybrid chosen over a recommendation that had no strategy gets one, and
	// the stored profile follows the new choice.
	r := &SearchRecommendation{SearchType: SearchTypeFTS}
	adoptMeasured(r, &EvalResult{Queries: 1, K: 10}, &EvalConfigResult{Config: "hybrid", NDCG: 1}, EvalConfig{SearchType: SearchTypeHybrid})
	if r.HybridStrategy != "alpha" || r.RetrievalProfile.DefaultSearchType != SearchTypeHybrid || !r.RetrievalProfile.HybridAlphaSet {
		t.Fatalf("adopt: %+v", r)
	}
}

func TestEval_HTTPAndMCP(t *testing.T) {
	s, cleanup := evalFixture(t)
	defer cleanup()
	call := func(method, target, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handleEvalQueries(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
		return rec
	}
	if rec := call(http.MethodGet, "/v1/eval-queries?collection=kb", ""); !strings.Contains(rec.Body.String(), `"queries":[]`) {
		t.Fatalf("empty list: %s", rec.Body.String())
	}
	if rec := call(http.MethodPost, "/v1/eval-queries", `{"collection":"kb","id":"b","query":"snapshot backup","expectedKeys":["backup.md"]}`); rec.Code != 200 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(http.MethodPost, "/v1/eval-queries", `{"collection":"kb","query":""}`); rec.Code != 400 {
		t.Fatalf("add invalid: %d", rec.Code)
	}
	if rec := call(http.MethodPost, "/v1/eval-queries", `{`); rec.Code != 400 {
		t.Fatalf("add bad json: %d", rec.Code)
	}
	if rec := call(http.MethodGet, "/v1/eval-queries?collection=kb", ""); !strings.Contains(rec.Body.String(), `"id":"b"`) {
		t.Fatalf("list: %s", rec.Body.String())
	}
	if rec := call(http.MethodPut, "/v1/eval-queries", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatal("method")
	}

	rec := doRequest(t, s.handleEval, map[string]any{"collection": "kb", "configs": []map[string]any{{"searchType": "fts"}}})
	var res EvalResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res.Results[0].Recall != 1 {
		t.Fatalf("eval: %s", rec.Body.String())
	}
	if rec = doRequest(t, s.handleEval, map[string]any{"collection": "none"}); rec.Code != 400 {
		t.Fatal("eval without a set")
	}
	r := httptest.NewRecorder()
	s.handleEval(r, httptest.NewRequest(http.MethodGet, "/v1/eval", nil))
	if r.Code != http.StatusMethodNotAllowed {
		t.Fatal("eval method")
	}
	r = httptest.NewRecorder()
	s.handleEval(r, httptest.NewRequest(http.MethodPost, "/v1/eval", strings.NewReader("{")))
	if r.Code != 400 {
		t.Fatal("eval json")
	}

	tools := &MCPToolServer{client: NewDirectClient(s)}
	ctx := context.Background()
	out, err := tools.mcpCallTool(ctx, "add_eval_query", map[string]interface{}{"collection": "kb", "query": "jwt tokens",
		"expected_keys": []interface{}{"auth.md"}, "grades": map[string]interface{}{"auth.md": float64(3)}})
	if err != nil || !strings.Contains(out, `"auth.md": 3`) {
		t.Fatalf("mcp add: %v %s", err, out)
	}
	if _, err = tools.mcpCallTool(ctx, "add_eval_query", map[string]interface{}{"collection": "kb"}); err == nil {
		t.Fatal("mcp add invalid")
	}
	out, err = tools.mcpCallTool(ctx, "run_eval", map[string]interface{}{"collection": "kb",
		"configs": []interface{}{map[string]interface{}{"searchType": "fts", "algorithm": "bm25"}}})
	if err != nil || !strings.Contains(out, `"recallAtK": 1`) {
		t.Fatalf("mcp run: %v %s", err, out)
	}
	if _, err = tools.mcpCallTool(ctx, "run_eval", map[string]interface{}{"collection": "kb", "configs": "x"}); err == nil {
		t.Fatal("mcp bad configs")
	}
	if _, err = tools.mcpCallTool(ctx, "run_eval", map[string]interface{}{}); err == nil {
		t.Fatal("mcp no collection")
	}

	// DELETE removes; deleting again reports nothing removed.
	if rec := call(http.MethodDelete, "/v1/eval-queries?collection=kb&id=b", ""); !strings.Contains(rec.Body.String(), `"deleted":true`) {
		t.Fatalf("delete: %s", rec.Body.String())
	}
}

func TestEval_ReadOnlyAndPermissions(t *testing.T) {
	s, cleanup := evalFixture(t)
	defer cleanup()
	s.Mode = ModeRead
	rec := httptest.NewRecorder()
	s.handleEvalQueries(rec, httptest.NewRequest(http.MethodPost, "/v1/eval-queries", strings.NewReader(`{}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-only add: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.handleEvalQueries(rec, httptest.NewRequest(http.MethodDelete, "/v1/eval-queries?collection=kb&id=x", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-only delete: %d", rec.Code)
	}

	// With auth on, an anonymous caller is refused on every route.
	s.Mode = ModeWrite
	t.Setenv("MDDB_AUTH_ENABLED", "true")
	am, amCleanup := authMwSetup(t)
	defer amCleanup()
	s.AuthManager = am
	for _, r := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/eval-queries?collection=kb", nil),
		httptest.NewRequest(http.MethodPost, "/v1/eval-queries", strings.NewReader(`{"collection":"kb"}`)),
		httptest.NewRequest(http.MethodDelete, "/v1/eval-queries?collection=kb&id=x", nil),
	} {
		rec = httptest.NewRecorder()
		s.handleEvalQueries(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s anonymous: %d", r.Method, rec.Code)
		}
	}
	if rec = doRequest(t, s.handleEval, map[string]any{"collection": "kb"}); rec.Code != http.StatusForbidden {
		t.Fatalf("eval anonymous: %d", rec.Code)
	}
}

func TestEval_SearchErrors(t *testing.T) {
	s, cleanup := evalFixture(t)
	defer cleanup()
	q := []EvalQuery{{Query: "backup", ExpectedKeys: []string{"backup.md"}}}
	res, err := s.RunEval(context.Background(), EvalRequest{Collection: "kb", Queries: q, Configs: []EvalConfig{
		{SearchType: SearchTypeFTS, Algorithm: "nope"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Results {
		if r.Error == "" {
			t.Fatalf("%s: %q", r.Config, r.Error)
		}
	}
}
