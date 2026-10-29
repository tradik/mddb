package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gql "mddb/graphql"
	json "mddb/internal/jsonx"
)

// seedSymbolRepo stores a tiny Go package, its test, a caller elsewhere and a
// stylesheet with the page that applies it.
func seedSymbolRepo(t *testing.T, s *Server) {
	t.Helper()
	files := map[string]string{
		"internal/search/query.go":      "package search\n\n// SearchHybrid ranks.\nfunc SearchHybrid(q string) []int {\n\ta := tokenize(q)\n\treturn score(a)\n}\n\nfunc tokenize(q string) []string {\n\treturn nil\n}\n",
		"internal/search/score.go":      "package search\n\nfunc score(a []string) []int {\n\treturn nil\n}\n\ntype Ranker struct {\n\tn int\n}\n",
		"internal/search/query_test.go": "package search\n\nfunc TestSearchHybrid(t *testing.T) {\n\tSearchHybrid(\"x\")\n\tif SearchHybrid(\"y\") == nil {\n\t}\n}\n",
		"cmd/tool.go":                   "package main\n\nfunc run() {\n\tSearchHybrid(\"q\")\n}\n",
		"theme/site.css":                "body { margin: 0 }\n\n.hero-banner {\n  color: red;\n}\n",
		"theme/index.html":              "<html>\n<body>\n<div class=\"hero-banner\">Hi</div>\n</body>\n</html>\n",
		"docs/readme.md":                "SearchHybrid is documented here.\n",
	}
	for key, content := range files {
		addTestDoc(t, s, "repo", key, "en", content, nil)
	}
}

func lookup(t *testing.T, s *Server, req SymbolRequest) *SymbolResult {
	t.Helper()
	res, err := s.LookupSymbol(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func passageKeys(ps []Passage) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Path+":"+itoa(p.Lines.Start)+"-"+itoa(p.Lines.End)+"@"+p.Symbol)
	}
	return strings.Join(out, " ")
}

func TestLookupSymbol_GoFunction(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSymbolRepo(t, s)

	res := lookup(t, s, SymbolRequest{Collection: "repo", Name: "SearchHybrid", IncludeContent: true})
	if got := passageKeys(res.Definitions); got != "internal/search/query.go:4-7@SearchHybrid" {
		t.Fatalf("definitions: %s", got)
	}
	if d := res.Definitions[0]; d.Kind != "function" || !strings.Contains(d.Content, "return score(a)") || d.Tokens == 0 || d.Source != RelDefinitions {
		t.Fatalf("definition passage: %+v", d)
	}
	if got := passageKeys(res.References); got != "cmd/tool.go:4-4@run internal/search/query_test.go:4-4@TestSearchHybrid internal/search/query_test.go:5-5@TestSearchHybrid" {
		t.Fatalf("references: %s", got)
	}
	if res.References[0].Content != `SearchHybrid("q")` {
		t.Fatalf("reference carries its line: %q", res.References[0].Content)
	}
	if got := passageKeys(res.Callers); got != "cmd/tool.go:3-5@run internal/search/query_test.go:3-7@TestSearchHybrid" {
		t.Fatalf("callers: %s", got)
	}
	if got := passageKeys(res.Tests); got != "internal/search/query_test.go:4-4@TestSearchHybrid internal/search/query_test.go:5-5@TestSearchHybrid" {
		t.Fatalf("tests: %s", got)
	}
	if got := passageKeys(res.Callees); got != "internal/search/score.go:3-5@score internal/search/query.go:9-11@tokenize" {
		t.Fatalf("callees: %s", got)
	}
	for _, want := range []string{"@Ranker", "@score", "@tokenize", "@TestSearchHybrid"} {
		if !strings.Contains(passageKeys(res.Siblings), want) {
			t.Fatalf("siblings miss %s: %s", want, passageKeys(res.Siblings))
		}
	}
	if strings.Contains(passageKeys(res.Siblings), "@SearchHybrid") || strings.Contains(passageKeys(res.Siblings), "cmd/") {
		t.Fatalf("siblings: %s", passageKeys(res.Siblings))
	}

	// Determinism: a second lookup is identical.
	again := lookup(t, s, SymbolRequest{Collection: "repo", Name: "SearchHybrid", IncludeContent: true})
	a, _ := json.Marshal(res)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatal("lookup is not deterministic")
	}
}

