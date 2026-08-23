package main

import (
	"mddb/internal/fts"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// --- Query Parser Tests ---

// --- Wildcard Match Tests ---

// --- Positional Index Tests ---

// --- Phrase Search Tests ---

// --- Proximity Search Tests ---

// --- Wildcard Search Tests ---

// --- Boolean Search Tests ---

// --- Range Search Tests ---

func TestSearchRange_Timestamp(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	addTestDoc(t, s, "blog", "old-post", "en", "old content", nil)
	addTestDoc(t, s, "blog", "new-post", "en", "new content", nil)

	// Range filter: documents added in the last hour
	now := currentUnix()
	results, err := s.SearchRange("blog", []RangeFilter{
		{Field: "addedAt", Gte: itoa(int(now - 3600)), Lte: itoa(int(now + 60))},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results within time range, got %d", len(results))
	}
}

func TestFilterByRange_MetaField(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	doc1 := addTestDoc(t, s, "shop", "item1", "en", "cheap item", map[string][]string{"price": {"10"}})
	doc2 := addTestDoc(t, s, "shop", "item2", "en", "mid item", map[string][]string{"price": {"50"}})
	doc3 := addTestDoc(t, s, "shop", "item3", "en", "expensive item", map[string][]string{"price": {"200"}})

	input := []fts.FTSResult{
		{DocID: doc1.ID, Score: 1.0},
		{DocID: doc2.ID, Score: 1.0},
		{DocID: doc3.ID, Score: 1.0},
	}

	// Filter: price between 20 and 100
	results, err := s.FilterByRange("shop", input, []RangeFilter{
		{Field: "price", Gte: "20", Lte: "100"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result in range 20-100, got %d", len(results))
	}
	if results[0].DocID != doc2.ID {
		t.Fatalf("expected doc %s, got %s", doc2.ID, results[0].DocID)
	}
}

// --- HTTP Handler Integration Tests ---

func TestHandleFTS_PhraseMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	doc := addTestDoc(t, s, "blog", "ml", "en", "introduction to machine learning algorithms", nil)
	_ = s.FTSIndex.Index("blog", doc.ID, doc.ContentMD)
	_ = s.FTSIndex.IndexPositions("blog", doc.ID, doc.ContentMD)

	body := `{"collection":"blog","query":"\"machine learning\"","mode":"auto"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/fts", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleFTS(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d; body: %s", w.Result().StatusCode, w.Body.String())
	}

	var resp FTSSearchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "phrase" {
		t.Fatalf("expected mode=phrase, got %s", resp.Mode)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 result, got %d", resp.Total)
	}
}

func TestHandleFTS_WildcardMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	doc := addTestDoc(t, s, "blog", "go-post", "en", "golang programming language guide", nil)
	_ = s.FTSIndex.Index("blog", doc.ID, doc.ContentMD)

	body := `{"collection":"blog","query":"prog*","mode":"wildcard"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/fts", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleFTS(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var resp FTSSearchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "wildcard" {
		t.Fatalf("expected mode=wildcard, got %s", resp.Mode)
	}
	if resp.Total == 0 {
		t.Fatal("expected at least 1 result for prog*")
	}
}

func TestHandleFTS_BooleanMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	doc1 := addTestDoc(t, s, "blog", "both", "en", "rust programming systems", nil)
	_ = s.FTSIndex.Index("blog", doc1.ID, doc1.ContentMD)

	doc2 := addTestDoc(t, s, "blog", "rust-only", "en", "rust language overview", nil)
	_ = s.FTSIndex.Index("blog", doc2.ID, doc2.ContentMD)

	body := `{"collection":"blog","query":"rust AND programming","mode":"boolean"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/fts", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleFTS(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var resp FTSSearchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "boolean" {
		t.Fatalf("expected mode=boolean, got %s", resp.Mode)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 result for AND, got %d", resp.Total)
	}
}

func TestHandleFTS_RangeFilter(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	doc1 := addTestDoc(t, s, "shop", "item1", "en", "cheap widget", map[string][]string{"price": {"10"}})
	_ = s.FTSIndex.Index("shop", doc1.ID, doc1.ContentMD)

	doc2 := addTestDoc(t, s, "shop", "item2", "en", "expensive widget", map[string][]string{"price": {"500"}})
	_ = s.FTSIndex.Index("shop", doc2.ID, doc2.ContentMD)

	body := `{"collection":"shop","query":"widget","rangeMeta":[{"field":"price","gte":"100","lte":"1000"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/fts", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleFTS(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d; body: %s", w.Result().StatusCode, w.Body.String())
	}

	var resp FTSSearchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Fatalf("expected 1 result with price range, got %d", resp.Total)
	}
}

func TestHandleFTS_AutoDetectMode(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	doc := addTestDoc(t, s, "blog", "post1", "en", "golang web programming tutorial", nil)
	_ = s.FTSIndex.Index("blog", doc.ID, doc.ContentMD)

	// Simple query with no mode specified should auto-detect as "simple"
	body := `{"collection":"blog","query":"golang"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/fts", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleFTS(w, req)

	if w.Result().StatusCode != 200 {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}

	var resp FTSSearchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Mode != "simple" {
		t.Fatalf("expected mode=simple for plain query, got %s", resp.Mode)
	}
}
