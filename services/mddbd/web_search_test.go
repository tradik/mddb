package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/internal/storage"
)

// #449: POST /v1/web-search as Open WebUI's external web search engine
// calls it — {"query", "count"} in, [{"link", "title", "snippet"}] out.

// seedWebSearch fills three collections and two of the server's own stores.
func seedWebSearch(t *testing.T, s *Server) {
	t.Helper()
	addTestDoc(t, s, "docs", "install", "en", "# Install\n\nRun the installer, then start the turbine service.\n",
		map[string][]string{"title": {"Installing"}, "url": {"https://docs.example.com/install"}})
	addTestDoc(t, s, "kb", "faq/turbine", "en", "The turbine needs oil every month.\n", nil)
	addTestDoc(t, s, "secret", "plan", "en", "The turbine plan is secret.\n", nil)
	addTestDoc(t, s, "t1/notes", "n", "en", "Tenant notes about the turbine.\n", nil)
	addTestDoc(t, s, memoryMessagesCollection, "m", "en", "a chat about the turbine\n", nil)
	addTestDoc(t, s, chatConversationsCollection, "c", "en", "another turbine chat\n", nil)
}

// webSearch posts body to target as c and decodes an array answer.
func webSearch(t *testing.T, s *Server, c *JWTClaims, target, body string) (int, []webSearchResult, string) {
	t.Helper()
	rec := owuiCall(s, c, http.MethodPost, target, strings.NewReader(body), map[string]string{"Content-Type": "application/json"})
	var out []webSearchResult
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("not a JSON array: %s", rec.Body.String())
		}
	}
	return rec.Code, out, rec.Body.String()
}

// linksOf is the results' links, space-separated.
func linksOf(res []webSearchResult) string {
	var out []string
	for _, r := range res {
		out = append(out, r.Link)
	}
	return strings.Join(out, " ")
}

func TestWebSearchAnswersTheContract(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedWebSearch(t, s)
	switchModules(t, s, FeatureWebSearch)

	code, res, body := webSearch(t, s, nil, "/v1/web-search", `{"query":"turbine","count":10}`)
	if code != http.StatusOK || body[0] != '[' {
		t.Fatalf("%d %s", code, body)
	}
	links := linksOf(res)
	if strings.Contains(links, "memory_") || strings.Contains(links, "chat_conversations") {
		t.Errorf("the server's own stores were searched: %s", links)
	}
	if !strings.Contains(links, "https://docs.example.com/install") ||
		!strings.Contains(links, "http://example.com/v1/doc-meta?collection=kb&key=faq%2Fturbine&lang=en") {
		t.Errorf("links: %s", links)
	}
	for _, r := range res {
		if r.Title == "" || !strings.Contains(strings.ToLower(r.Snippet), "turbine") {
			t.Errorf("result without a title or a passage: %+v", r)
		}
		if r.Link == "https://docs.example.com/install" && r.Title != "Installing" {
			t.Errorf("meta.title not used: %+v", r)
		}
	}

	// count is honoured; the URL picks the collections, comma-separated or
	// repeated, and names the server's own stores when it wants them.
	if _, res, _ := webSearch(t, s, nil, "/v1/web-search", `{"query":"turbine","count":1}`); len(res) != 1 {
		t.Errorf("count 1: %d results", len(res))
	}
	if _, res, _ := webSearch(t, s, nil, "/v1/web-search?collections=kb,docs&collections=memory_messages", `{"query":"turbine"}`); len(res) != 3 {
		t.Errorf("named collections: %s", linksOf(res))
	}
	if _, res, _ := webSearch(t, s, nil, "/v1/web-search?searchType=hybrid&collections=kb", `{"query":"oil"}`); len(res) != 1 || res[0].Title != "faq/turbine" {
		t.Errorf("hybrid: %+v", res)
	}

	// The server's list narrows the default.
	t.Setenv(webSearchCollectionsEnv, "kb")
	if _, res, _ := webSearch(t, s, nil, "/v1/web-search", `{"query":"turbine"}`); len(res) != 1 || res[0].Title != "faq/turbine" {
		t.Errorf("%s: %+v", webSearchCollectionsEnv, res)
	}
}

