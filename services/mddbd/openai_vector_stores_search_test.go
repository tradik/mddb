package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// #446: POST /v1/vector_stores/{id}/search, as Open WebUI's
// OpenAI-compatible knowledge provider sends it.

// openAISearchBody is what Open WebUI sends.
const openAISearchBody = `{"query":"follower replica","max_num_results":5}`

// openAIFTSServer has no embedding provider, so it searches by full text.
func openAIFTSServer(t *testing.T) *Server {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(func() { s.closeCollectionFiles(); cleanup() })
	addTestDoc(t, s, "acme/docs", "replication", "en",
		"# Replication\n\nA follower replica copies every write from the leader.\n\nFailover promotes a follower.",
		map[string][]string{"url": {"https://docs.example.org/replication"}, "tag": {"ops", "ha"}})
	addTestDoc(t, s, "acme/docs", "backup", "en", "# Backup\n\nBackups are tarballs, restored with one command.", nil)
	switchModules(t, s, FeatureVectorStores)
	return s
}

// openAIResultsOf is the data array of a search page.
func openAIResultsOf(out map[string]any) []map[string]any {
	var res []map[string]any
	data, _ := out["data"].([]any)
	for _, d := range data {
		res = append(res, d.(map[string]any))
	}
	return res
}

func TestOpenAIVectorStoreSearchFullText(t *testing.T) {
	for _, layout := range []string{"single", "per-collection"} {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			s := openAIFTSServer(t)
			h := esHandler(t, s, false)
			// Open WebUI escapes the id: acme/docs arrives as acme%2Fdocs.
			rec, out := openAIDo(t, h, http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", openAISearchBody, "")
			if rec.Code != http.StatusOK || out["object"] != "vector_store.search_results.page" ||
				out["has_more"] != false || out["next_page"] != nil {
				t.Fatalf("search: %d %v", rec.Code, out)
			}
			if q := out["search_query"].([]any); len(q) != 1 || q[0] != "follower replica" {
				t.Fatalf("search_query %v", q)
			}
			res := openAIResultsOf(out)
			if len(res) != 1 {
				t.Fatalf("results %v", res)
			}
			hit := res[0]
			attrs := hit["attributes"].(map[string]any)
			text := hit["content"].([]any)[0].(map[string]any)
			if hit["filename"] != "replication" || hit["file_id"] == "" || hit["score"] != float64(1) ||
				attrs["url"] != "https://docs.example.org/replication" || attrs["collection"] != "acme/docs" ||
				attrs["lang"] != "en" || attrs["tag"] != "ops, ha" ||
				text["type"] != "text" || !strings.Contains(text["text"].(string), "follower replica copies") {
				t.Fatalf("hit %v", hit)
			}
			// An unescaped slash reaches the same collection.
			if rec, out = openAIDo(t, h, http.MethodPost, "/v1/vector_stores/acme/docs/search", openAISearchBody, ""); rec.Code != http.StatusOK || len(openAIResultsOf(out)) != 1 {
				t.Fatalf("unescaped id: %d %v", rec.Code, out)
			}
		})
	}
}

func TestOpenAIVectorStoreSearchQueryForms(t *testing.T) {
	s := openAIFTSServer(t)
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias("handbook", "acme/docs"); err != nil {
		t.Fatal(err)
	}
	h := esHandler(t, s, false)
	// An array of queries, an alias for an id, and the OpenAI options that
	// are accepted: a threshold above every score leaves nothing.
	rec, out := openAIDo(t, h, http.MethodPost, "/v1/vector_stores/handbook/search",
		`{"query":["backup", " ", "tarballs"],"filters":{"type":"eq","key":"tag","value":"ops"},"rewrite_query":true}`, "")
	if rec.Code != http.StatusOK || len(openAIResultsOf(out)) != 1 || openAIResultsOf(out)[0]["filename"] != "backup" {
		t.Fatalf("array query via alias: %d %v", rec.Code, out)
	}
	if q := out["search_query"].([]any); len(q) != 2 {
		t.Fatalf("blank queries are dropped: %v", q)
	}
	_, out = openAIDo(t, h, http.MethodPost, "/v1/vector_stores/handbook/search",
		`{"query":"backup","ranking_options":{"ranker":"auto","score_threshold":1.5}}`, "")
	if len(openAIResultsOf(out)) != 0 || out["data"] == nil {
		t.Fatalf("threshold: %v", out)
	}
}

