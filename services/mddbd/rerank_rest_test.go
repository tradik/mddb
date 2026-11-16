package main

import (
	"net/http"
	"testing"
	"time"

	"mddb/internal/cache"
	json "mddb/internal/jsonx"
)

// An answer whose rerank was skipped is not cached: the next request asks
// the model again instead of repeating the fallback for the cache's lifetime.
func TestRerank_SkippedAnswerIsNotCached(t *testing.T) {
	s, f := rerankServer(t)
	s.SearchCache = cache.NewSearchCache(10)
	req := map[string]any{"collection": "rr", "query": rerankQuery, "cacheTtl": 60, "rerank": rrEnabled()}
	f.code.Store(http.StatusInternalServerError)
	for i := 0; i < 2; i++ {
		rec := doRequest(t, s.handleFTS, req)
		if rec.Header().Get("X-MDDB-Cache") != "miss" {
			t.Fatalf("request %d: %s", i, rec.Header().Get("X-MDDB-Cache"))
		}
	}
	f.code.Store(0)
	doRequest(t, s.handleFTS, req)
	if rec := doRequest(t, s.handleFTS, req); rec.Header().Get("X-MDDB-Cache") != "hit" {
		t.Fatalf("a reranked answer is cached: %s", rec.Header().Get("X-MDDB-Cache"))
	}
}

// RAG-011 over REST: every search surface reranks with the model, falls back
// to its own order with a reason when the model fails, and answers byte for
// byte as before when reranking is not asked for.

type rerankCase struct {
	name string
	h    http.HandlerFunc
	req  func() map[string]any
}

// rerankCases are the REST searches, each asking for three results.
func rerankCases(s *Server) []rerankCase {
	return []rerankCase{
		{"vector", s.handleVectorSearch, func() map[string]any {
			return map[string]any{"collection": "rr", "query": rerankQuery, "topK": 3}
		}},
		{"hybrid", s.handleHybridSearch, func() map[string]any {
			return map[string]any{"collection": "rr", "query": rerankQuery, "topK": 3}
		}},
		{"fts", s.handleFTS, func() map[string]any {
			return map[string]any{"collection": "rr", "query": rerankQuery, "limit": 3, "algorithm": "bm25"}
		}},
		{"cross", s.handleCrossSearch, func() map[string]any {
			return map[string]any{"targetCollections": []string{"rr"}, "query": rerankQuery, "topK": 3}
		}},
		{"cross-fts", s.handleCrossSearch, func() map[string]any {
			return map[string]any{"targetCollections": []string{"rr"}, "query": rerankQuery, "topK": 3, "searchType": "fts"}
		}},
		{"cross-hybrid", s.handleCrossSearch, func() map[string]any {
			return map[string]any{"targetCollections": []string{"rr"}, "query": rerankQuery, "topK": 3, "searchType": "hybrid"}
		}},
	}
}