func TestWebSearchRefusals(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	switchModules(t, s, FeatureWebSearch)
	cases := []struct{ name, method, target, body, want string }{
		{"bad json", http.MethodPost, "/v1/web-search", `{`, "invalid JSON body"},
		{"no query", http.MethodPost, "/v1/web-search", `{"query":"  "}`, "query is required"},
		{"search type", http.MethodPost, "/v1/web-search?searchType=vector", `{"query":"x"}`, "searchType must be fts or hybrid"},
		{"empty list", http.MethodPost, "/v1/web-search?collections=,", `{"query":"x"}`, ""},
		{"no role match", http.MethodPost, "/v1/web-search?collections=role:none", `{"query":"x"}`, "no collection matches"},
	}
	for _, tc := range cases {
		rec := owuiCall(s, nil, tc.method, tc.target, strings.NewReader(tc.body), nil)
		if tc.want == "" {
			if rec.Code != http.StatusOK {
				t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}
	if rec := owuiCall(s, nil, http.MethodGet, "/v1/web-search", nil, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}

	// A server whose collection list is still being counted says so.
	t.Setenv("MDDB_STATS_WAIT", "0")
	fresh, done := newHandlerTestServer(t)
	defer done()
	switchModules(t, fresh, FeatureWebSearch)
	if code, _, body := webSearch(t, fresh, nil, "/v1/web-search", `{"query":"x"}`); code != http.StatusServiceUnavailable {
		t.Errorf("pending stats: %d %s", code, body)
	}
}

// With auth on, a caller searches what it may read: by default every such
// collection (a tenant only its own), on the URL only collections it may
// read.
func TestWebSearchFollowsPermissions(t *testing.T) {
	s := accessServer(t)
	seedWebSearch(t, s)
	switchModules(t, s, FeatureWebSearch)

	if _, res, _ := webSearch(t, s, alice, "/v1/web-search", `{"query":"turbine"}`); linksOf(res) != "https://docs.example.com/install" {
		t.Errorf("alice: %s", linksOf(res))
	}
	if code, _, body := webSearch(t, s, alice, "/v1/web-search?collections=docs,secret", `{"query":"turbine"}`); code != http.StatusForbidden ||
		body != `{"error":"forbidden"}` {
		t.Errorf("alice naming secret: %d %s", code, body)
	}
	if _, res, _ := webSearch(t, s, tina, "/v1/web-search", `{"query":"turbine"}`); len(res) != 1 || !strings.Contains(res[0].Link, "t1%2Fnotes") {
		t.Errorf("tenant: %s", linksOf(res))
	}
	if _, res, _ := webSearch(t, s, root, "/v1/web-search", `{"query":"turbine"}`); len(res) != 4 {
		t.Errorf("admin: %s", linksOf(res))
	}
	if code, _, body := webSearch(t, s, nil, "/v1/web-search", `{"query":"turbine"}`); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d %s", code, body)
	}
	if code, _, _ := webSearch(t, s, nil, "/v1/web-search?collections=docs", `{"query":"turbine"}`); code != http.StatusUnauthorized {
		t.Errorf("anonymous naming docs: %d", code)
	}
}

// GO-054: a search counts against the tenant's read rate.
func TestWebSearchCountsAgainstTheReadRate(t *testing.T) {
	s := limitServer(t)
	switchModules(t, s, FeatureWebSearch)
	setLimits(t, s, "t1", TenantLimits{ReadPerSecond: 1})
	if code, _, body := webSearch(t, s, tina, "/v1/web-search", `{"query":"turbine"}`); code != http.StatusOK {
		t.Fatalf("first search: %d %s", code, body)
	}
	if code, _, body := webSearch(t, s, tina, "/v1/web-search", `{"query":"turbine"}`); code != http.StatusTooManyRequests {
		t.Errorf("over the read rate: %d %s", code, body)
	}
}

func TestWebSearchHelpers(t *testing.T) {
	if got := strings.Join(splitList([]string{"a, b", "", " ,c"}), "|"); got != "a|b|c" {
		t.Errorf("splitList: %s", got)
	}
	if !webSearchInternal("t1/"+chatConversationsCollection) || webSearchInternal("docs") {
		t.Error("webSearchInternal")
	}
	doc := &storage.Doc{Key: "k", Lang: "en", Meta: map[string][]string{"title": {" "}, "url": {"ftp://x"}, "link": {"http://a/b"}}}
	if docTitle(doc) != "k" || docLink("http://h", "c", doc) != "http://a/b" {
		t.Errorf("title %q link %q", docTitle(doc), docLink("http://h", "c", doc))
	}
	r := httptest.NewRequest(http.MethodPost, "https://mddb.local:11023/v1/web-search", nil)
	r.TLS = &tls.ConnectionState{}
	if got := requestOrigin(r); got != "https://mddb.local:11023" {
		t.Errorf("origin: %s", got)
	}
	if accessStatus(ErrForbidden) != http.StatusForbidden || accessStatus(ErrUnauthorized) != http.StatusUnauthorized {
		t.Error("accessStatus")
	}
}