func TestLookupSymbol_IncludeLimitAndCSS(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSymbolRepo(t, s)

	res := lookup(t, s, SymbolRequest{Collection: "repo", Name: "SearchHybrid", Include: []string{RelReferences}, Limit: 1})
	if len(res.Definitions) != 0 || len(res.References) != 1 || !res.Truncated || res.Callers != nil || res.Siblings != nil {
		t.Fatalf("include+limit: %+v", res)
	}

	css := lookup(t, s, SymbolRequest{Collection: "repo", Name: ".hero-banner"})
	if got := passageKeys(css.Definitions); got != "theme/site.css:3-5@.hero-banner" {
		t.Fatalf("css definition: %s", got)
	}
	if got := passageKeys(css.References); got != "theme/index.html:3-3@" {
		t.Fatalf("css reference (bare class name in HTML): %s", got)
	}

	if _, err := s.LookupSymbol(SymbolRequest{Collection: "repo"}); err == nil {
		t.Fatal("missing name")
	}
	if _, err := s.LookupSymbol(SymbolRequest{Collection: "repo", Name: "x", Include: []string{"implementations"}}); err == nil {
		t.Fatal("unknown relation")
	}
	none := lookup(t, s, SymbolRequest{Collection: "empty", Name: "x", Limit: 999})
	if len(none.Definitions) != 0 || none.References != nil {
		t.Fatalf("unknown symbol: %+v", none)
	}
}

func TestLookupSymbol_Surfaces(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSymbolRepo(t, s)

	rec := httptest.NewRecorder()
	s.handleSymbol(rec, httptest.NewRequest(http.MethodGet,
		"/v1/symbol?collection=repo&name=SearchHybrid&include=definitions,tests&limit=5&includeContent=true", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"path":"internal/search/query.go"`) || strings.Contains(rec.Body.String(), `"callers"`) {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, s.handleSymbol, SymbolRequest{Collection: "repo", Name: "tokenize"})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"symbol":"tokenize"`) {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body.String())
	}
	for body, want := range map[string]int{`{"collection":"repo"}`: 400, `{"collection":"repo","name":"x","include":["nope"]}`: 422, `not json`: 400} {
		rec = httptest.NewRecorder()
		s.handleSymbol(rec, httptest.NewRequest(http.MethodPost, "/v1/symbol", strings.NewReader(body)))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", body, rec.Code, want)
		}
	}
	rec = httptest.NewRecorder()
	s.handleSymbol(rec, httptest.NewRequest(http.MethodPut, "/v1/symbol", nil))
	if rec.Code != 405 {
		t.Fatalf("PUT: %d", rec.Code)
	}

	tools := &MCPToolServer{client: NewDirectClient(s)}
	out, err := tools.toolCodeSymbol(context.Background(), map[string]interface{}{
		"collection": "repo", "name": "SearchHybrid", "include": []interface{}{"callers"}, "limit": 5,
	})
	if err != nil || !strings.Contains(out, `"symbol": "run"`) {
		t.Fatalf("MCP: %v %s", err, out)
	}
	if _, err := tools.toolCodeSymbol(context.Background(), map[string]interface{}{"collection": "repo"}); err == nil {
		t.Fatal("MCP without name")
	}

	adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	limit, content := 5, true
	g, err := adapter.CodeSymbol(context.Background(), gql.CodeSymbolInput{Collection: "repo", Name: "SearchHybrid", Limit: &limit, IncludeContent: &content})
	if err != nil || len(g.Definitions) != 1 || g.Definitions[0].Lines.Start != 4 || g.Definitions[0].Symbol == nil || len(g.Callers) != 2 {
		t.Fatalf("GraphQL: %v %+v", err, g)
	}
	if _, err := adapter.CodeSymbol(context.Background(), gql.CodeSymbolInput{Collection: "repo"}); err == nil {
		t.Fatal("GraphQL without name")
	}
	if p := passagesToGQL([]Passage{{Score: 0.5}}); p[0].Score == nil || *p[0].Score != 0.5 {
		t.Fatal("score")
	}
}

