package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/cache"
	"mddb/internal/fts"
	"mddb/internal/spell"
	"mddb/internal/storage"

	json "mddb/internal/jsonx"

	bolt "go.etcd.io/bbolt"
)

// Coverage batch covG: REST full-text search handlers. The helpers below are
// shared by every covG test file in this package.

// covGServer is the full handler test server with a language-aware FTS index.
func covGServer(t *testing.T) *Server {
	t.Helper()
	s, cleanup := newHandlerTestServer(t)
	t.Cleanup(cleanup)
	reg := fts.NewLangRegistry("en")
	fts.RegisterDefaultLanguages(reg)
	s.FTSIndex.SetStemmer(fts.NewPorterStemmer())
	s.FTSIndex.SetLangRegistry(reg)
	return s
}

// covGDecode unmarshals a recorder body into v, failing on a non-JSON body.
func covGDecode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}
}

// covGRaw sends a raw (possibly malformed) body to a handler.
func covGRaw(handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	handler(rec, req)
	return rec
}

// covGWantCode fails unless the recorder carries the expected status.
func covGWantCode(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, want, rec.Body.String())
	}
}

// covGEnableAuth turns on an enabled AuthManager, so any request without
// claims in its context is refused.
func covGEnableAuth(t *testing.T, s *Server) {
	t.Helper()
	am, cleanup := authMwSetup(t)
	t.Cleanup(cleanup)
	s.AuthManager = am
}

// covGPutRaw writes bytes straight into the docs bucket, bypassing every
// write path — used for corrupt and expired documents.
func covGPutRaw(t *testing.T, s *Server, collection, id string, data []byte) {
	t.Helper()
	if err := s.DB.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("docs")).Put(storage.DocKey(collection, id), data)
	}); err != nil {
		t.Fatal(err)
	}
}

// covGPutDoc stores an encoded document directly.
func covGPutDoc(t *testing.T, s *Server, collection string, doc *storage.Doc) {
	t.Helper()
	data, err := marshalDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	covGPutRaw(t, s, collection, doc.ID, data)
}

// covGSetConfig stores a collection config.
func covGSetConfig(t *testing.T, s *Server, collection string, cfg *CollectionConfig) {
	t.Helper()
	if cfg.Type == "" {
		cfg.Type = "default"
	}
	if err := s.CollectionManager.Set(collection, cfg); err != nil {
		t.Fatal(err)
	}
}

// covGFailWriter is a ResponseWriter whose body writes fail, like a client
// that hung up mid-response.
type covGFailWriter struct {
	header http.Header
	code   int
}

func (w *covGFailWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *covGFailWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }
func (w *covGFailWriter) WriteHeader(code int)      { w.code = code }

// covGFTSDocs seeds a small searchable blog collection and returns the docs.
func covGFTSDocs(t *testing.T, s *Server) []storage.Doc {
	t.Helper()
	return []storage.Doc{
		addTestDoc(t, s, "blog", "a", "en", "the quick brown fox jumps over the lazy dog programming",
			map[string][]string{"tag": {"go"}, "year": {"2020"}}),
		addTestDoc(t, s, "blog", "b", "en", "programming languages and quick compilers",
			map[string][]string{"tag": {"rust"}, "year": {"2024"}}),
		addTestDoc(t, s, "blog", "c", "en", "a lazy afternoon with no code at all",
			map[string][]string{"tag": {"go"}, "year": {"2022"}}),
	}
}

func covGFTS(t *testing.T, s *Server, body any) (FTSSearchResponse, *httptest.ResponseRecorder) {
	t.Helper()
	rec := doRequest(t, s.handleFTS, body)
	var resp FTSSearchResponse
	if rec.Code == http.StatusOK {
		covGDecode(t, rec, &resp)
	}
	return resp, rec
}

