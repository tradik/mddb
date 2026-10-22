package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The match set of a search is every document that satisfies the query and
// its filters. Filters, boosts and facetScope "all" act on that set; the limit
// only decides how much of it is returned. Until #311 the limit was applied
// first, so a filter could only keep what happened to rank in the global top
// N: 100 matching documents, limit 50, and a filter returned 7.

// seedMatchSet indexes n documents that all contain "library"; every tenth is
// category=rare, and the rare ones score lowest, so none is in the top n/10.
func seedMatchSet(t *testing.T, s *Server, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		cat, reps := "common", 2+i%5
		if i%10 == 0 {
			cat, reps = "rare", 1
		}
		content := strings.Repeat("library ", reps) + fmt.Sprintf("entry %d with some more words to dilute", i)
		doc := addTestDoc(t, s, "ports", fmt.Sprintf("p%03d", i), "en", content,
			map[string][]string{"category": {cat}})
		if err := s.FTSIndex.Index("ports", doc.ID, doc.ContentMD); err != nil {
			t.Fatal(err)
		}
	}
}

func ftsSearch(t *testing.T, s *Server, body map[string]any) (FTSSearchResponse, int) {
	t.Helper()
	rec := doRequest(t, s.handleFTS, body)
	var resp FTSSearchResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
	}
	return resp, rec.Code
}

func TestFTSFilterMetaSearchesTheWholeMatchSet(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedMatchSet(t, s, 200) // 20 rare, all below the top 20

	resp, code := ftsSearch(t, s, map[string]any{
		"collection": "ports", "query": "library", "limit": 20,
		"filterMeta": map[string][]string{"category": {"rare"}},
	})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(resp.Results) != 20 {
		t.Errorf("20 documents match the filter and the limit is 20, got %d", len(resp.Results))
	}
	for _, r := range resp.Results {
		if r.Document.Meta["category"][0] != "rare" {
			t.Errorf("%s is not category=rare", r.Document.Key)
		}
	}
}

func TestFTSBoostCanLiftADocumentFromBelowTheLimit(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedMatchSet(t, s, 200)

	resp, _ := ftsSearch(t, s, map[string]any{
		"collection": "ports", "query": "library", "limit": 5,
		"boost": map[string]float64{"category:rare": 100},
	})
	if len(resp.Results) == 0 || resp.Results[0].Document.Meta["category"][0] != "rare" {
		t.Errorf("a 100x boost on category=rare did not bring a rare document to the top: %v", resultKeys(resp))
	}
}

func TestFTSFacetScopeAllCountsEveryMatch(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedMatchSet(t, s, 200)

	resp, code := ftsSearch(t, s, map[string]any{
		"collection": "ports", "query": "library", "limit": 10,
		"facetBy": []string{"category"}, "facetScope": "all",
	})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(resp.Results) != 10 {
		t.Errorf("results = %d, want the page of 10", len(resp.Results))
	}
	if resp.Matched != 200 {
		t.Errorf("matched = %d, want 200", resp.Matched)
	}
	got := map[string]int{}
	for _, b := range resp.Facets["category"] {
		got[b.Value] = b.Count
	}
	if got["common"] != 180 || got["rare"] != 20 {
		t.Errorf("category facets = %v, want common=180 rare=20", got)
	}

	// The default stays what it was: counts over the returned page.
	resp, _ = ftsSearch(t, s, map[string]any{
		"collection": "ports", "query": "library", "limit": 10, "facetBy": []string{"category"},
	})
	n := 0
	for _, b := range resp.Facets["category"] {
		n += b.Count
	}
	if n != 10 {
		t.Errorf("default facet counts add up to %d, want the 10 returned", n)
	}
}

func TestFTSFacetScopeAllRespectsTheFilter(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedMatchSet(t, s, 200)

	resp, _ := ftsSearch(t, s, map[string]any{
		"collection": "ports", "query": "library", "limit": 5,
		"filterMeta": map[string][]string{"category": {"rare"}},
		"facetBy":    []string{"category"}, "facetScope": "all",
	})
	if resp.Matched != 20 {
		t.Errorf("matched = %d, want the 20 rare documents", resp.Matched)
	}
	if b := resp.Facets["category"]; len(b) != 1 || b[0].Value != "rare" || b[0].Count != 20 {
		t.Errorf("category facets = %v, want only rare=20", b)
	}
}

func TestFTSRejectsAnUnknownFacetScope(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	if _, code := ftsSearch(t, s, map[string]any{
		"collection": "ports", "query": "library", "facetBy": []string{"category"}, "facetScope": "everything",
	}); code != http.StatusBadRequest {
		t.Errorf("status %d, want 400 for an unknown facetScope", code)
	}
}

func TestHybridFilterMetaSearchesTheWholeTextMatchSet(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	seedMatchSet(t, s, 200)

	rec := doRequest(t, s.handleHybridSearch, map[string]any{
		"collection": "ports", "query": "library", "topK": 10,
		"filterMeta": map[string][]string{"category": {"rare"}},
	})
	var resp HybridSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("status %d: %v", rec.Code, err)
	}
	if len(resp.Results) != 10 {
		t.Errorf("20 rare documents match, topK is 10, got %d", len(resp.Results))
	}
}

func resultKeys(resp FTSSearchResponse) []string {
	out := make([]string, len(resp.Results))
	for i, r := range resp.Results {
		out[i] = r.Document.Key
	}
	return out
}

// A metadata value may itself contain '|', the index-key separator. The
// value/ID boundary is then found by matching the ID, not by splitting.
func TestFacetsOverMatchesHandleASeparatorInTheValue(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	a := addTestDoc(t, s, "ports", "a", "en", "x", map[string][]string{"origin": {"net|www"}, "arch": {"amd64"}})
	b := addTestDoc(t, s, "ports", "b", "en", "x", map[string][]string{"origin": {"net|www"}})
	_ = addTestDoc(t, s, "ports", "c", "en", "x", map[string][]string{"origin": {"net|www"}})

	got := s.computeFacetsOverMatches("ports", map[string]bool{a.ID: true, b.ID: true},
		[]string{"origin", "arch", "", "missing"}, 0)
	if o := got["origin"]; len(o) != 1 || o[0].Value != "net|www" || o[0].Count != 2 {
		t.Errorf("origin = %v, want net|www=2 (c is not a match)", o)
	}
	if a := got["arch"]; len(a) != 1 || a[0].Count != 1 {
		t.Errorf("arch = %v, want amd64=1", a)
	}
	if m, ok := got["missing"]; !ok || len(m) != 0 {
		t.Errorf("a key no document has must be present and empty, got %v (present=%v)", m, ok)
	}
	if s.computeFacetsOverMatches("ports", nil, nil, 0) != nil {
		t.Error("no facetBy should give no facets")
	}
}