func rawSearch(t *testing.T, h http.HandlerFunc, req map[string]any) string {
	t.Helper()
	rec := doRequest(t, h, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	// Full-text search lists matched terms in map order; body() sorts them,
	// so two answers compare by content rather than by iteration order.
	norm, err := json.Marshal(body(t, rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	return string(norm)
}

// Not asked for: the same bytes whether a model is configured or not.
func TestRerank_OffIsGolden(t *testing.T) {
	t.Setenv("MDDB_SEARCH_STATS", "false")
	s, f := rerankServer(t)
	for _, c := range rerankCases(s) {
		with := rawSearch(t, c.h, c.req())
		s.setRerank(nil, 0, "")
		without := rawSearch(t, c.h, c.req())
		useReranker(s, f, time.Second, 0)
		if with != without {
			t.Fatalf("%s differs:\n%s\n%s", c.name, with, without)
		}
	}
	if f.calls.Load() != 0 {
		t.Fatalf("the model was called %d times", f.calls.Load())
	}
}

func TestRerank_RESTReorders(t *testing.T) {
	s, f := rerankServer(t)
	for _, c := range rerankCases(s) {
		base := restSearch(t, c.h, c.req())
		if keys := rrKeys(base); len(keys) == 0 || keys[0] == "gamma" {
			t.Fatalf("%s: the fixture must not already rank gamma first: %v", c.name, keys)
		}
		req := c.req()
		req["rerank"] = rrEnabled()
		m := restSearch(t, c.h, req)
		keys := rrKeys(m)
		if len(keys) != 3 || keys[0] != "gamma" || skipped(m) != "" {
			t.Fatalf("%s: %v %v", c.name, keys, m["rerankSkipped"])
		}
		report := m["rerank"].(map[string]any)
		if report["model"] != "fake-rerank" || report["candidates"].(float64) < 3 || report["moves"] != nil {
			t.Fatalf("%s report: %v", c.name, report)
		}
	}
	if f.calls.Load() != int64(len(rerankCases(s))) {
		t.Fatalf("calls %d", f.calls.Load())
	}
}

func TestRerank_RESTSkipsAndKeepsTheSearch(t *testing.T) {
	s, f := rerankServer(t)
	modes := []struct {
		reason string
		set    func()
	}{
		{"timeout", func() { f.delay.Store(int64(300 * time.Millisecond)) }},
		{"error", func() { f.code.Store(http.StatusInternalServerError) }},
		{"invalidResponse", func() { f.body.Store(`{"results":[{"index":99,"relevance_score":1}]}`) }},
		{"notConfigured", func() { s.setRerank(nil, 0, "") }},
	}
	for _, mode := range modes {
		useReranker(s, f, 50*time.Millisecond, 0)
		f.delay.Store(0)
		f.code.Store(0)
		f.body.Store("")
		mode.set()
		for _, c := range rerankCases(s) {
			base := rrKeys(restSearch(t, c.h, c.req()))
			req := c.req()
			req["rerank"] = rrEnabled()
			m := restSearch(t, c.h, req)
			if skipped(m) != mode.reason || m["rerank"] != nil {
				t.Fatalf("%s/%s: %v", mode.reason, c.name, m)
			}
			if got := rrKeys(m); !sameKeys(got, base) {
				t.Fatalf("%s/%s: %v, want the search's own %v", mode.reason, c.name, got, base)
			}
		}
	}
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRerank_ExplainTopNAndPassages(t *testing.T) {
	s, _ := rerankServer(t)
	m := restSearch(t, s.handleHybridSearch, map[string]any{"collection": "rr", "query": rerankQuery, "topK": 3,
		"rerank": rrEnabled("explain", true, "topN", 1)})
	if keys := rrKeys(m); len(keys) != 1 || keys[0] != "gamma" {
		t.Fatalf("topN: %v", keys)
	}
	moves := m["rerank"].(map[string]any)["moves"].([]any)
	first := moves[0].(map[string]any)
	if first["id"] == "" || first["after"].(float64) != 1 || first["before"].(float64) == 1 || first["score"].(float64) != 2 {
		t.Fatalf("moves: %v", moves)
	}
	// The search's own explain flag reports the moves too.
	m = restSearch(t, s.handleVectorSearch, map[string]any{"collection": "rr", "query": rerankQuery, "explain": true,
		"rerank": rrEnabled()})
	if m["rerank"].(map[string]any)["moves"] == nil {
		t.Fatalf("explain: %v", m["rerank"])
	}
	// Bodies the caller did not ask for stay out.
	if doc := m["results"].([]any)[0].(map[string]any)["document"].(map[string]any); doc["contentMd"] != nil && doc["contentMd"] != "" {
		t.Fatalf("content leaked: %v", doc)
	}
	for _, c := range rerankCases(s) {
		req := c.req()
		req["format"], req["rerank"] = "passages", rrEnabled()
		m := restSearch(t, c.h, req)
		p := m["passages"].([]any)
		if len(p) == 0 || p[0].(map[string]any)["key"] != "gamma" || m["rerank"] == nil {
			t.Fatalf("%s passages: %v", c.name, m)
		}
	}
	// Chunk mode reranks the chunks the search cut.
	m = restSearch(t, s.handleVectorSearch, map[string]any{"collection": "rr", "query": rerankQuery, "retrievalMode": "chunk",
		"rerank": rrEnabled()})
	if keys := rrKeys(m); keys[0] != "gamma" {
		t.Fatalf("chunk mode: %v", keys)
	}
}

func TestRerank_ProfileDefault(t *testing.T) {
	s, _ := rerankServer(t)
	on := true
	if err := s.CollectionManager.Set("rr", &CollectionConfig{Retrieval: &RetrievalProfileDef{
		Rerank: &RerankOptions{Enabled: &on, Candidates: 3, TopN: 2}}}); err != nil {
		t.Fatal(err)
	}
	m := restSearch(t, s.handleHybridSearch, map[string]any{"collection": "rr", "query": rerankQuery, "topK": 3})
	if keys := rrKeys(m); len(keys) != 2 || keys[0] != "gamma" || m["rerank"].(map[string]any)["candidates"].(float64) != 3 {
		t.Fatalf("profile: %v %v", keys, m["rerank"])
	}
	m = restSearch(t, s.handleHybridSearch, map[string]any{"collection": "rr", "query": rerankQuery, "topK": 3,
		"rerank": map[string]any{"enabled": false}})
	if m["rerank"] != nil || len(rrKeys(m)) != 3 {
		t.Fatalf("request off: %v", m)
	}
	// Fields the request leaves out come from the profile.
	m = restSearch(t, s.handleHybridSearch, map[string]any{"collection": "rr", "query": rerankQuery, "topK": 3,
		"rerank": map[string]any{"topN": 3}})
	if len(rrKeys(m)) != 3 || m["rerank"] == nil {
		t.Fatalf("merge: %v", m)
	}
	bad := &RetrievalProfileDef{Rerank: &RerankOptions{Candidates: 500}}
	if err := bad.Validate(); err == nil {
		t.Fatal("candidates above the ceiling accepted in a profile")
	}
}

func TestRerank_CacheAndBudget(t *testing.T) {
	s, f := rerankServer(t)
	useReranker(s, f, time.Second, 100)
	req := func(q string) map[string]any {
		return map[string]any{"collection": "rr", "query": q, "limit": 3, "algorithm": "bm25", "rerank": rrEnabled()}
	}
	restSearch(t, s.handleFTS, req(rerankQuery))
	m := restSearch(t, s.handleFTS, req(rerankQuery))
	report := m["rerank"].(map[string]any)
	if f.calls.Load() != 1 || report["cached"] != report["candidates"] {
		t.Fatalf("cache: calls %d report %v", f.calls.Load(), report)
	}
	// One model call a day: the first is charged, the next is refused.
	setTestQuota(t, s, "rr", CollectionQuota{EmbeddingCallsPerDay: 1})
	if m := restSearch(t, s.handleFTS, req("pump")); skipped(m) != "" {
		t.Fatalf("first call: %v", m)
	}
	if got := s.quotas().budget.today("rr"); got.calls != 1 || got.tokens == 0 {
		t.Fatalf("charged: %+v", got)
	}
	if m := restSearch(t, s.handleFTS, req("seal")); skipped(m) != rerankSkipBudget {
		t.Fatalf("over budget: %v", m)
	}
	// A cached answer costs nothing, so it is not refused either way.
	if got := s.quotas().budget.today("rr"); got.calls != 1 {
		t.Fatalf("a skipped call was charged: %+v", got)
	}
}

func TestRerank_InvalidOptions(t *testing.T) {
	s, _ := rerankServer(t)
	for _, bad := range []map[string]any{{"enabled": true, "candidates": -1}, {"enabled": true, "topN": 5000}} {
		for _, c := range rerankCases(s) {
			req := c.req()
			req["rerank"] = bad
			if rec := doRequest(t, c.h, req); rec.Code == http.StatusOK {
				t.Fatalf("%s accepted %v", c.name, bad)
			}
		}
	}
	// A cross search by vector has no text for the model to read.
	vec := make([]float32, 64)
	vec[0] = 1
	m := restSearch(t, s.handleCrossSearch, map[string]any{"targetCollections": []string{"rr"}, "queryVector": vec,
		"rerank": rrEnabled()})
	if skipped(m) != rerankSkipNoQuery {
		t.Fatalf("no query: %v", m)
	}
}
