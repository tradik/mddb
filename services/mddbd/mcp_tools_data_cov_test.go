package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// covBRun dispatches one tool against a fake client in read-write mode.
func covBRun(t *testing.T, fake *covBFakeClient, name string, args map[string]interface{}) (string, error) {
	t.Helper()
	ts := &MCPToolServer{client: fake, globalMode: ModeRW}
	return ts.mcpCallTool(context.Background(), name, args)
}

// covBDataToolArgs holds, per data tool, arguments that pass the tool's own
// validation so the call reaches the client.
var covBDataToolArgs = map[string]map[string]interface{}{
	"add_document":           {"collection": "c", "key": "k", "lang": "en", "content_md": "body"},
	"search_documents":       {"collection": "c"},
	"delete_document":        {"collection": "c", "key": "k", "lang": "en"},
	"get_stats":              {},
	"add_documents_batch":    {"collection": "c", "documents": []interface{}{}},
	"delete_documents_batch": {"collection": "c", "documents": []interface{}{}},
	"export_documents":       {"collection": "c"},
	"create_backup":          {"to": "b.db"},
	"restore_backup":         {"from": "b.db"},
	"semantic_search":        {"collection": "c", "query": "q"},
	"vector_reindex":         {"collection": "c"},
	"vector_stats":           {},
	"import_url":             {"collection": "c", "url": "https://example.com"},
	"set_ttl":                {"collection": "c", "key": "k", "lang": "en", "ttl": 60.0},
	"full_text_search":       {"collection": "c", "query": "q"},
	"fts_reindex":            {"collection": "c"},
	"fts_languages":          {},
	"hybrid_search":          {"collection": "c", "query": "q"},
	"register_webhook":       {"url": "https://example.com/h"},
	"list_webhooks":          {},
	"delete_webhook":         {"id": "w1"},
	"set_schema":             {"collection": "c", "schema": "{}"},
	"get_schema":             {"collection": "c"},
	"delete_schema":          {"collection": "c"},
	"list_schemas":           {},
	"validate_document":      {"collection": "c"},
	"update_document":        {"collection": "c", "key": "k", "lang": "en"},
	"get_document_meta":      {"collection": "c", "key": "k", "lang": "en"},
	"code_graph":             {"collection": "c", "key": "k"},
	"classify_document":      {"collection": "c", "text": "t"},
	"search_advisor":         {"collection": "c"},
	"get_indexing_status":    {"collection": "c"},
	"code_symbol":            {"collection": "c", "name": "Foo"},
}

// TestCovBSearchToolsRejectBadFilterAndGroupBy checks the operator filter
// (SRCH-017) and group_by (SRCH-016) are validated before the client is
// called, on every search tool that takes them.
func TestCovBSearchToolsRejectBadFilterAndGroupBy(t *testing.T) {
	badFilter := map[string]interface{}{"tag": map[string]interface{}{"$bogus": "x"}}
	badGroup := map[string]interface{}{"per_group": 2.0}
	base := map[string]map[string]interface{}{
		"search_documents": {"collection": "c"},
		"semantic_search":  {"collection": "c", "query": "q"},
		"full_text_search": {"collection": "c", "query": "q"},
		"hybrid_search":    {"collection": "c", "query": "q"},
		"cross_search":     {"target_collections": []interface{}{"a"}},
		"aggregate":        {"collection": "c"},
	}
	takesGroupBy := map[string]bool{"semantic_search": true, "full_text_search": true, "hybrid_search": true, "cross_search": true}
	for name, args := range base {
		t.Run(name, func(t *testing.T) {
			fake := covBNewFake()
			withFilter := map[string]interface{}{"filter": badFilter}
			for k, v := range args {
				withFilter[k] = v
			}
			if _, err := covBRun(t, fake, name, withFilter); err == nil || !strings.Contains(err.Error(), "unsupported filter operator") {
				t.Errorf("bad filter: err = %v", err)
			}
			if takesGroupBy[name] {
				withGroup := map[string]interface{}{"group_by": badGroup}
				for k, v := range args {
					withGroup[k] = v
				}
				if _, err := covBRun(t, fake, name, withGroup); err == nil || !strings.Contains(err.Error(), "groupBy.key is required") {
					t.Errorf("bad group_by: err = %v", err)
				}
			}
			if len(fake.calls) != 0 {
				t.Errorf("invalid input reached the client: %v", fake.calls)
			}
		})
	}
}

