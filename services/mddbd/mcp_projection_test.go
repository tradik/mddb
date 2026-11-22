package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"mddb/internal/cache"
	"mddb/internal/fts"

	bolt "go.etcd.io/bbolt"
)

// --- helpers -------------------------------------------------------------

func boolPtr(b bool) *bool { return &b }

// newProjectionMCPServer builds an MCPToolServer wired to a DirectClient over a
// real BoltDB + FTS index, so the projection tests exercise the full path:
// args map -> tool handler -> DirectClient -> JSON string.
func newProjectionMCPServer(t *testing.T) (*MCPToolServer, *Server, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "proj.db")
	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		DB:   db,
		Path: dbPath,
		Mode: ModeRW,
		BucketNames: BucketNames{
			Docs:    []byte("docs"),
			IdxMeta: []byte("idxmeta"),
			Rev:     []byte("rev"),
			ByKey:   []byte("bykey"),
		},
		Cache: cache.NewDocumentCache(100, 60),
	}
	if err := s.ensureBuckets(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	s.FTSIndex = fts.NewFTSIndex(db)
	if err := s.FTSIndex.EnsureBuckets(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	langReg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(langReg)
	s.FTSIndex.SetStemmer(fts.NewPorterStemmer())
	s.FTSIndex.SetLangRegistry(langReg)

	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	return ts, s, func() { _ = db.Close() }
}

// seedVersionsDoc inserts a "versions"-style document with the full 6-key meta
// set and a sizable body, mirroring the issue's real deployment.
func seedVersionsDoc(t *testing.T, s *Server) {
	t.Helper()
	_, _, err := s.addDocument("versions", "go", "en_US", map[string][]string{
		"name":             {"go"},
		"currentVersion":   {"1.26.4"},
		"versionChangedAt": {"2026-06-01"},
		"dockerImage":      {"golang:1.26.4"},
		"category":         {"language"},
		"homepage":         {"https://go.dev"},
	}, "# Go\n\nThe Go programming language release changelog body goes here.", 0, true)
	if err != nil {
		t.Fatalf("seed doc: %v", err)
	}
}

func sortedKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sampleDoc() MCPDocument {
	return MCPDocument{
		ID:   "id1",
		Key:  "go",
		Lang: "en_US",
		Meta: map[string][]string{
			"name":           {"go"},
			"currentVersion": {"1.26"},
			"category":       {"language"},
		},
		ContentMD: "BODY",
		AddedAt:   time.Unix(1000, 0).UTC(),
		UpdatedAt: time.Unix(2000, 0).UTC(),
	}
}

// --- mcpGetStringSlice ---------------------------------------------------

func TestMcpGetStringSlice(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]interface{}
		want []string
	}{
		{"json array", map[string]interface{}{"fields": []interface{}{"a", "b"}}, []string{"a", "b"}},
		{"native slice", map[string]interface{}{"fields": []string{"x"}}, []string{"x"}},
		{"single string", map[string]interface{}{"fields": "only"}, []string{"only"}},
		{"empty string", map[string]interface{}{"fields": ""}, nil},
		{"absent", map[string]interface{}{}, nil},
		{"wrong type", map[string]interface{}{"fields": 42.0}, nil},
		{"mixed drops non-strings", map[string]interface{}{"fields": []interface{}{"a", 3.0, "b"}}, []string{"a", "b"}},
		{"only non-strings", map[string]interface{}{"fields": []interface{}{1.0, 2.0}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mcpGetStringSlice(c.in, "fields")
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// --- mcpProjectionActive / mcpProjectionArgs -----------------------------

func TestMcpProjectionActive(t *testing.T) {
	cases := []struct {
		fields  []string
		content bool
		want    bool
	}{
		{nil, true, false},              // default: nothing to do
		{nil, false, true},              // drop body
		{[]string{"name"}, true, true},  // project meta
		{[]string{"name"}, false, true}, // both
		{[]string{}, true, false},       // empty fields == all
	}
	for _, c := range cases {
		if got := mcpProjectionActive(c.fields, c.content); got != c.want {
			t.Errorf("mcpProjectionActive(%v,%v)=%v want %v", c.fields, c.content, got, c.want)
		}
	}
}

func TestMcpProjectionArgs(t *testing.T) {
	ic, fields := mcpProjectionArgs(map[string]interface{}{})
	if !ic || fields != nil {
		t.Errorf("defaults: got include=%v fields=%v; want true/nil", ic, fields)
	}
	ic, fields = mcpProjectionArgs(map[string]interface{}{
		"include_content": false,
		"fields":          []interface{}{"name", "currentVersion"},
	})
	if ic {
		t.Error("expected include_content=false")
	}
	if !reflect.DeepEqual(fields, []string{"name", "currentVersion"}) {
		t.Errorf("fields=%v", fields)
	}

	// GO-019: fields without an explicit include_content drops the body —
	// that's the whole point of a fields projection.
	ic, _ = mcpProjectionArgs(map[string]interface{}{
		"fields": []interface{}{"name"},
	})
	if ic {
		t.Error("fields without include_content must default to include=false (GO-019)")
	}

	// Explicit include_content=true still wins over the fields default.
	ic, _ = mcpProjectionArgs(map[string]interface{}{
		"include_content": true,
		"fields":          []interface{}{"name"},
	})
	if !ic {
		t.Error("explicit include_content=true must override the fields default")
	}
}

// --- projectMeta ---------------------------------------------------------

func TestProjectMeta_MetaPrefix(t *testing.T) {
	meta := map[string][]string{"title": {"T"}, "meta.odd": {"O"}, "odd": {"plain"}}
	got := projectMeta(meta, []string{"meta.title", "meta.odd", "meta.missing"})
	if len(got) != 2 || got["title"][0] != "T" || got["meta.odd"][0] != "O" {
		t.Fatalf("prefix projection: %v", got)
	}
}

func TestProjectMeta(t *testing.T) {
	meta := map[string][]string{"a": {"1"}, "b": {"2"}, "c": {"3"}}
	got := projectMeta(meta, []string{"a", "c", "missing"})
	want := map[string][]string{"a": {"1"}, "c": {"3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	// Absent keys never produce empty placeholders.
	if _, ok := got["missing"]; ok {
		t.Error("missing key should be skipped, not emitted empty")
	}
	if len(projectMeta(meta, nil)) != 0 {
		t.Error("nil fields should yield empty projection")
	}
}

// --- projectMCPDocument --------------------------------------------------

func TestProjectMCPDocument_FieldsMinimal(t *testing.T) {
	m := projectMCPDocument(sampleDoc(), []string{"name", "currentVersion"}, false)
	if keys := sortedKeys(m); !reflect.DeepEqual(keys, []string{"id", "key", "meta"}) {
		t.Fatalf("keys=%v; want [id key meta]", keys)
	}
	meta := m["meta"].(map[string][]string)
	if _, ok := meta["category"]; ok {
		t.Error("category should be projected out")
	}
	if len(meta) != 2 {
		t.Errorf("meta should have 2 keys, got %d", len(meta))
	}
}

func TestProjectMCPDocument_FieldsWithContent(t *testing.T) {
	m := projectMCPDocument(sampleDoc(), []string{"name"}, true)
	if keys := sortedKeys(m); !reflect.DeepEqual(keys, []string{"content_md", "id", "key", "meta"}) {
		t.Fatalf("keys=%v", keys)
	}
	if m["content_md"] != "BODY" {
		t.Errorf("content_md=%v", m["content_md"])
	}
}

func TestProjectMCPDocument_NoFieldsDropBody(t *testing.T) {
	m := projectMCPDocument(sampleDoc(), nil, false)
	want := []string{"added_at", "id", "key", "lang", "meta", "updated_at"}
	if keys := sortedKeys(m); !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys=%v; want %v", keys, want)
	}
	if _, ok := m["content_md"]; ok {
		t.Error("content_md must be omitted when includeContent=false")
	}
	// Full meta preserved when no fields projection.
	if len(m["meta"].(map[string][]string)) != 3 {
		t.Error("expected full meta")
	}
}

func TestProjectMCPDocument_NoFieldsWithContent(t *testing.T) {
	m := projectMCPDocument(sampleDoc(), nil, true)
	want := []string{"added_at", "content_md", "id", "key", "lang", "meta", "updated_at"}
	if keys := sortedKeys(m); !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys=%v; want %v", keys, want)
	}
}

// --- response wrappers ---------------------------------------------------

func TestProjectSearchResult(t *testing.T) {
	resp := &MCPSearchResponse{Documents: []MCPDocument{sampleDoc()}, Total: 1}
	m := projectSearchResult(resp, []string{"name"}, false)
	if m["total"] != 1 {
		t.Errorf("total=%v", m["total"])
	}
	docs := m["documents"].([]map[string]interface{})
	if len(docs) != 1 {
		t.Fatalf("docs len=%d", len(docs))
	}
	if _, ok := docs[0]["content_md"]; ok {
		t.Error("content_md should be dropped")
	}
}

func TestProjectFTSResult(t *testing.T) {
	resp := &MCPFTSSearchResponse{
		Results: []MCPFTSResult{{
			Document:     sampleDoc(),
			Score:        1.5,
			MatchedTerms: []string{"go"},
		}},
		Total:     1,
		Algorithm: "bm25",
		Fuzzy:     1,
		Lang:      "en",
	}
	m := projectFTSResult(resp, []string{"name"}, false)
	if m["total"] != 1 || m["algorithm"] != "bm25" || m["fuzzy"] != 1 || m["lang"] != "en" {
		t.Errorf("envelope not preserved: %+v", m)
	}
	results := m["results"].([]map[string]interface{})
	if results[0]["score"] != 1.5 {
		t.Errorf("score not preserved: %v", results[0]["score"])
	}
	if !reflect.DeepEqual(results[0]["matchedTerms"], []string{"go"}) {
		t.Errorf("matchedTerms not preserved: %v", results[0]["matchedTerms"])
	}
	// Lang omitted when empty.
	resp.Lang = ""
	if _, ok := projectFTSResult(resp, nil, false)["lang"]; ok {
		t.Error("empty lang should be omitted")
	}
}

func TestProjectVectorResult(t *testing.T) {
	resp := &MCPVectorSearchResponse{
		Results: []MCPVectorSearchResult{{
			Document: sampleDoc(),
			Score:    0.9,
			Rank:     1,
		}},
		Total:          1,
		Model:          "test-model",
		Dimensions:     384,
		Algorithm:      "flat",
		DistanceMetric: "cosine",
	}
	m := projectVectorResult(resp, []string{"name"}, false)
	if m["total"] != 1 || m["model"] != "test-model" || m["dimensions"] != 384 ||
		m["algorithm"] != "flat" || m["distanceMetric"] != "cosine" {
		t.Errorf("envelope not preserved: %+v", m)
	}
	results := m["results"].([]map[string]interface{})
	if results[0]["rank"] != 1 || results[0]["score"] != float32(0.9) {
		t.Errorf("score/rank not preserved: %+v", results[0])
	}
}

// --- handler integration: search_documents -------------------------------

func TestToolSearchDocuments_DefaultUnchanged(t *testing.T) {
	ts, s, cleanup := newProjectionMCPServer(t)
	defer cleanup()
	seedVersionsDoc(t, s)

	out, err := ts.mcpCallTool(context.Background(), "search_documents",
		map[string]interface{}{"collection": "versions"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	doc := got["documents"].([]interface{})[0].(map[string]interface{})
	if _, ok := doc["content_md"]; !ok {
		t.Error("default output must include content_md")
	}
	if _, ok := doc["added_at"]; !ok {
		t.Error("default output must include added_at")
	}
	meta := doc["meta"].(map[string]interface{})
	if len(meta) != 6 {
		t.Errorf("default output must include all 6 meta keys, got %d", len(meta))
	}
}

func TestToolSearchDocuments_IncludeContentFalse(t *testing.T) {
	ts, s, cleanup := newProjectionMCPServer(t)
	defer cleanup()
	seedVersionsDoc(t, s)

	out, err := ts.mcpCallTool(context.Background(), "search_documents",
		map[string]interface{}{"collection": "versions", "include_content": false})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	doc := got["documents"].([]interface{})[0].(map[string]interface{})
	if _, ok := doc["content_md"]; ok {
		t.Error("content_md must be omitted when include_content=false")
	}
	// Full meta still present (only the body was dropped).
	if len(doc["meta"].(map[string]interface{})) != 6 {
		t.Error("all meta keys should survive content omission")
	}
}

func TestToolSearchDocuments_Fields(t *testing.T) {
	ts, s, cleanup := newProjectionMCPServer(t)
	defer cleanup()
	seedVersionsDoc(t, s)

	out, err := ts.mcpCallTool(context.Background(), "search_documents",
		map[string]interface{}{
			"collection":      "versions",
			"include_content": false,
			"fields":          []interface{}{"name", "currentVersion", "versionChangedAt", "dockerImage"},
		})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	doc := got["documents"].([]interface{})[0].(map[string]interface{})
	if keys := sortedKeys(doc); !reflect.DeepEqual(keys, []string{"id", "key", "meta"}) {
		t.Fatalf("hit keys=%v; want [id key meta]", keys)
	}
	meta := doc["meta"].(map[string]interface{})
	if len(meta) != 4 {
		t.Errorf("expected 4 projected meta keys, got %d (%v)", len(meta), meta)
	}
	if _, ok := meta["category"]; ok {
		t.Error("category must be projected out")
	}
	if _, ok := meta["homepage"]; ok {
		t.Error("homepage must be projected out")
	}
}

// --- handler integration: full_text_search -------------------------------

func TestToolFTSSearch_DefaultUnchanged(t *testing.T) {
	ts, s, cleanup := newProjectionMCPServer(t)
	defer cleanup()
	seedVersionsDoc(t, s)

	out, err := ts.mcpCallTool(context.Background(), "full_text_search",
		map[string]interface{}{"collection": "versions", "query": "programming"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	results := got["results"].([]interface{})
	if len(results) == 0 {
		t.Fatal("expected at least one FTS hit")
	}
	doc := results[0].(map[string]interface{})["document"].(map[string]interface{})
	if _, ok := doc["content_md"]; !ok {
		t.Error("default FTS output must include content_md")
	}
}

func TestToolFTSSearch_Projection(t *testing.T) {
	ts, s, cleanup := newProjectionMCPServer(t)
	defer cleanup()
	seedVersionsDoc(t, s)

	out, err := ts.mcpCallTool(context.Background(), "full_text_search",
		map[string]interface{}{
			"collection":      "versions",
			"query":           "programming",
			"include_content": false,
			"fields":          []interface{}{"name", "currentVersion"},
		})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	results := got["results"].([]interface{})
	if len(results) == 0 {
		t.Fatal("expected at least one FTS hit")
	}
	hit := results[0].(map[string]interface{})
	// Envelope fields survive.
	if _, ok := hit["score"]; !ok {
		t.Error("score must be preserved")
	}
	doc := hit["document"].(map[string]interface{})
	if keys := sortedKeys(doc); !reflect.DeepEqual(keys, []string{"id", "key", "meta"}) {
		t.Fatalf("projected FTS doc keys=%v", keys)
	}
	if len(doc["meta"].(map[string]interface{})) != 2 {
		t.Error("expected 2 projected meta keys")
	}
}

// --- custom-tool wiring --------------------------------------------------

func TestMcpMergeProjectionDefaults(t *testing.T) {
	merged := map[string]interface{}{}
	mcpMergeProjectionDefaults(MCPCustomToolDefs{
		IncludeContent: boolPtr(false),
		Fields:         []string{"name", "currentVersion"},
	}, merged)
	if merged["include_content"] != false {
		t.Errorf("include_content=%v", merged["include_content"])
	}
	if !reflect.DeepEqual(merged["fields"], []string{"name", "currentVersion"}) {
		t.Errorf("fields=%v", merged["fields"])
	}

	// Nil/empty defaults wire nothing (backward compatible).
	empty := map[string]interface{}{}
	mcpMergeProjectionDefaults(MCPCustomToolDefs{}, empty)
	if len(empty) != 0 {
		t.Errorf("expected no wiring, got %v", empty)
	}
}

func TestMcpMergeActionDefaults(t *testing.T) {
	m := map[string]interface{}{}
	if err := mcpMergeActionDefaults("full_text_search", MCPCustomToolDefs{Limit: 5}, m); err != nil {
		t.Fatal(err)
	}
	if m["limit"] != float64(5) {
		t.Errorf("limit=%v", m["limit"])
	}
	if err := mcpMergeActionDefaults("bogus", MCPCustomToolDefs{}, map[string]interface{}{}); err == nil {
		t.Error("expected error for unknown action")
	}

	sem := map[string]interface{}{}
	_ = mcpMergeActionDefaults("semantic_search", MCPCustomToolDefs{
		TopK:       3,
		Threshold:  0.6,
		FilterMeta: map[string][]string{"tag": {"featured"}},
	}, sem)
	if sem["top_k"] != float64(3) || sem["threshold"] != 0.6 {
		t.Errorf("semantic defaults not merged: %v", sem)
	}
	if sem["filter_meta"] == nil {
		t.Error("semantic filter_meta not merged")
	}

	sd := map[string]interface{}{}
	_ = mcpMergeActionDefaults("search_documents", MCPCustomToolDefs{
		Sort:       "updatedAt",
		Asc:        boolPtr(true),
		Limit:      10,
		Offset:     2,
		FilterMeta: map[string][]string{"tag": {"bestseller"}},
	}, sd)
	if sd["sort"] != "updatedAt" || sd["asc"] != true || sd["limit"] != float64(10) || sd["offset"] != float64(2) {
		t.Errorf("search_documents defaults not merged: %v", sd)
	}
	if sd["filter_meta"] == nil {
		t.Error("search_documents filter_meta not merged")
	}
}

func TestToolFTSSearch_Error(t *testing.T) {
	ts, _, cleanup := newProjectionMCPServer(t)
	defer cleanup()
	// Missing query -> FTSSearch returns an error, exercising the error path.
	if _, err := ts.mcpCallTool(context.Background(), "full_text_search",
		map[string]interface{}{"collection": "versions"}); err == nil {
		t.Error("expected error for missing query")
	}
}

func TestMcpCallCustomTool_WiresProjectionEndToEnd(t *testing.T) {
	ts, s, cleanup := newProjectionMCPServer(t)
	defer cleanup()
	seedVersionsDoc(t, s)

	ct := MCPCustomToolConfig{
		Name:        "version_check",
		Description: "Check a package version",
		Action:      "full_text_search",
		Defaults: MCPCustomToolDefs{
			Collection:     "versions",
			Query:          "programming",
			Limit:          1,
			IncludeContent: boolPtr(false),
			Fields:         []string{"name", "currentVersion", "versionChangedAt", "dockerImage"},
		},
	}

	out, err := ts.mcpCallCustomTool(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	results := got["results"].([]interface{})
	if len(results) == 0 {
		t.Fatal("expected a hit")
	}
	doc := results[0].(map[string]interface{})["document"].(map[string]interface{})
	if _, ok := doc["content_md"]; ok {
		t.Error("custom tool includeContent:false must drop content_md")
	}
	if keys := sortedKeys(doc); !reflect.DeepEqual(keys, []string{"id", "key", "meta"}) {
		t.Fatalf("projected custom-tool doc keys=%v", keys)
	}
}

// --- include_content coercion (stringified bools) ------------------------

func TestMcpCoerceBool(t *testing.T) {
	cases := []struct {
		in      interface{}
		wantVal bool
		wantOk  bool
	}{
		{true, true, true},
		{false, false, true},
		{"true", true, true},
		{"false", false, true},
		{"TRUE", true, true},
		{" false ", false, true},
		{"yes", true, true},
		{"no", false, true},
		{"1", true, true},
		{"0", false, true},
		{float64(1), true, true},
		{float64(0), false, true},
		{"maybe", false, false},
		{nil, false, false},
		{[]interface{}{}, false, false},
	}
	for _, c := range cases {
		val, ok := mcpCoerceBool(c.in)
		if val != c.wantVal || ok != c.wantOk {
			t.Errorf("mcpCoerceBool(%#v)=(%v,%v); want (%v,%v)", c.in, val, ok, c.wantVal, c.wantOk)
		}
	}
}

func TestMcpProjectionArgs_StringBool(t *testing.T) {
	// A client that stringifies the bool must still drop the body.
	ic, _ := mcpProjectionArgs(map[string]interface{}{"include_content": "false"})
	if ic {
		t.Error(`include_content:"false" (string) must be honored as false`)
	}
	if !mcpProjectionActive(nil, ic) {
		t.Error("projection should be active for stringified include_content=false")
	}
}

// --- semantic_search handler wiring (fake vector client) -----------------

// fakeVectorClient embeds MCPClient so only VectorSearch is implemented; every
// other method stays nil (and would panic if called, which the handler never
// does). It records the request so tests can assert include_content is threaded
// into the vector request.
type fakeVectorClient struct {
	MCPClient
	lastReq *MCPVectorSearchRequest
	resp    *MCPVectorSearchResponse
}

func (f *fakeVectorClient) VectorSearch(_ context.Context, req *MCPVectorSearchRequest) (*MCPVectorSearchResponse, error) {
	f.lastReq = req
	return f.resp, nil
}

func newFakeSemanticResp() *MCPVectorSearchResponse {
	return &MCPVectorSearchResponse{
		Results: []MCPVectorSearchResult{{
			Document: sampleDoc(),
			Score:    0.9,
			Rank:     1,
		}},
		Total:          1,
		Model:          "test-model",
		Dimensions:     3,
		Algorithm:      "flat",
		DistanceMetric: "cosine",
	}
}

func TestToolSemanticSearch_DefaultUnchanged(t *testing.T) {
	fake := &fakeVectorClient{resp: newFakeSemanticResp()}
	ts := &MCPToolServer{client: fake, globalMode: ModeRW}
	out, err := ts.mcpCallTool(context.Background(), "semantic_search",
		map[string]interface{}{"collection": "versions", "query": "go"})
	if err != nil {
		t.Fatal(err)
	}
	// include_content defaults true -> threaded into the vector request.
	if fake.lastReq == nil || !fake.lastReq.IncludeContent {
		t.Error("default include_content must be threaded as true into the vector request")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	doc := got["results"].([]interface{})[0].(map[string]interface{})["document"].(map[string]interface{})
	if _, ok := doc["content_md"]; !ok {
		t.Error("default semantic output must include content_md")
	}
}

func TestToolSemanticSearch_Projection(t *testing.T) {
	fake := &fakeVectorClient{resp: newFakeSemanticResp()}
	ts := &MCPToolServer{client: fake, globalMode: ModeRW}
	out, err := ts.mcpCallTool(context.Background(), "semantic_search",
		map[string]interface{}{
			"collection":      "versions",
			"query":           "go",
			"include_content": false,
			"fields":          []interface{}{"name", "currentVersion"},
		})
	if err != nil {
		t.Fatal(err)
	}
	// Handler must thread include_content=false into the request (catches a
	// re-hardcoded IncludeContent:true regression).
	if fake.lastReq == nil || fake.lastReq.IncludeContent {
		t.Error("expected req.IncludeContent=false threaded into VectorSearch")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	hit := got["results"].([]interface{})[0].(map[string]interface{})
	if hit["score"] == nil || hit["rank"] == nil {
		t.Error("score/rank envelope must be preserved")
	}
	doc := hit["document"].(map[string]interface{})
	// Projection branch must run (catches a missing projectVectorResult branch).
	if _, ok := doc["content_md"]; ok {
		t.Error("content_md must be dropped when include_content=false")
	}
	if keys := sortedKeys(doc); !reflect.DeepEqual(keys, []string{"id", "key", "meta"}) {
		t.Fatalf("projected semantic doc keys=%v", keys)
	}
	if len(doc["meta"].(map[string]interface{})) != 2 {
		t.Error("expected 2 projected meta keys")
	}
}