func TestCovGFTSRejectsBadRequests(t *testing.T) {
	s := covGServer(t)
	covGWantCode(t, covGRaw(s.handleFTS, http.MethodPost, "/", "{not json"), http.StatusBadRequest)

	_, rec := covGFTS(t, s, map[string]any{"collection": "blog"})
	covGWantCode(t, rec, http.StatusBadRequest)

	_, rec = covGFTS(t, s, map[string]any{"collection": "blog", "query": "x", "facetScope": "everything"})
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "facetScope") {
		t.Errorf("error should name facetScope: %s", rec.Body.String())
	}

	_, rec = covGFTS(t, s, map[string]any{"collection": "blog", "query": "x", "algorithm": "lucene"})
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "unknown algorithm") {
		t.Errorf("want unknown algorithm error: %s", rec.Body.String())
	}

	s.FTSIndex = nil
	_, rec = covGFTS(t, s, map[string]any{"collection": "blog", "query": "x"})
	covGWantCode(t, rec, http.StatusBadRequest)
}

func TestCovGFTSCacheMissThenHit(t *testing.T) {
	s := covGServer(t)
	s.SearchCache = cache.NewSearchCache(10)
	covGFTSDocs(t, s)

	body := map[string]any{"collection": "blog", "query": "programming", "cacheTtl": 60}
	first, rec := covGFTS(t, s, body)
	covGWantCode(t, rec, http.StatusOK)
	if got := rec.Header().Get("X-MDDB-Cache"); got != "miss" {
		t.Errorf("first request X-MDDB-Cache = %q, want miss", got)
	}
	if first.Total != 2 {
		t.Fatalf("total = %d, want 2", first.Total)
	}

	second, rec := covGFTS(t, s, body)
	if got := rec.Header().Get("X-MDDB-Cache"); got != "hit" {
		t.Errorf("second request X-MDDB-Cache = %q, want hit", got)
	}
	if second.Total != first.Total || second.Results[0].Document.ID != first.Results[0].Document.ID {
		t.Error("a cache hit must replay the stored answer")
	}

	// A client that hangs up during either write is logged, not panicked on.
	fw := &covGFailWriter{}
	b, _ := json.Marshal(body)
	s.handleFTS(fw, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(b))))
	if fw.header.Get("X-MDDB-Cache") != "hit" {
		t.Error("the failing writer should still have been served from the cache")
	}
	fw = &covGFailWriter{}
	s.handleFTS(fw, httptest.NewRequest(http.MethodPost, "/",
		strings.NewReader(`{"collection":"blog","query":"lazy","cacheTtl":5}`)))
	if fw.header.Get("X-MDDB-Cache") != "miss" {
		t.Error("a new question is a miss even when the write fails")
	}
}

func TestCovGFTSAlgorithmsAndFuzzy(t *testing.T) {
	s := covGServer(t)
	covGFTSDocs(t, s)
	for _, algo := range []string{"tfidf", "bm25", "bm25f", "pmisparse"} {
		for _, fuzzy := range []int{-3, 0, 1, 9} {
			resp, rec := covGFTS(t, s, map[string]any{
				"collection": "blog", "query": "programming", "algorithm": algo, "fuzzy": fuzzy,
				"fieldWeights": map[string]float64{"content": 2},
			})
			covGWantCode(t, rec, http.StatusOK)
			wantFuzzy := min(max(fuzzy, 0), 2)
			if resp.Fuzzy != wantFuzzy {
				t.Errorf("%s fuzzy %d: response fuzzy = %d, want %d", algo, fuzzy, resp.Fuzzy, wantFuzzy)
			}
			if resp.Algorithm != algo || resp.Mode != "simple" {
				t.Errorf("%s: algorithm/mode = %s/%s", algo, resp.Algorithm, resp.Mode)
			}
			if algo == "bm25f" && resp.FieldWeights["content"] != 2 {
				t.Error("bm25f should echo its field weights")
			}
			if algo != "bm25f" && resp.FieldWeights != nil {
				t.Errorf("%s should not echo field weights", algo)
			}
			if algo != "pmisparse" && resp.Total == 0 {
				t.Errorf("%s fuzzy %d found nothing for a term two documents contain", algo, fuzzy)
			}
		}
	}
}

