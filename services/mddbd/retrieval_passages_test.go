package main

import (
	"context"
	"strings"
	"testing"
	"time"

	gql "mddb/graphql"
	json "mddb/internal/jsonx"
)

// themeCSS is a 160-line stylesheet with one rule worth finding, as in #192.
func themeCSS() string {
	var b strings.Builder
	for i := 0; i < 150; i++ {
		b.WriteString(".filler-" + itoa(i) + " { margin: 0; }\n")
	}
	b.WriteString("\n.hero-banner {\n  background: url(hero.png);\n  padding: 2rem;\n}\n")
	for i := 0; i < 5; i++ {
		b.WriteString(".tail-" + itoa(i) + " { color: red; }\n")
	}
	return b.String()
}

func seedPassageRepo(t *testing.T, s *Server) {
	t.Helper()
	s.VectorIndex.SetReady()
	css := addTestDoc(t, s, "theme", "css/style.css", "en", themeCSS(), map[string][]string{"path": {"css/style.css"}})
	goDoc := addTestDoc(t, s, "theme", "app/run.go", "en", "package app\n\n// Run starts.\nfunc Run() {\n\tbackground()\n}\n",
		map[string][]string{"path": {"app/run.go"}})
	_ = s.VectorIndex.Add("theme", css.ID+"#0", []float32{0.2, 0.9, 0})
	_ = s.VectorIndex.Add("theme", css.ID+"#1", []float32{1, 0, 0})
	_ = s.VectorIndex.Add("theme", goDoc.ID+"#0", []float32{0.9, 0.1, 0})
}

func decodePassages(t *testing.T, body []byte) PassagesResponse {
	t.Helper()
	var p PassagesResponse
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode: %v %s", err, body)
	}
	return p
}

func TestPassages_Issue192OneCallUnder1KiB(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedPassageRepo(t, s)

	rec := doRequest(t, s.handleFTS, map[string]any{
		"collection": "theme", "query": "background", "limit": 1, "format": "passages",
		"filter": map[string]any{"path": map[string]any{"prefix": "css/"}},
	})
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	p := decodePassages(t, rec.Body.Bytes())
	if len(p.Passages) != 1 {
		t.Fatalf("passages: %+v", p)
	}
	got := p.Passages[0]
	if got.Path != "css/style.css" || got.Lines.Start != 152 || got.Lines.End != 155 || got.Symbol != ".hero-banner" ||
		!strings.Contains(got.Content, "background: url(hero.png)") || got.Source != "fts" || got.Tokens == 0 {
		t.Fatalf("passage: %+v", got)
	}
	t.Logf("#192: one call, %d bytes (whole document: %d bytes)", rec.Body.Len(), len(themeCSS()))
	if rec.Body.Len() >= 1024 {
		t.Fatalf("response is %d bytes, want < 1 KiB", rec.Body.Len())
	}
}

func TestPassages_SameShapeEverySurface(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedPassageRepo(t, s)
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}
	tools := &MCPToolServer{client: NewDirectClient(s)}
	ctx := context.Background()

	surfaces := map[string]func() []byte{
		"rest fts": func() []byte {
			return doRequest(t, s.handleFTS, map[string]any{"collection": "theme", "query": "background", "format": "passages"}).Body.Bytes()
		},
		"rest vector parent": func() []byte {
			return doRequest(t, s.handleVectorSearch, map[string]any{"collection": "theme", "queryVector": []float32{1, 0, 0}, "topK": 2, "format": "passages"}).Body.Bytes()
		},
		"rest vector chunk": func() []byte {
			return doRequest(t, s.handleVectorSearch, map[string]any{"collection": "theme", "queryVector": []float32{1, 0, 0}, "topK": 2,
				"retrievalMode": "chunk", "format": "passages"}).Body.Bytes()
		},
		"rest hybrid": func() []byte {
			return doRequest(t, s.handleHybridSearch, map[string]any{"collection": "theme", "query": "background", "topK": 2, "format": "passages"}).Body.Bytes()
		},
		"rest cross": func() []byte {
			return doRequest(t, s.handleCrossSearch, map[string]any{"query": "background", "queryVector": []float32{1, 0, 0},
				"targetCollections": []string{"theme"}, "topK": 2, "format": "passages"}).Body.Bytes()
		},
	}
	for name, tool := range map[string]func(context.Context, map[string]interface{}) (string, error){
		"mcp full_text_search": tools.toolFTSSearch, "mcp semantic_search": tools.toolSemanticSearch,
		"mcp hybrid_search": tools.toolHybridSearch, "mcp cross_search": tools.toolCrossSearch,
		"mcp search_documents": tools.toolSearchDocuments,
	} {
		tool := tool
		surfaces[name] = func() []byte {
			out, err := tool(ctx, map[string]interface{}{"collection": "theme", "query": "background", "top_k": 2,
				"target_collections": []interface{}{"theme"}, "format": "passages", "passage_lines": 3})
			if err != nil {
				t.Fatalf("%v", err)
			}
			return []byte(out)
		}
	}
	for name, run := range surfaces {
		t.Run(name, func(t *testing.T) {
			body := run()
			p := decodePassages(t, body)
			if len(p.Passages) == 0 || p.Total != len(p.Passages) || strings.Contains(string(body), `"results"`) {
				t.Fatalf("shape: %s", body)
			}
			for _, ps := range p.Passages {
				if ps.Collection != "theme" || ps.Path == "" || ps.Lines.Start < 1 || ps.Lines.End < ps.Lines.Start ||
					ps.Content == "" || ps.Tokens == 0 || ps.Source == "" {
					t.Fatalf("passage: %+v", ps)
				}
			}
		})
	}

	// The best chunk is where a parent-mode vector passage is cut.
	p := decodePassages(t, surfaces["rest vector parent"]())
	if p.Passages[0].Path != "css/style.css" || p.Passages[0].Lines.Start == 1 {
		t.Fatalf("parent mode cuts the best chunk (#1), got %+v", p.Passages[0])
	}
}

