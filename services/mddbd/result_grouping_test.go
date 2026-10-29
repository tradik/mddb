package main

import (
	"context"
	"strings"
	"testing"
	"time"

	gql "mddb/graphql"
	json "mddb/internal/jsonx"
	vec "mddb/internal/vector"
	proto "mddb/proto"
)

// seedGroupedRepo: a.go has three chunk vectors, b.go one, c.md one; two
// authors, and one document without an author.
func seedGroupedRepo(t *testing.T, s *Server) {
	t.Helper()
	s.VectorIndex.SetReady()
	docs := []struct {
		key, path, author string
		vecs              [][]float32
	}{
		{"a", "a.go", "ann", [][]float32{{1, 0, 0}, {0.99, 0.01, 0}, {0.98, 0.02, 0}}},
		{"b", "b.go", "ann", [][]float32{{0.97, 0.03, 0}}},
		{"c", "c.md", "bob", [][]float32{{0.96, 0.04, 0}}},
		{"d", "d.md", "", [][]float32{{0.95, 0.05, 0}}},
		{"e", "e.md", "", [][]float32{{0.94, 0.06, 0}}},
	}
	for _, d := range docs {
		meta := map[string][]string{"path": {d.path}}
		if d.author != "" {
			meta["author"] = []string{d.author}
		}
		doc := addTestDoc(t, s, "repo", d.key, "en", "retry logic "+d.key, meta)
		for i, v := range d.vecs {
			if err := s.VectorIndex.Add("repo", doc.ID+"#"+string(rune('0'+i)), v); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func vectorSearch(t *testing.T, s *Server, req VectorSearchRequest) VectorSearchResponseHTTP {
	t.Helper()
	rec := doRequest(t, s.handleVectorSearch, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp VectorSearchResponseHTTP
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestGroupBy_VectorChunkModePerFile(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedGroupedRepo(t, s)

	resp := vectorSearch(t, s, VectorSearchRequest{
		Collection: "repo", QueryVector: []float32{1, 0, 0}, TopK: 10, RetrievalMode: "chunk",
		GroupBy: &GroupBy{Key: "path", PerGroup: 2},
	})
	perFile := map[string]int{}
	for _, r := range resp.Results {
		perFile[r.Group]++
		if r.GroupRank < 1 || r.GroupRank > 2 {
			t.Fatalf("groupRank %d", r.GroupRank)
		}
	}
	if perFile["a.go"] != 2 || perFile["b.go"] != 1 || len(resp.Results) != 6 {
		t.Fatalf("per file: %v (total %d)", perFile, len(resp.Results))
	}
	if resp.Results[0].Group != "a.go" || resp.Results[0].GroupRank != 1 || resp.Results[1].GroupRank != 2 {
		t.Fatalf("order: %+v", resp.Results[:2])
	}

	// document grouping in chunk mode: up to perGroup chunks of one document.
	resp = vectorSearch(t, s, VectorSearchRequest{
		Collection: "repo", QueryVector: []float32{1, 0, 0}, TopK: 2, RetrievalMode: "chunk",
		GroupBy: &GroupBy{Key: "document", PerGroup: 3},
	})
	if len(resp.Results) != 2 || resp.Results[1].GroupRank != 2 {
		t.Fatalf("document grouping: %+v", resp.Results)
	}
}

func TestGroupBy_VectorParentModeMissingKeyStaysApart(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedGroupedRepo(t, s)

	resp := vectorSearch(t, s, VectorSearchRequest{
		Collection: "repo", QueryVector: []float32{1, 0, 0}, TopK: 10,
		GroupBy: &GroupBy{Key: "author"},
	})
	var keys []string
	for _, r := range resp.Results {
		keys = append(keys, r.Document.Key+":"+r.Group)
	}
	// ann keeps a (best), bob keeps c; d and e lack the key and stay apart.
	if strings.Join(keys, ",") != "a:ann,c:bob,d:,e:" {
		t.Fatalf("got %v", keys)
	}
}

func TestGroupBy_AbsentIsUnchanged(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedGroupedRepo(t, s)
	rec := doRequest(t, s.handleVectorSearch, VectorSearchRequest{
		Collection: "repo", QueryVector: []float32{1, 0, 0}, TopK: 10, RetrievalMode: "chunk",
	})
	if strings.Contains(rec.Body.String(), `"group"`) || strings.Contains(rec.Body.String(), `"groupRank"`) {
		t.Fatalf("no groupBy must add no fields: %s", rec.Body.String())
	}
	var resp VectorSearchResponseHTTP
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Results) != 7 {
		t.Fatalf("chunk mode without groupBy returns every chunk, got %d", len(resp.Results))
	}
}

func TestGroupBy_Validation(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedGroupedRepo(t, s)
	for _, g := range []*GroupBy{{Key: ""}, {Key: "path", PerGroup: -1}, {Key: "path", PerGroup: maxPerGroup + 1}} {
		for name, h := range map[string]func(*GroupBy) int{
			"vector": func(g *GroupBy) int {
				return doRequest(t, s.handleVectorSearch, VectorSearchRequest{Collection: "repo", QueryVector: []float32{1, 0, 0}, GroupBy: g}).Code
			},
			"hybrid": func(g *GroupBy) int {
				return doRequest(t, s.handleHybridSearch, HybridSearchRequest{Collection: "repo", Query: "retry", GroupBy: g}).Code
			},
			"cross": func(g *GroupBy) int {
				return doRequest(t, s.handleCrossSearch, CrossSearchRequest{Query: "x", TargetCollections: []string{"repo"}, GroupBy: g}).Code
			},
			"fts": func(g *GroupBy) int {
				return doRequest(t, s.handleFTS, FTSSearchRequest{Collection: "repo", Query: "retry", GroupBy: g}).Code
			},
		} {
			if code := h(g); code != 422 && code != 400 {
				t.Errorf("%s %+v: status %d, want 4xx", name, g, code)
			}
		}
	}
}

func TestGroupBy_FTSAndProfileDefault(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedGroupedRepo(t, s)

	resp, err := s.searchFTS(FTSSearchRequest{Collection: "repo", Query: "retry", Limit: 10,
		GroupBy: &GroupBy{Key: "author"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]int{}
	for _, r := range resp.Results {
		groups[r.Group]++
	}
	if groups["ann"] != 1 || groups["bob"] != 1 || groups[""] != 2 {
		t.Fatalf("fts groups: %v", groups)
	}

	// Collection profile supplies the default; a request still overrides it.
	if err := s.CollectionManager.Set("repo", &CollectionConfig{
		Retrieval: &RetrievalProfileDef{GroupBy: &GroupBy{Key: "author"}},
	}); err != nil {
		t.Fatal(err)
	}
	resp, _ = s.searchFTS(FTSSearchRequest{Collection: "repo", Query: "retry", Limit: 10}, time.Now())
	if len(resp.Results) != 4 {
		t.Fatalf("profile groupBy: %d results", len(resp.Results))
	}
	resp, _ = s.searchFTS(FTSSearchRequest{Collection: "repo", Query: "retry", Limit: 10,
		GroupBy: &GroupBy{Key: "path"}}, time.Now())
	if len(resp.Results) != 5 {
		t.Fatalf("request overrides profile: %d results", len(resp.Results))
	}
	if err := (&RetrievalProfileDef{GroupBy: &GroupBy{}}).Validate(); err == nil {
		t.Fatal("profile with an empty groupBy key must be rejected")
	}
}

func TestGroupBy_HybridAndCross(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedGroupedRepo(t, s)
	s.Embedding = &mockEmbedding{dims: 3, model: "test-model"}

	rec := doRequest(t, s.handleHybridSearch, HybridSearchRequest{
		Collection: "repo", Query: "retry", TopK: 10, GroupBy: &GroupBy{Key: "author"},
	})
	var hyb HybridSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &hyb); err != nil || rec.Code != 200 {
		t.Fatalf("hybrid: %d %s", rec.Code, rec.Body.String())
	}
	seen := map[string]int{}
	for i, r := range hyb.Results {
		seen[r.Group]++
		if r.Rank != i+1 {
			t.Fatalf("hybrid ranks not renumbered: %+v", hyb.Results)
		}
	}
	if seen["ann"] != 1 || seen["bob"] != 1 {
		t.Fatalf("hybrid groups: %v", seen)
	}

	addTestDoc(t, s, "notes", "n1", "en", "retry note", map[string][]string{"author": {"ann"}})
	doc, _ := s.loadDocByRef("notes", "n1", "en")
	_ = s.VectorIndex.Add("notes", doc.ID, []float32{1, 0, 0})
	rec = doRequest(t, s.handleCrossSearch, CrossSearchRequest{
		QueryVector: []float32{1, 0, 0}, TargetCollections: []string{"repo", "notes"}, TopK: 10,
		GroupBy: &GroupBy{Key: "collection"},
	})
	var cross CrossSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cross); err != nil || rec.Code != 200 {
		t.Fatalf("cross: %d %s", rec.Code, rec.Body.String())
	}
	if len(cross.Results) != 2 || cross.Results[0].Group == cross.Results[1].Group {
		t.Fatalf("one per collection: %+v", cross.Results)
	}

	// MCP surfaces carry the same parameter.
	tools := &MCPToolServer{client: NewDirectClient(s)}
	ctx := context.Background()
	for name, fn := range map[string]func(context.Context, map[string]interface{}) (string, error){
		"semantic_search":  tools.toolSemanticSearch,
		"full_text_search": tools.toolFTSSearch,
		"hybrid_search":    tools.toolHybridSearch,
		"cross_search":     tools.toolCrossSearch,
	} {
		out, err := fn(ctx, map[string]interface{}{
			"collection": "repo", "query": "retry", "top_k": 10, "limit": 10,
			"target_collections": []interface{}{"repo", "notes"},
			"group_by":           map[string]interface{}{"key": "author", "perGroup": 1},
		})
		if err != nil || !strings.Contains(out, `"groupRank": 1`) {
			t.Errorf("%s: %v %s", name, err, out)
		}
		if _, err := fn(ctx, map[string]interface{}{
			"collection": "repo", "query": "retry", "target_collections": []interface{}{"repo"},
			"group_by": map[string]interface{}{"per_group": 2},
		}); err == nil {
			t.Errorf("%s: group_by without key must fail", name)
		}
	}
}

func TestGroupBy_ConversionHelpers(t *testing.T) {
	if groupByFromGQL(nil) != nil {
		t.Fatal("nil GraphQL input")
	}
	two := 2
	if g := groupByFromGQL(&gql.GroupByInput{Key: "path", PerGroup: &two}); g.Key != "path" || g.PerGroup != 2 {
		t.Fatalf("gql: %+v", g)
	}
	p := retrievalProfileToProto(&RetrievalProfileDef{GroupBy: &GroupBy{Key: "path", PerGroup: 3}})
	if p.GroupByKey != "path" || p.GroupByPerGroup != 3 {
		t.Fatalf("to proto: %+v", p)
	}
	back := retrievalProfileFromProto(p)
	if back.GroupBy == nil || back.GroupBy.Key != "path" || back.GroupBy.PerGroup != 3 {
		t.Fatalf("from proto: %+v", back.GroupBy)
	}
	if retrievalProfileFromProto(&proto.RetrievalProfileProto{}).GroupBy != nil {
		t.Fatal("empty key means no groupBy")
	}
	if groupByKey(nil) != "" || groupByPerGroup(nil) != 0 {
		t.Fatal("nil groupBy to proto")
	}
	if (&GroupBy{Key: "k"}).perGroup() != 1 {
		t.Fatal("perGroup default")
	}
	var nilG *GroupBy
	if nilG.Validate() != nil || groupTop([]int{1}, nil, nil, nil, nil)[0] != 1 {
		t.Fatal("nil groupBy is a no-op")
	}
	if got := groupHybridItems("c", []HybridSearchResultItem{{}}, nil, 1); len(got) != 1 {
		t.Fatal("nil groupBy on hybrid items")
	}
	if got := groupedCandidates(2); got != 50 {
		t.Fatalf("groupedCandidates(2) = %d", got)
	}
}

func TestCrossSearchItems_FilterAndTruncation(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedGroupedRepo(t, s)
	addTestDoc(t, s, "notes", "n1", "en", "retry note", map[string][]string{"author": {"ann"}})
	doc, _ := s.loadDocByRef("notes", "n1", "en")
	_ = s.VectorIndex.Add("notes", doc.ID, []float32{1, 0, 0})
	// A vector whose document is gone is skipped, not returned empty.
	_ = s.VectorIndex.Add("notes", "notes|ghost|en", []float32{1, 0, 0})

	p := crossSearchParams{
		Searcher: s.VectorSearchers["flat"], QueryVector: []float32{1, 0, 0},
		Targets: []string{"repo", "notes"}, SearchTopK: 20, TopK: 2,
		Metric: vec.ResolveSimilarity("cosine"), Filter: MetaFilter{"author": {In: []string{"bob"}, hasIn: true}},
	}
	items := s.crossSearchItems(p)
	if len(items) != 1 || items[0].Document.Key != "c" || items[0].Document.ContentMD != "" {
		t.Fatalf("filter excludes notes entirely and keeps bob: %+v", items)
	}
	p.Filter, p.GroupBy = nil, &GroupBy{Key: "author"}
	items = s.crossSearchItems(p)
	if len(items) != 2 || items[0].Rank != 1 || items[1].Rank != 2 {
		t.Fatalf("grouped and cut to topK: %+v", items)
	}
	p.GroupBy, p.IncludeContent = nil, true
	items = s.crossSearchItems(p)
	if len(items) != 2 || items[0].Document.ContentMD == "" {
		t.Fatalf("ungrouped topK with content: %+v", items)
	}
}
