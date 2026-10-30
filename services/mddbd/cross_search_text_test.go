package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	proto "mddb/proto"
)

// seedReferences: two reference collections and one ordinary one.
func seedReferences(t *testing.T, s *Server) {
	t.Helper()
	for _, c := range []string{"ref-a", "ref-b"} {
		if err := s.CollectionManager.Set(c, &CollectionConfig{Code: &CodeConfigDef{Role: CodeRoleReference}, Description: "Reference: " + c}); err != nil {
			t.Fatal(err)
		}
	}
	addTestDoc(t, s, "ref-a", "tree/btree.go", "en", "package tree\n\n// Delete removes a key.\nfunc Delete(k int) {\n\trebalance(k)\n}\n\nfunc rebalance(k int) {}\n", nil)
	addTestDoc(t, s, "ref-a", "tree/btree_test.go", "en", "package tree\n\nfunc TestDelete(t *testing.T) {\n\tDelete(1)\n}\n", nil)
	addTestDoc(t, s, "ref-a", "tree/node.go", "en", "package tree\n\nimport \"tree/btree.go\"\n\nfunc split() {}\n", nil)
	addTestDoc(t, s, "ref-b", "hnsw/graph.go", "en", "package hnsw\n\n// Delete marks a node deleted and repairs neighbours.\nfunc Delete(id int) {\n\trepair(id)\n}\n\nfunc repair(id int) {}\n", nil)
	addTestDoc(t, s, "mine", "app.go", "en", "package app\n\nfunc Delete() {}\n", nil)
}

func TestExpandTargets(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedReferences(t, s)
	got := s.expandTargets([]string{"mine", "role:reference", "ref-a", "role:nothing"})
	if strings.Join(got, ",") != "mine,ref-a,ref-b" {
		t.Fatalf("expand: %v", got)
	}
}