func TestPassages_AbsentFormatIsUnchangedAndValidation(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedPassageRepo(t, s)

	// One match, so ranking ties cannot reorder the two responses.
	before := doRequest(t, s.handleFTS, map[string]any{"collection": "theme", "query": "hero"}).Body.String()
	if !strings.Contains(before, `"results"`) || strings.Contains(before, `"passages"`) {
		t.Fatalf("no format: %s", before)
	}
	explicit := doRequest(t, s.handleFTS, map[string]any{"collection": "theme", "query": "hero", "format": "results"}).Body.String()
	if stripStats(explicit) != stripStats(before) {
		t.Fatal(`format "results" must equal no format`)
	}
	for name, code := range map[string]int{
		"fts":    doRequest(t, s.handleFTS, map[string]any{"collection": "theme", "query": "x", "format": "xml"}).Code,
		"vector": doRequest(t, s.handleVectorSearch, map[string]any{"collection": "theme", "queryVector": []float32{1, 0, 0}, "format": "xml"}).Code,
		"hybrid": doRequest(t, s.handleHybridSearch, map[string]any{"collection": "theme", "query": "x", "passageLines": -1, "format": "passages"}).Code,
		"cross":  doRequest(t, s.handleCrossSearch, map[string]any{"query": "x", "targetCollections": []string{"theme"}, "format": "xml"}).Code,
	} {
		if code != 400 && code != 422 {
			t.Errorf("%s: invalid format accepted (%d)", name, code)
		}
	}
	dc := NewDirectClient(s)
	if _, err := dc.Search(context.Background(), &MCPSearchRequest{Collection: "theme", PassageOptions: PassageOptions{Format: "xml"}}); err == nil {
		t.Fatal("MCP search: invalid format")
	}
	if _, err := dc.CrossSearch(context.Background(), &MCPCrossSearchRequest{TargetCollections: []string{"theme"}, PassageOptions: PassageOptions{Format: "xml"}}); err == nil {
		t.Fatal("MCP cross: invalid format")
	}
	if _, err := dc.HybridSearch(context.Background(), &MCPHybridSearchRequest{Collection: "theme", Query: "x", PassageOptions: PassageOptions{Format: "xml"}}); err == nil {
		t.Fatal("MCP hybrid: invalid format")
	}
	if _, err := dc.VectorSearch(context.Background(), &MCPVectorSearchRequest{Collection: "theme", Query: "x", PassageOptions: PassageOptions{Format: "xml"}}); err == nil {
		t.Fatal("MCP vector: invalid format")
	}
}

// stripStats drops the timing that differs between two identical searches.
func stripStats(body string) string {
	if i := strings.Index(body, `"searchStats"`); i >= 0 {
		return body[:i]
	}
	return body
}