func TestGraphExpand_SymbolKinds(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSymbolRepo(t, s)
	seeds := []graphSeed{{Key: "internal/search/query.go", Score: 1}}

	kinds := func(k ...string) map[string]string {
		out := map[string]string{}
		for _, e := range s.expandByGraph("repo", seeds, GraphExpandOptions{Kinds: k}) {
			out[e.Key] = e.Kind
		}
		return out
	}
	if got := kinds(RelTests); len(got) != 1 || got["internal/search/query_test.go"] != RelTests {
		t.Fatalf("tests: %v", got)
	}
	if got := kinds(RelCallers); got["cmd/tool.go"] != RelCallers || got["internal/search/query_test.go"] != RelCallers {
		t.Fatalf("callers: %v", got)
	}
	if got := kinds(RelCallees); got["internal/search/score.go"] != RelCallees {
		t.Fatalf("callees: %v", got)
	}
	if got := kinds(RelSiblings); got["internal/search/score.go"] != RelSiblings || got["cmd/tool.go"] != "" {
		t.Fatalf("siblings: %v", got)
	}
	if err := (GraphExpandOptions{Kinds: []string{"implementations"}}).ValidateKinds(); err == nil {
		t.Fatal("unknown kind")
	}
	rec := doRequest(t, s.handleVectorSearch, VectorSearchRequest{Collection: "repo", QueryVector: []float32{1, 0, 0},
		RetrievalMode: "graph", GraphExpand: GraphExpandOptions{Kinds: []string{"nope"}}})
	if rec.Code != 422 {
		t.Fatalf("vector search with an unknown kind: %d", rec.Code)
	}
	if !isTestPath("spec/models/user_spec.rb") || !isTestPath("src/__tests__/a.js") || isTestPath("internal/search/query.go") {
		t.Fatal("isTestPath")
	}
}

func TestLookupSymbol_SameFileCallerAndMethods(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedSymbolRepo(t, s)
	// tokenize is called from SearchHybrid in its own file: the extractor
	// leaves own names out of uses, the full-text index still finds it.
	res := lookup(t, s, SymbolRequest{Collection: "repo", Name: "tokenize", Include: []string{RelCallers}})
	if got := passageKeys(res.Callers); got != "internal/search/query.go:4-7@SearchHybrid" {
		t.Fatalf("same-file caller: %s", got)
	}
	// A method called through a value: s.Len() is not in uses either.
	addTestDoc(t, s, "repo", "pkg/a.go", "en", "package pkg\n\ntype T struct{}\n\nfunc (t T) Len() int { return 0 }\n", nil)
	addTestDoc(t, s, "repo", "pkg/b.go", "en", "package pkg\n\nfunc use(t T) int {\n\treturn t.Len()\n}\n", nil)
	res = lookup(t, s, SymbolRequest{Collection: "repo", Name: "Len", Include: []string{RelReferences}})
	if got := passageKeys(res.References); got != "pkg/b.go:4-4@use" {
		t.Fatalf("method reference: %s", got)
	}
}

func TestLookupSymbol_Edges(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	// HTML has no spans: an id is placed on the line it first appears.
	addTestDoc(t, s, "web", "page.html", "en", "<html>\n<div id=\"main\">x</div>\n</html>\n", nil)
	res := lookup(t, s, SymbolRequest{Collection: "web", Name: "#main", Include: []string{RelDefinitions}})
	if got := passageKeys(res.Definitions); got != "page.html:2-2@" {
		t.Fatalf("html id: %s", got)
	}
	// meta.path wins over the key as the passage path.
	doc := addTestDoc(t, s, "web", "k1", "en", "func A() {}\n", map[string][]string{"path": {"src/a.go"}, "kind": {"code"}, "language": {"go"}})
	if docPath(&doc) != "src/a.go" {
		t.Fatalf("docPath: %s", docPath(&doc))
	}
	st := newSourceText(&doc)
	if st.text(5, 9) != "" || st.text(0, 1) != "func A() {}" {
		t.Fatal("text clamps")
	}
	if lineOf("a\nb", 3) != "" || lineOf("a\nb", 2) != "b" {
		t.Fatal("lineOf")
	}
	if err := (GraphExpandOptions{Kinds: []string{RelTests, RelSiblings}}).ValidateKinds(); err != nil {
		t.Fatal(err)
	}
}

func TestCodeLanguagesEndpoint(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	rec := httptest.NewRecorder()
	s.handleCodeLanguages(rec, httptest.NewRequest(http.MethodGet, "/v1/code-languages", nil))
	var resp CodeLanguagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Extensions[".go"] != "go" || resp.Extensions[".tsx"] != "typescript" {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	for i := 1; i < len(resp.Languages); i++ {
		if resp.Languages[i-1] >= resp.Languages[i] {
			t.Fatalf("languages not sorted and unique: %v", resp.Languages)
		}
	}
	rec = httptest.NewRecorder()
	s.handleCodeLanguages(rec, httptest.NewRequest(http.MethodPost, "/v1/code-languages", nil))
	if rec.Code != 405 {
		t.Fatalf("POST: %d", rec.Code)
	}
}