func TestCrossSearch_TextTypes(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedReferences(t, s)
	dc := NewDirectClient(s)
	ctx := context.Background()

	// Hybrid over one collection gives hybrid_search's order (RRF of one list).
	single, err := dc.HybridSearch(ctx, &MCPHybridSearchRequest{Collection: "ref-a", Query: "delete rebalance", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	cross, err := dc.CrossSearch(ctx, &MCPCrossSearchRequest{Query: "delete rebalance", TargetCollections: []string{"ref-a"},
		SearchType: CrossSearchHybrid, TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Results) == 0 || len(cross.Results) != len(single.Results) {
		t.Fatalf("lengths: %d vs %d", len(single.Results), len(cross.Results))
	}
	for i := range single.Results {
		if single.Results[i].Document.Key != cross.Results[i].Document.Key {
			t.Fatalf("rank %d: %s vs %s", i, single.Results[i].Document.Key, cross.Results[i].Document.Key)
		}
	}

	// FTS across every reference collection, fused by rank.
	fts, err := dc.CrossSearch(ctx, &MCPCrossSearchRequest{Query: "delete", TargetCollections: []string{"role:reference"},
		SearchType: CrossSearchFTS, TopK: 10, IncludeContent: true})
	if err != nil {
		t.Fatal(err)
	}
	colls := map[string]bool{}
	for i, r := range fts.Results {
		colls[r.Collection] = true
		if r.Rank != i+1 || r.Document.ContentMD == "" {
			t.Fatalf("result %d: %+v", i, r)
		}
	}
	if !colls["ref-a"] || !colls["ref-b"] || colls["mine"] || fts.Algorithm != CrossSearchFTS {
		t.Fatalf("collections: %v (%s)", colls, fts.Algorithm)
	}

	// Grouped and as passages.
	grouped, _ := dc.CrossSearch(ctx, &MCPCrossSearchRequest{Query: "delete", TargetCollections: []string{"role:reference"},
		SearchType: CrossSearchFTS, TopK: 10, GroupBy: &GroupBy{Key: "collection"},
		PassageOptions: PassageOptions{Format: FormatPassages}})
	if grouped.Passages == nil || len(grouped.Passages.Passages) != 2 {
		t.Fatalf("one passage per collection: %+v", grouped.Passages)
	}

	// Errors.
	for _, bad := range []*MCPCrossSearchRequest{
		{Query: "x", TargetCollections: []string{"ref-a"}, SearchType: "regex"},
		{TargetCollections: []string{"ref-a"}, SearchType: CrossSearchFTS},
		{Query: "x", TargetCollections: []string{"role:nobody"}, SearchType: CrossSearchFTS},
	} {
		if _, err := dc.CrossSearch(ctx, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// A target that cannot answer adds nothing.
	if got := s.crossTextItems(ctx, crossTextParams{SearchType: CrossSearchFTS, Query: "", Targets: []string{"ref-a"}, TopK: 3}); len(got) != 0 {
		t.Fatalf("empty query: %+v", got)
	}
}

func TestCrossSearch_TextREST(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedReferences(t, s)

	rec := doRequest(t, s.handleCrossSearch, map[string]any{"query": "delete", "targetCollections": []string{"role:reference"},
		"searchType": "hybrid", "format": "passages", "topK": 3})
	var p PassagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || rec.Code != 200 || len(p.Passages) == 0 || p.Passages[0].Symbol == "" {
		t.Fatalf("passages: %d %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, s.handleCrossSearch, map[string]any{"query": "delete", "targetCollections": []string{"ref-a"}, "searchType": "fts"})
	var r CrossSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || len(r.Results) == 0 || r.Algorithm != "fts" {
		t.Fatalf("results: %d %s", rec.Code, rec.Body.String())
	}
	for body, want := range map[string]int{
		`{"query":"x","targetCollections":["ref-a"],"searchType":"regex"}`: 422,
		`{"targetCollections":["ref-a"],"searchType":"fts"}`:               400,
		`{"query":"x","targetCollections":["role:nobody"]}`:                400,
	} {
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		if code := doRequest(t, s.handleCrossSearch, m).Code; code != want {
			t.Errorf("%s: %d, want %d", body, code, want)
		}
	}
	if n, _ := s.crossTopK(nil, 0); n != 10 {
		t.Fatal("crossTopK default")
	}
	if n, _ := s.crossTopK(nil, 3); n != 3 {
		t.Fatal("crossTopK explicit")
	}
}

func TestReferenceTools(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	tools := &MCPToolServer{client: NewDirectClient(s)}
	ctx := context.Background()
	if _, err := tools.toolReferenceSearch(ctx, map[string]interface{}{"query": "x"}); err == nil || !strings.Contains(err.Error(), "reference add") {
		t.Fatalf("no references: %v", err)
	}
	seedReferences(t, s)

	out, err := tools.toolReferenceSearch(ctx, map[string]interface{}{"query": "delete neighbours", "top_k": 3})
	if err != nil || !strings.Contains(out, `"passages"`) || !strings.Contains(out, `"collection": "ref-`) || strings.Contains(out, `"mine"`) {
		t.Fatalf("reference_search: %v %s", err, out)
	}
	if _, err := tools.toolReferenceSearch(ctx, map[string]interface{}{"query": "x", "filter": map[string]interface{}{"k": map[string]interface{}{"bad": 1}}}); err == nil {
		t.Fatal("bad filter")
	}
	out, err = tools.toolReferenceSymbol(ctx, map[string]interface{}{"name": "Delete"})
	if err != nil || strings.Count(out, `"kind": "function"`) != 2 || !strings.Contains(out, "TestDelete") {
		t.Fatalf("reference_symbol: %v %s", err, out)
	}
	out, err = tools.toolReferenceExamples(ctx, map[string]interface{}{"name": "Delete", "collections": []interface{}{"ref-a"}})
	if err != nil || !strings.Contains(out, `"symbol": "TestDelete"`) || !strings.Contains(out, "Delete(1)") || strings.Contains(out, "hnsw") {
		t.Fatalf("reference_examples: %v %s", err, out)
	}
	out, err = tools.toolReferenceDependencies(ctx, map[string]interface{}{"collection": "ref-a", "key": "tree/node.go"})
	if err != nil || !strings.Contains(out, `"kind": "imports"`) || strings.Contains(out, `"kind": "calls"`) {
		t.Fatalf("reference_dependencies: %v %s", err, out)
	}
	if _, err := tools.toolReferenceDependencies(ctx, map[string]interface{}{"collection": "ref-a", "key": "nope.go"}); err == nil {
		t.Fatal("unknown file")
	}
	if _, err := tools.toolReferenceSymbol(ctx, map[string]interface{}{"name": ""}); err == nil {
		t.Fatal("empty name")
	}
}

func TestCodeRoleValidation(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if err := (&CodeConfigDef{Role: "mine"}).Validate(); err == nil {
		t.Fatal("unknown role")
	}
	rec := httptest.NewRecorder()
	s.handleCollectionConfig(rec, httptest.NewRequest(http.MethodPut, "/v1/collection-config",
		strings.NewReader(`{"collection":"c","code":{"role":"mine"}}`)))
	if rec.Code != 400 {
		t.Fatalf("REST: %d", rec.Code)
	}
	dc := NewDirectClient(s)
	if err := dc.SetCollectionConfig(context.Background(), &MCPSetCollectionConfigRequest{Collection: "c", Code: &CodeConfigDef{Role: "mine"}}); err == nil {
		t.Fatal("MCP")
	}
	g := &GRPCServer{server: s}
	if _, err := g.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{Collection: "c", Code: &proto.CodeConfigProto{Role: "mine"}}); err == nil {
		t.Fatal("gRPC")
	}
	if _, err := g.SetCollectionConfig(context.Background(), &proto.SetCollectionConfigRequest{Collection: "c", Code: &proto.CodeConfigProto{Role: "reference"}}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := s.CollectionManager.Get("c"); cfg.Code.Role != CodeRoleReference || codeConfigToProto(cfg.Code).Role != CodeRoleReference {
		t.Fatalf("stored: %+v", cfg.Code)
	}
}