func TestCovGFTSAutoModeDetection(t *testing.T) {
	s := covGServer(t)
	covGFTSDocs(t, s)
	for query, want := range map[string]string{
		`"quick brown"`:          "phrase",
		`"quick fox"~3`:          "proximity",
		`progr*`:                 "wildcard",
		`"quick brown" progr*`:   "boolean",
		`programming AND quick`:  "boolean",
		`plain words`:            "simple",
		`"quick fox"~3 -compile`: "boolean",
	} {
		resp, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": query, "mode": "auto"})
		covGWantCode(t, rec, http.StatusOK)
		if resp.Mode != want {
			t.Errorf("query %q: mode = %q, want %q", query, resp.Mode, want)
		}
	}
}

func TestCovGFTSProximityMode(t *testing.T) {
	s := covGServer(t)
	covGFTSDocs(t, s)

	// No proximity clause in the query: the request distance (or 5) applies.
	resp, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": "quick fox", "mode": "proximity"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Mode != "proximity" || resp.Total != 1 {
		t.Errorf("default distance: mode %q total %d, want proximity/1", resp.Mode, resp.Total)
	}
	// A distance too small to bridge "quick ... fox" (two words apart).
	resp, _ = covGFTS(t, s, map[string]any{"collection": "blog", "query": "quick fox", "mode": "proximity", "distance": 1})
	if resp.Total != 0 {
		t.Errorf("distance 1 should not join words two apart, got %d", resp.Total)
	}
	// The clause's own distance wins over the request's.
	resp, _ = covGFTS(t, s, map[string]any{"collection": "blog", "query": `"quick fox"~4`, "mode": "proximity", "distance": 1})
	if resp.Total != 1 {
		t.Errorf("clause distance 4 should match, got %d", resp.Total)
	}
}

func TestCovGFTSExpressionMode(t *testing.T) {
	s := covGServer(t)
	covGFTSDocs(t, s)
	resp, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": "programming AND NOT rust", "mode": "expression"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Mode != "expression" {
		t.Errorf("mode = %q", resp.Mode)
	}
	for _, q := range []string{"   ", "(programming AND", `"unterminated`} {
		_, rec = covGFTS(t, s, map[string]any{"collection": "blog", "query": q, "mode": "expression"})
		covGWantCode(t, rec, http.StatusBadRequest)
	}
}

func TestCovGFTSUnknownModeFallsBackToSimple(t *testing.T) {
	s := covGServer(t)
	covGFTSDocs(t, s)
	resp, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": "lazy", "mode": "telepathy"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Mode != "simple" || resp.Total != 2 {
		t.Errorf("mode %q total %d, want simple/2", resp.Mode, resp.Total)
	}
}