// TestCovBSearchToolsForwardFilterGroupByIndexWait checks the new search
// controls arrive in the client request.
func TestCovBSearchToolsForwardFilterGroupByIndexWait(t *testing.T) {
	extra := map[string]interface{}{
		"filter":         map[string]interface{}{"path": map[string]interface{}{"prefix": "internal/"}},
		"group_by":       map[string]interface{}{"key": "path", "perGroup": 2.0},
		"read_level":     "indexedPlusPending",
		"wait_for_index": 500.0,
		"wait_for_keys":  []interface{}{"k1", 3.0},
		"format":         "results",
		"passage_lines":  4.0,
	}
	run := func(fake *covBFakeClient, name string, args map[string]interface{}) {
		t.Helper()
		for k, v := range extra {
			args[k] = v
		}
		if _, err := covBRun(t, fake, name, args); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	fake := covBNewFake()
	run(fake, "semantic_search", map[string]interface{}{"collection": "c", "query": "q"})
	v := fake.covBCall(t, "VectorSearch").(*MCPVectorSearchRequest)
	if v.Filter["path"].Prefix != "internal/" || v.GroupBy == nil || v.GroupBy.Key != "path" || v.GroupBy.PerGroup != 2 ||
		v.ReadLevel != "indexedPlusPending" || v.WaitForIndex != 500 || len(v.WaitForKeys) != 1 ||
		v.PassageLines != 4 {
		t.Errorf("vector request = %+v", v)
	}
	run(fake, "hybrid_search", map[string]interface{}{"collection": "c", "query": "q"})
	if h := fake.covBCall(t, "HybridSearch").(*MCPHybridSearchRequest); h.Filter == nil || h.GroupBy == nil || h.WaitForIndex != 500 {
		t.Errorf("hybrid request = %+v", h)
	}
	run(fake, "full_text_search", map[string]interface{}{"collection": "c", "query": "q"})
	if f := fake.covBCall(t, "FTSSearch").(*MCPFTSSearchRequest); f.Filter == nil || f.GroupBy == nil || f.Format != "results" {
		t.Errorf("fts request = %+v", f)
	}
	run(fake, "search_documents", map[string]interface{}{"collection": "c"})
	if s := fake.covBCall(t, "Search").(*MCPSearchRequest); s.Filter == nil || s.PassageLines != 4 {
		t.Errorf("search request = %+v", s)
	}
}

// TestCovBSearchToolsReturnPassages checks that when the client answers
// with passages (format "passages", AGT-002) the tool returns them instead of
// the result list.
func TestCovBSearchToolsReturnPassages(t *testing.T) {
	passages := &PassagesResponse{Total: 1, Passages: []Passage{{Collection: "c", Key: "k", Path: "covb/path.go"}}}
	fake := covBNewFake()
	fake.searchResp = &MCPSearchResponse{Passages: passages, Documents: []MCPDocument{{Key: "RESULT-LIST"}}}
	fake.vecResp = &MCPVectorSearchResponse{Passages: passages}
	fake.ftsResp = &MCPFTSSearchResponse{Passages: passages}
	fake.hybridResp = &MCPHybridSearchResponse{Passages: passages}
	fake.crossResp = &MCPCrossSearchResponse{Passages: passages}
	for name, args := range map[string]map[string]interface{}{
		"search_documents": {"collection": "c", "format": "passages"},
		"semantic_search":  {"collection": "c", "query": "q", "format": "passages"},
		"full_text_search": {"collection": "c", "query": "q", "format": "passages"},
		"hybrid_search":    {"collection": "c", "query": "q", "format": "passages"},
		"cross_search":     {"target_collections": []interface{}{"a"}, "format": "passages"},
	} {
		out, err := covBRun(t, fake, name, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		res := covBUnmarshal(t, out)
		if res["total"].(float64) != 1 || !strings.Contains(out, "covb/path.go") || strings.Contains(out, "RESULT-LIST") {
			t.Errorf("%s: out = %s", name, out)
		}
	}
}

func TestCovBExpectedRevisionAndNewTools(t *testing.T) {
	fake := covBNewFake()
	if _, err := covBRun(t, fake, "add_document", map[string]interface{}{"collection": "c", "key": "k", "lang": "en", "expected_revision": "r1"}); err != nil {
		t.Fatal(err)
	}
	if r := fake.covBCall(t, "Add").(*MCPAddRequest); r.ExpectedRevision != "r1" {
		t.Errorf("add expected revision = %q", r.ExpectedRevision)
	}
	if _, err := covBRun(t, fake, "update_document", map[string]interface{}{"collection": "c", "key": "k", "lang": "en", "expected_revision": "r2"}); err != nil {
		t.Fatal(err)
	}
	if r := fake.covBCall(t, "UpdateDocument").(*MCPUpdateDocumentRequest); r.ExpectedRevision != "r2" {
		t.Errorf("update expected revision = %q", r.ExpectedRevision)
	}

	out, err := covBRun(t, fake, "get_indexing_status", map[string]interface{}{"collection": "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if res := covBUnmarshal(t, out); res["collection"] != "docs" || res["pendingEmbeddings"].(float64) != 3 {
		t.Errorf("indexing status = %s", out)
	}

	out, err = covBRun(t, fake, "code_symbol", map[string]interface{}{
		"collection": "src", "name": "Open", "limit": 5.0, "include_content": true, "include": []interface{}{"callers", 1.0, "references"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fake.covBCall(t, "CodeSymbol").(*SymbolRequest)
	if r.Collection != "src" || r.Name != "Open" || r.Limit != 5 || !r.IncludeContent || len(r.Include) != 2 || r.Include[1] != "references" {
		t.Errorf("symbol request = %+v", r)
	}
	if res := covBUnmarshal(t, out); res["symbol"] != "Open" {
		t.Errorf("symbol out = %s", out)
	}
}

// TestCovBDataToolsPassClientErrorsThrough checks every data tool returns the
// client's error itself, not a success message and not a different error.
func TestCovBDataToolsPassClientErrorsThrough(t *testing.T) {
	for name, args := range covBDataToolArgs {
		t.Run(name, func(t *testing.T) {
			fake := covBNewFake()
			fake.err = errCovBClient
			out, err := covBRun(t, fake, name, args)
			if !errors.Is(err, errCovBClient) {
				t.Fatalf("err = %v, want errCovBClient", err)
			}
			if out != "" {
				t.Errorf("output on error = %q", out)
			}
		})
	}
}

// TestCovBDataToolsSucceed checks every data tool answers the client's
// success with output, and the plain-message tools with the exact message.
func TestCovBDataToolsSucceed(t *testing.T) {
	wantPrefix := map[string]string{
		"add_document":     "Document added successfully:\n",
		"delete_document":  "Document deleted: c/k (en)",
		"export_documents": "Export started",
		"create_backup":    "Backup created: b.db",
		"restore_backup":   "Database restored from: b.db",
		"vector_reindex":   "Reindex complete:\n",
		"import_url":       "Document imported from URL:\n",
		"set_ttl":          "TTL updated:\n",
		"register_webhook": "Webhook registered:\n",
		"delete_webhook":   "Webhook deleted: w1",
		"set_schema":       "Schema set for collection: c",
		"delete_schema":    "Schema deleted for collection: c",
		"update_document":  "Document updated:\n",
	}
	for name, args := range covBDataToolArgs {
		t.Run(name, func(t *testing.T) {
			out, err := covBRun(t, covBNewFake(), name, args)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if p, ok := wantPrefix[name]; ok {
				if !strings.HasPrefix(out, p) {
					t.Errorf("output = %q, want prefix %q", out, p)
				}
				return
			}
			if !strings.HasPrefix(strings.TrimSpace(out), "{") && !strings.HasPrefix(strings.TrimSpace(out), "null") {
				t.Errorf("output is not JSON: %q", out)
			}
		})
	}
}

func TestCovBAddDocumentForwardsFields(t *testing.T) {
	fake := covBNewFake()
	args := map[string]interface{}{
		"collection": "blog", "key": "hello", "lang": "en", "content_md": "# Hi",
		"meta": map[string]interface{}{"tag": []interface{}{"a", "b"}},
	}
	out, err := covBRun(t, fake, "add_document", args)
	if err != nil {
		t.Fatal(err)
	}
	req := fake.covBCall(t, "Add").(*MCPAddRequest)
	if req.Collection != "blog" || req.Key != "hello" || req.Lang != "en" || req.ContentMD != "# Hi" || len(req.Meta["tag"]) != 2 {
		t.Errorf("request = %+v", req)
	}
	if got := covBUnmarshal(t, out); got["key"] != "hello" {
		t.Errorf("output document = %v", got)
	}
}

func TestCovBSearchDocumentsProjection(t *testing.T) {
	fake := covBNewFake()
	fake.searchResp = &MCPSearchResponse{Total: 1, Documents: []MCPDocument{{
		ID: "1", Key: "k", Lang: "en", ContentMD: "SECRET-BODY",
		Meta: map[string][]string{"title": {"T"}, "author": {"A"}},
	}}}

	// fields narrows to one meta key and drops the body by default (GO-019).
	out, err := covBRun(t, fake, "search_documents", map[string]interface{}{
		"collection": "c", "fields": []interface{}{"title"}, "asc": true, "limit": 5.0, "offset": 2.0, "sort": "key",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := fake.covBCall(t, "Search").(*MCPSearchRequest)
	if !req.Asc || req.Limit != 5 || req.Offset != 2 || req.Sort != "key" || req.IncludeContent {
		t.Errorf("request = %+v", req)
	}
	if strings.Contains(out, "SECRET-BODY") || strings.Contains(out, "author") || !strings.Contains(out, "title") {
		t.Errorf("projection not applied: %s", out)
	}

	// Without projection the full response comes back, body included.
	out, err = covBRun(t, fake, "search_documents", map[string]interface{}{"collection": "c"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SECRET-BODY") || !strings.Contains(out, "author") {
		t.Errorf("full shape lost data: %s", out)
	}
}

func TestCovBBatchToolsValidateDocuments(t *testing.T) {
	for _, name := range []string{"add_documents_batch", "delete_documents_batch"} {
		t.Run(name, func(t *testing.T) {
			fake := covBNewFake()
			if _, err := covBRun(t, fake, name, map[string]interface{}{"collection": "c", "documents": "nope"}); err == nil || err.Error() != "documents must be an array" {
				t.Errorf("non-array: err = %v", err)
			}
			if _, err := covBRun(t, fake, name, map[string]interface{}{"collection": "c", "documents": []interface{}{map[string]interface{}{}, 3.0}}); err == nil || err.Error() != "invalid document at index 1" {
				t.Errorf("non-object: err = %v", err)
			}
			if len(fake.calls) != 0 {
				t.Errorf("invalid input reached the client: %v", fake.calls)
			}
		})
	}

	fake := covBNewFake()
	docs := []interface{}{map[string]interface{}{"key": "a", "lang": "en", "content_md": "x", "meta": map[string]interface{}{"t": "v"}}}
	if _, err := covBRun(t, fake, "add_documents_batch", map[string]interface{}{"collection": "c", "documents": docs}); err != nil {
		t.Fatal(err)
	}
	add := fake.covBCall(t, "AddBatch").(*MCPAddBatchRequest)
	if add.Collection != "c" || len(add.Documents) != 1 || add.Documents[0].Key != "a" || add.Documents[0].ContentMD != "x" || add.Documents[0].Meta["t"][0] != "v" {
		t.Errorf("AddBatch request = %+v", add)
	}
	if _, err := covBRun(t, fake, "delete_documents_batch", map[string]interface{}{"collection": "c", "documents": docs}); err != nil {
		t.Fatal(err)
	}
	del := fake.covBCall(t, "DeleteBatch").(*MCPDeleteBatchRequest)
	if del.Collection != "c" || len(del.Documents) != 1 || del.Documents[0].Key != "a" || del.Documents[0].Lang != "en" {
		t.Errorf("DeleteBatch request = %+v", del)
	}
}

func TestCovBSemanticSearchForwardsOptionsAndProjects(t *testing.T) {
	fake := covBNewFake()
	fake.vecResp = &MCPVectorSearchResponse{Total: 1, Results: []MCPVectorSearchResult{{
		Score: 0.9, Document: MCPDocument{ID: "1", Key: "k", ContentMD: "SECRET-BODY", Meta: map[string][]string{"title": {"T"}}},
	}}}
	out, err := covBRun(t, fake, "semantic_search", map[string]interface{}{
		"collection": "c", "query": "q", "top_k": 3.0, "mmr": true, "mmr_lambda": 0.4, "threshold": 0.2,
		"include_content": false, "algorithm": "hnsw", "retrieval_mode": "chunk", "window_size": 2.0, "oversample": 1.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := fake.covBCall(t, "VectorSearch").(*MCPVectorSearchRequest)
	if !req.MMR || req.MMRLambda != 0.4 || req.Threshold != 0.2 || req.TopK != 3 || req.IncludeContent ||
		req.Algorithm != "hnsw" || req.RetrievalMode != "chunk" || req.WindowSize != 2 || req.Oversample != 1.5 {
		t.Errorf("request = %+v", req)
	}
	if strings.Contains(out, "SECRET-BODY") {
		t.Errorf("include_content=false still returned the body: %s", out)
	}

	out, err = covBRun(t, fake, "semantic_search", map[string]interface{}{"collection": "c", "query": "q"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SECRET-BODY") {
		t.Errorf("full shape lost the body: %s", out)
	}
}

func TestCovBFullTextSearchForwardsOptionsAndProjects(t *testing.T) {
	fake := covBNewFake()
	fake.ftsResp = &MCPFTSSearchResponse{Total: 1, Results: []MCPFTSResult{{
		Score: 1.5, MatchedTerms: []string{"q"}, Document: MCPDocument{ID: "1", Key: "k", ContentMD: "SECRET-BODY"},
	}}}
	out, err := covBRun(t, fake, "full_text_search", map[string]interface{}{
		"collection": "c", "query": "q", "fields": "title", "highlight": "true", "fuzzy": 2.0,
		"boost": map[string]interface{}{"title": 3.0}, "facet_by": []interface{}{"tag"}, "mode": "phrase",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := fake.covBCall(t, "FTSSearch").(*MCPFTSSearchRequest)
	if !req.Highlight || req.Fuzzy != 2 || req.Boost["title"] != 3 || len(req.FacetBy) != 1 || req.Mode != "phrase" || req.IncludeContent {
		t.Errorf("request = %+v", req)
	}
	if strings.Contains(out, "SECRET-BODY") || !strings.Contains(out, "matchedTerms") {
		t.Errorf("projection: %s", out)
	}

	out, err = covBRun(t, fake, "full_text_search", map[string]interface{}{"collection": "c", "query": "q"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SECRET-BODY") {
		t.Errorf("full shape lost the body: %s", out)
	}
}

func TestCovBOptionalFlagsForwarded(t *testing.T) {
	fake := covBNewFake()
	mustRun := func(name string, args map[string]interface{}) {
		t.Helper()
		if _, err := covBRun(t, fake, name, args); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	mustRun("vector_reindex", map[string]interface{}{"collection": "c", "force": true})
	if r := fake.covBCall(t, "VectorReindex").(*MCPVectorReindexRequest); !r.Force || r.Collection != "c" {
		t.Errorf("VectorReindex = %+v", r)
	}

	mustRun("hybrid_search", map[string]interface{}{"collection": "c", "query": "q", "alpha": 0.7, "threshold": 0.1, "rrf_k": 30.0, "strategy": "rrf"})
	if r := fake.covBCall(t, "HybridSearch").(*MCPHybridSearchRequest); r.Alpha != 0.7 || r.Threshold != 0.1 || r.RRFK != 30 || r.Strategy != "rrf" {
		t.Errorf("HybridSearch = %+v", r)
	}

	mustRun("register_webhook", map[string]interface{}{"url": "u", "collection": "c", "events": []interface{}{"doc.added", 1.0, "doc.deleted"}})
	if r := fake.covBCall(t, "RegisterWebhook").(*MCPRegisterWebhookRequest); len(r.Events) != 2 || r.Events[1] != "doc.deleted" || r.Collection != "c" {
		t.Errorf("RegisterWebhook = %+v", r)
	}

	mustRun("classify_document", map[string]interface{}{"collection": "c", "text": "t", "labels": []interface{}{"x", 2.0, "y"}, "multi": true, "threshold": 0.3, "top_k": 2.0})
	if r := fake.covBCall(t, "Classify").(*MCPClassifyRequest); len(r.Labels) != 2 || !r.Multi || r.Threshold != 0.3 || r.TopK != 2 {
		t.Errorf("Classify = %+v", r)
	}

	mustRun("code_graph", map[string]interface{}{"collection": "c", "key": "k", "direction": "out", "depth": 2.0, "max_degree": 9.0, "lines": "true"})
	if r := fake.covBCall(t, "CodeGraph").(*MCPCodeGraphRequest); r.Direction != "out" || r.Depth != 2 || r.MaxDegree != 9 || !r.Lines {
		t.Errorf("CodeGraph = %+v", r)
	}

	mustRun("import_url", map[string]interface{}{"collection": "c", "url": "u", "key": "k", "lang": "en", "ttl": 30.0, "meta": map[string]interface{}{"a": "b"}})
	if r := fake.covBCall(t, "ImportURL").(*MCPImportURLRequest); r.TTL != 30 || r.URL != "u" || r.Meta["a"][0] != "b" {
		t.Errorf("ImportURL = %+v", r)
	}

	mustRun("set_ttl", map[string]interface{}{"collection": "c", "key": "k", "lang": "en", "ttl": 45.0})
	if r := fake.covBCall(t, "SetTTL").(*MCPSetTTLRequest); r.TTL != 45 {
		t.Errorf("SetTTL = %+v", r)
	}

	mustRun("search_advisor", map[string]interface{}{"collection": "docs"})
	if c := fake.covBCall(t, "SearchAdvisor").(string); c != "docs" {
		t.Errorf("SearchAdvisor collection = %q", c)
	}
}

// TestCovBUpdateDocumentMetaSemantics checks the three meta cases: absent
// leaves meta alone (nil), an empty object clears it (non-nil, empty), and a
// populated object replaces it; content_md and ttl are pointers set only when
// passed.
func TestCovBUpdateDocumentMetaSemantics(t *testing.T) {
	fake := covBNewFake()
	base := func() map[string]interface{} {
		return map[string]interface{}{"collection": "c", "key": "k", "lang": "en"}
	}

	if _, err := covBRun(t, fake, "update_document", base()); err != nil {
		t.Fatal(err)
	}
	r := fake.covBCall(t, "UpdateDocument").(*MCPUpdateDocumentRequest)
	if r.Meta != nil || r.ContentMD != nil || r.TTL != nil {
		t.Errorf("absent fields set: %+v", r)
	}

	args := base()
	args["meta"] = map[string]interface{}{}
	if _, err := covBRun(t, fake, "update_document", args); err != nil {
		t.Fatal(err)
	}
	r = fake.covBCall(t, "UpdateDocument").(*MCPUpdateDocumentRequest)
	if r.Meta == nil || len(r.Meta) != 0 {
		t.Errorf("empty meta should clear: %+v", r.Meta)
	}

	args = base()
	args["meta"] = map[string]interface{}{"tag": "x"}
	args["content_md"] = "new body"
	args["ttl"] = 120.0
	if _, err := covBRun(t, fake, "update_document", args); err != nil {
		t.Fatal(err)
	}
	r = fake.covBCall(t, "UpdateDocument").(*MCPUpdateDocumentRequest)
	if r.Meta["tag"][0] != "x" || r.ContentMD == nil || *r.ContentMD != "new body" || r.TTL == nil || *r.TTL != 120 {
		t.Errorf("populated update = %+v", r)
	}
}

// TestCovBDataToolsAgainstStore exercises the add → search → update →
// get_document_meta → delete path against a real store through DirectClient.
func TestCovBDataToolsAgainstStore(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	ctx := context.Background()

	if _, err := ts.mcpCallTool(ctx, "add_document", map[string]interface{}{
		"collection": "covb", "key": "one", "lang": "en", "content_md": "# One\n\nalpha body",
		"meta": map[string]interface{}{"tag": "first"},
	}); err != nil {
		t.Fatal(err)
	}

	out, err := ts.mcpCallTool(ctx, "search_documents", map[string]interface{}{"collection": "covb"})
	if err != nil {
		t.Fatal(err)
	}
	if got := covBUnmarshal(t, out); got["total"].(float64) != 1 || !strings.Contains(out, "alpha body") {
		t.Errorf("search = %s", out)
	}

	if _, err := ts.mcpCallTool(ctx, "update_document", map[string]interface{}{
		"collection": "covb", "key": "one", "lang": "en", "meta": map[string]interface{}{"tag": "second"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err = ts.mcpCallTool(ctx, "get_document_meta", map[string]interface{}{"collection": "covb", "key": "one", "lang": "en"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "second") || strings.Contains(out, "first") {
		t.Errorf("meta after update = %s", out)
	}

	out, err = ts.mcpCallTool(ctx, "delete_document", map[string]interface{}{"collection": "covb", "key": "one", "lang": "en"})
	if err != nil || out != "Document deleted: covb/one (en)" {
		t.Fatalf("delete = %q, %v", out, err)
	}
	out, err = ts.mcpCallTool(ctx, "search_documents", map[string]interface{}{"collection": "covb"})
	if err != nil {
		t.Fatal(err)
	}
	if got := covBUnmarshal(t, out); got["total"].(float64) != 0 {
		t.Errorf("document still found after delete: %s", out)
	}
}