func TestPassages_ProfileDefaultAndWindow(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedPassageRepo(t, s)
	if err := s.CollectionManager.Set("theme", &CollectionConfig{Retrieval: &RetrievalProfileDef{Format: FormatPassages}}); err != nil {
		t.Fatal(err)
	}
	resp, err := s.searchFTS(FTSSearchRequest{Collection: "theme", Query: "background"}, time.Now())
	if err != nil || resp.passages == nil {
		t.Fatalf("profile format: %v %+v", err, resp)
	}
	if err := (&RetrievalProfileDef{Format: "xml"}).Validate(); err == nil {
		t.Fatal("profile with an unknown format")
	}

	// A hit outside any definition gets the window; radius 2 → 5 lines.
	doc := addTestDoc(t, s, "notes", "n.md", "en", "a\nb\nc\nd\ne\nf\ng\nh\n", nil)
	p := passageAround("notes", &doc, 4, 2, 0.5, "fts")
	if p.Lines.Start != 2 || p.Lines.End != 6 || p.Content != "b\nc\nd\ne\nf" || p.Score != 0.5 {
		t.Fatalf("window: %+v", p)
	}
	// A definition longer than the window is not returned whole.
	long := addTestDoc(t, s, "notes", "big.go", "en", "package x\n\nfunc Big() {\n"+strings.Repeat("\tx()\n", 40)+"}\n", nil)
	p = passageAround("notes", &long, 20, 3, 0, "fts")
	if p.Lines.End-p.Lines.Start != 6 || p.Symbol != "Big" {
		t.Fatalf("long definition: %+v", p)
	}
	if (PassageOptions{PassageLines: 5000}).radius() != maxPassageLines || queryTerms(`"a" Bc d.`)[0] != "bc" {
		t.Fatal("radius/queryTerms")
	}
	if got := s.finishPassages("notes", nil, nil); got.Passages == nil || got.Total != 0 {
		t.Fatal("empty passages render as []")
	}
}

func TestPassages_GraphQL(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedPassageRepo(t, s)
	adapter := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	format := "passages"
	f, err := adapter.FullTextSearch(context.Background(), gql.FTSInput{Collection: "theme", Query: "background", Format: &format})
	if err != nil || len(f.Passages) != 2 || len(f.Results) != 0 {
		t.Fatalf("fts: %v %+v", err, f)
	}
	for _, p := range f.Passages {
		if p.Path == "app/run.go" && (p.Lines.Start != 4 || p.Symbol == nil || *p.Symbol != "Run") {
			t.Fatalf("go passage: %+v", p)
		}
	}
	v, err := adapter.VectorSearch(context.Background(), gql.VectorSearchInput{Collection: "theme", QueryVector: []float64{1, 0, 0}, Format: &format})
	if err != nil || len(v.Passages) == 0 || len(v.Results) != 0 {
		t.Fatalf("vector: %v %+v", err, v)
	}
}

func TestHitLine(t *testing.T) {
	cases := []struct {
		content string
		terms   []string
		want    int
	}{
		{"a\nfoo bar\nbar", []string{"bar"}, 2},
		{"classes here\nclass", []string{"class"}, 2},             // whole word first
		{"many classes", []string{"class"}, 1},                    // then a stem
		{"x\ny\njavascript", []string{"javascrip~javascript"}, 3}, // fuzzy: indexed form
		{"nothing", []string{"zzz", ""}, 1},
	}
	for _, tc := range cases {
		if got := hitLine(tc.content, tc.terms); got != tc.want {
			t.Errorf("hitLine(%q, %v) = %d, want %d", tc.content, tc.terms, got, tc.want)
		}
	}
}

func TestPassages_WithHighlightsAndBudget(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedPassageRepo(t, s)
	rec := doRequest(t, s.handleFTS, map[string]any{"collection": "theme", "query": "padding", "highlight": true, "format": "passages"})
	p := decodePassages(t, rec.Body.Bytes())
	if len(p.Passages) != 1 || p.Passages[0].Symbol != ".hero-banner" {
		t.Fatalf("highlighted hit: %+v", p)
	}
	// The budget counts passages, not documents.
	if err := s.CollectionManager.Set("theme", &CollectionConfig{Retrieval: &RetrievalProfileDef{ContextTokenBudget: 1}}); err != nil {
		t.Fatal(err)
	}
	rec = doRequest(t, s.handleFTS, map[string]any{"collection": "theme", "query": "background", "format": "passages"})
	if p = decodePassages(t, rec.Body.Bytes()); !p.ContextTruncated {
		t.Fatalf("budget: %+v", p)
	}
}