func TestCovGFTSFiltersRangesFacetsHighlights(t *testing.T) {
	s := covGServer(t)
	docs := covGFTSDocs(t, s)

	// A filter that matches nothing short-circuits with an empty, typed answer.
	resp, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": "lazy",
		"filterMeta": map[string][]string{"tag": {"haskell"}}, "algorithm": "bm25"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 0 || resp.Algorithm != "bm25" || resp.Results == nil {
		t.Errorf("empty filter: %+v", resp)
	}

	resp, _ = covGFTS(t, s, map[string]any{
		"collection": "blog", "query": "afternoon programming",
		"filterMeta": map[string][]string{"tag": {"go"}},
		"rangeMeta":  []RangeFilter{{Field: "year", Gte: "2021"}},
		"facetBy":    []string{"tag"},
		"highlight":  true, "highlightTag": "<b>",
	})
	if resp.Total != 1 || resp.Results[0].Document.ID != docs[2].ID {
		t.Fatalf("filter+range should leave only doc c, got %+v", resp.Results)
	}
	if len(resp.Results[0].Highlights) == 0 || !strings.Contains(resp.Results[0].Highlights[0].Fragment, "<b>") {
		t.Errorf("highlight with a custom tag missing: %+v", resp.Results[0].Highlights)
	}
	if got := resp.Facets["tag"]; len(got) != 1 {
		t.Errorf("returned-scope facets = %+v", resp.Facets)
	}
	if resp.Stats == nil || resp.Stats.TotalTokens == 0 {
		t.Error("search stats should be reported by default")
	}

	// facetScope all counts every match, not just the page.
	resp, _ = covGFTS(t, s, map[string]any{"collection": "blog", "query": "lazy programming", "limit": 1,
		"facetBy": []string{"tag"}, "facetScope": "all"})
	if resp.Total != 1 || resp.Matched != 3 {
		t.Errorf("facetScope all: total %d matched %d, want 1/3", resp.Total, resp.Matched)
	}
}

func TestCovGFTSSkipsMissingCorruptAndExpiredDocs(t *testing.T) {
	s := covGServer(t)
	good := addTestDoc(t, s, "blog", "good", "en", "zebra crossing", nil)
	// Indexed but never stored, stored as garbage, stored but expired.
	for _, id := range []string{"ghost", "corrupt", "expired"} {
		if err := s.FTSIndex.Index("blog", id, "zebra stripes"); err != nil {
			t.Fatal(err)
		}
	}
	covGPutRaw(t, s, "blog", "corrupt", []byte{0xff, 0x00, 0x13})
	covGPutDoc(t, s, "blog", &storage.Doc{ID: "expired", Key: "expired", Lang: "en",
		ContentMD: "zebra stripes", ExpiresAt: time.Now().Add(-time.Hour).Unix()})

	resp, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": "zebra"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 1 || resp.Results[0].Document.ID != good.ID {
		t.Errorf("only the live, readable document should survive, got %+v", resp.Results)
	}
}

func TestCovGFTSSpellCorrection(t *testing.T) {
	s := covGServer(t)
	covGFTSDocs(t, s)
	sm := spell.NewSpellManager(s.DB)
	if err := sm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := sm.AddWord("blog", "de", "programming", 1000); err != nil {
		t.Fatal(err)
	}
	sm.LoadAll()
	deadline := time.Now().Add(5 * time.Second)
	for !sm.Ready() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	s.SpellManager = sm
	// The collection's spellLang overrides the request language.
	covGSetConfig(t, s, "blog", &CollectionConfig{SpellCorrect: true, SpellLang: "de"})

	resp, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": "programing", "lang": "en"})
	covGWantCode(t, rec, http.StatusOK)
	if resp.SpellCorrected == nil || resp.SpellCorrected.Corrected != "programming" {
		t.Fatalf("spellCorrected = %+v", resp.SpellCorrected)
	}
	if resp.Total != 2 {
		t.Errorf("the corrected query should find both documents, got %d", resp.Total)
	}

	// A correctly spelled query reports no correction.
	resp, _ = covGFTS(t, s, map[string]any{"collection": "blog", "query": "programming"})
	if resp.SpellCorrected != nil {
		t.Errorf("no correction expected, got %+v", resp.SpellCorrected)
	}
}