func TestOpenAIVectorStoreSearchRefusals(t *testing.T) {
	s := openAIFTSServer(t)
	h := esHandler(t, s, false)
	for _, c := range []struct {
		method, path, body string
		status             int
		code               string
	}{
		{http.MethodPost, "/v1/vector_stores/acme%2Fdocs/files", openAISearchBody, http.StatusNotFound, "unknown_route"},
		{http.MethodPost, "/v1/vector_stores/acme%2Fdocs", openAISearchBody, http.StatusNotFound, "unknown_route"},
		{http.MethodGet, "/v1/vector_stores/acme%2Fdocs/search", "", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodPost, "/v1/vector_stores/nope/search", openAISearchBody, http.StatusNotFound, "vector_store_not_found"},
		{http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", `[1]`, http.StatusBadRequest, "invalid_request"},
		{http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", `{}`, http.StatusBadRequest, "invalid_request"},
		{http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", `{"query":"  "}`, http.StatusBadRequest, "invalid_request"},
		{http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", `{"query":7}`, http.StatusBadRequest, "invalid_request"},
		{http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", `{"query":"x","max_num_results":0}`, http.StatusBadRequest, "invalid_request"},
	} {
		rec, out := openAIDo(t, h, c.method, c.path, c.body, "")
		if rec.Code != c.status || openAIErrCode(out) != c.code {
			t.Fatalf("%s %s %s: %d %v", c.method, c.path, c.body, rec.Code, out)
		}
	}
}

func TestOpenAIVectorStoreSearchBodyTooLarge(t *testing.T) {
	s := openAIFTSServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", strings.NewReader(openAISearchBody))
	rec := httptest.NewRecorder()
	req.Body = http.MaxBytesReader(rec, req.Body, 4)
	s.handleOpenAIVectorStoreSubtree(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the cap: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOpenAIVectorStoreSearchPermissions(t *testing.T) {
	s := openAIFTSServer(t)
	am, cleanup := authMwSetup(t)
	defer cleanup()
	s.AuthManager = am
	h := esHandler(t, s, true)
	if _, err := am.CreateUser("reader", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := am.SetPermission(&Permission{Username: "reader", Collection: "acme/docs", Read: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := am.CreateUser("other", "pw"); err != nil {
		t.Fatal(err)
	}
	reader, _ := am.CreateAPIKey("reader", "open-webui", 0)
	other, _ := am.CreateAPIKey("other", "open-webui", 0)
	if rec, out := openAIDo(t, h, http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", openAISearchBody, reader); rec.Code != http.StatusOK || len(openAIResultsOf(out)) != 1 {
		t.Fatalf("reader: %d %v", rec.Code, out)
	}
	// 403 whether or not the collection exists (docs/AUTHENTICATION.md).
	for _, id := range []string{"acme%2Fdocs", "nope"} {
		rec, out := openAIDo(t, h, http.MethodPost, "/v1/vector_stores/"+id+"/search", openAISearchBody, other)
		if rec.Code != http.StatusForbidden || openAIErrCode(out) != "forbidden" {
			t.Fatalf("other on %s: %d %v", id, rec.Code, out)
		}
	}
	// Without claims the handler itself answers 401 in the OpenAI shape.
	rec := httptest.NewRecorder()
	s.handleOpenAIVectorStoreSubtree(rec, httptest.NewRequest(http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", strings.NewReader(openAISearchBody)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no claims: %d %s", rec.Code, rec.Body.String())
	}
}

func TestOpenAIVectorStoreSearchFullTextFailure(t *testing.T) {
	s := openAIFTSServer(t)
	s.FTSIndex = nil
	rec := httptest.NewRecorder()
	s.handleOpenAIVectorStoreSubtree(rec, httptest.NewRequest(http.MethodPost, "/v1/vector_stores/acme%2Fdocs/search", strings.NewReader(openAISearchBody)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusInternalServerError || openAIErrCode(out) != "search_failed" {
		t.Fatalf("no FTS index: %d %v", rec.Code, out)
	}
}

func TestOpenAIVectorStoreSearchHybrid(t *testing.T) {
	for _, layout := range []string{"single", "per-collection"} {
		t.Run(layout, func(t *testing.T) {
			t.Setenv("MDDB_STORAGE_LAYOUT", layout)
			s := covGVectorServer(t)
			switchModules(t, s, FeatureVectorStores)
			covGEmbedded(t, s, "kb", "go", "golang meetup in berlin", map[string][]string{"url": {"https://example.org/go"}})
			covGEmbedded(t, s, "kb", "rust", "rust conference in warsaw", nil)
			h := esHandler(t, s, false)
			rec, out := openAIDo(t, h, http.MethodPost, "/v1/vector_stores/kb/search", `{"query":"golang berlin"}`, "")
			res := openAIResultsOf(out)
			if rec.Code != http.StatusOK || len(res) == 0 || res[0]["filename"] != "go" {
				t.Fatalf("hybrid: %d %v", rec.Code, out)
			}
			for _, r := range res {
				if sc := r["score"].(float64); sc < 0 || sc > 1 {
					t.Fatalf("score %v outside 0..1", sc)
				}
			}
			if res[0]["attributes"].(map[string]any)["url"] != "https://example.org/go" {
				t.Fatalf("attributes %v", res[0]["attributes"])
			}

			// A provider that is down fails the search with a 500.
			s.Embedding = covGFailEmbedding{&mockEmbedding{dims: 3, model: "test-model"}}
			rec, out = openAIDo(t, h, http.MethodPost, "/v1/vector_stores/kb/search", `{"query":"golang"}`, "")
			if rec.Code != http.StatusInternalServerError || openAIErrCode(out) != "search_failed" {
				t.Fatalf("provider down: %d %v", rec.Code, out)
			}
		})
	}
}

func TestOpenAIResultCountAndRelativeScore(t *testing.T) {
	n := func(v int) *int { return &v }
	if openAIResultCount(nil) != 10 || openAIResultCount(n(3)) != 3 || openAIResultCount(n(500)) != 50 {
		t.Fatal("max_num_results: default 10, capped at 50")
	}
	if relativeScore(2, 4) != 0.5 || relativeScore(3, 0) != 1 || relativeScore(-1, 4) != 0 || relativeScore(5, 4) != 1 {
		t.Fatal("relativeScore")
	}
}

func TestOpenAIResultsUseQueryWordsForVectorOnlyHits(t *testing.T) {
	s := openAIFTSServer(t)
	doc, err := s.liveDocByKey("acme/docs", "backup", "en")
	if err != nil {
		t.Fatal(err)
	}
	hits := []openAIHit{{doc: doc, score: 0.4}}
	res := openAIResults("acme/docs", []string{"restored"}, hits, 0)
	if len(res) != 1 || !strings.Contains(res[0].Content[0].Text, "restored") {
		t.Fatalf("vector-only hit: %v", res)
	}
}