func TestCovGFTSCurationAndContextBudget(t *testing.T) {
	s := covGServer(t)
	docs := covGFTSDocs(t, s)
	cm := NewCurationManager(s.DB)
	if err := cm.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := cm.Set(&CurationRule{ID: "r1", Collection: "blog", Query: "programming", Enabled: true,
		Pins: []PinnedDoc{{Key: "c", Lang: "en", Position: 1}}}); err != nil {
		t.Fatal(err)
	}
	s.CurationManager = cm

	resp, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": "programming", "limit": 2})
	covGWantCode(t, rec, http.StatusOK)
	if resp.Total != 2 {
		t.Fatalf("curation must still honour the limit, got %d", resp.Total)
	}
	if resp.Results[0].Document.ID != docs[2].ID || !resp.Results[0].Pinned {
		t.Errorf("pinned doc c should lead, got %+v", resp.Results[0])
	}

	// A tiny context budget keeps only what fits and says so.
	covGSetConfig(t, s, "blog", &CollectionConfig{Retrieval: &RetrievalProfileDef{ContextTokenBudget: 1}})
	resp, _ = covGFTS(t, s, map[string]any{"collection": "blog", "query": "lazy"})
	if !resp.ContextTruncated || resp.Total >= 2 {
		t.Errorf("budget: truncated=%v total=%d", resp.ContextTruncated, resp.Total)
	}
}

func TestCovGFTSStatsDisabled(t *testing.T) {
	t.Setenv("MDDB_SEARCH_STATS", "false")
	s := covGServer(t)
	covGFTSDocs(t, s)
	resp, _ := covGFTS(t, s, map[string]any{"collection": "blog", "query": "lazy"})
	if resp.Stats != nil {
		t.Error("MDDB_SEARCH_STATS=false must drop searchStats")
	}
}

func TestCovGFTSReindex(t *testing.T) {
	s := covGServer(t)
	covGFTSDocs(t, s)
	covGWantCode(t, covGRaw(s.handleFTSReindex, http.MethodPost, "/", "{"), http.StatusBadRequest)
	covGWantCode(t, doRequest(t, s.handleFTSReindex, map[string]any{}), http.StatusBadRequest)

	rec := doRequest(t, s.handleFTSReindex, map[string]any{"collection": "blog"})
	covGWantCode(t, rec, http.StatusOK)
	var out map[string]any
	covGDecode(t, rec, &out)
	if out["status"] != "ok" || out["reindexed"].(float64) != 3 {
		t.Errorf("reindex = %v", out)
	}

	idx := s.FTSIndex
	s.FTSIndex = nil
	covGWantCode(t, doRequest(t, s.handleFTSReindex, map[string]any{"collection": "blog"}), http.StatusBadRequest)
	s.FTSIndex = idx

	// The store failing underneath is a 500 with the cause.
	_ = s.DB.Close()
	rec = doRequest(t, s.handleFTSReindex, map[string]any{"collection": "blog"})
	covGWantCode(t, rec, http.StatusInternalServerError)
	if !strings.Contains(rec.Body.String(), "error") {
		t.Errorf("500 body should carry the error: %s", rec.Body.String())
	}
}

func TestCovGFTSLanguages(t *testing.T) {
	s := covGServer(t)
	rec := covGRaw(s.handleFTSLanguages, http.MethodGet, "/", "")
	covGWantCode(t, rec, http.StatusOK)
	var out struct {
		Languages   []struct{ Code, Name string }
		DefaultLang string
	}
	covGDecode(t, rec, &out)
	if out.DefaultLang != "en" || len(out.Languages) < 2 {
		t.Fatalf("languages = %+v", out)
	}
	for _, l := range out.Languages {
		if l.Name == "" {
			t.Errorf("language %q has no name", l.Code)
		}
	}

	s.FTSIndex = nil
	rec = covGRaw(s.handleFTSLanguages, http.MethodGet, "/", "")
	if !strings.Contains(rec.Body.String(), `"languages":[]`) {
		t.Errorf("no index should list no languages: %s", rec.Body.String())
	}
}

func TestCovGFTSStoreFailureIsAnError(t *testing.T) {
	s := covGServer(t)
	covGFTSDocs(t, s)
	_ = s.DB.Close()
	_, rec := covGFTS(t, s, map[string]any{"collection": "blog", "query": "lazy"})
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "database not open") {
		t.Errorf("the store's error should reach the caller: %s", rec.Body.String())
	}
}
