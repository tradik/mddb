package main

import (
	"net/http"
	"testing"

	json "mddb/internal/jsonx"
)

// ---------- FTSSearchRequest struct field ----------

func TestFTSSearchRequest_FilterMeta(t *testing.T) {
	// Verify that FTSSearchRequest can carry a FilterMeta field and that it
	// round-trips through JSON correctly.
	req := FTSSearchRequest{
		Collection: "blog",
		Query:      "golang",
		Limit:      10,
		FilterMeta: map[string][]string{
			"tag":      {"go", "programming"},
			"category": {"tutorial"},
		},
	}

	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded FTSSearchRequest
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Collection != "blog" {
		t.Errorf("expected collection 'blog', got %q", decoded.Collection)
	}
	if decoded.Query != "golang" {
		t.Errorf("expected query 'golang', got %q", decoded.Query)
	}
	if decoded.FilterMeta == nil {
		t.Fatal("expected FilterMeta to be non-nil after round-trip")
	}
	if len(decoded.FilterMeta["tag"]) != 2 {
		t.Errorf("expected 2 tag values, got %d", len(decoded.FilterMeta["tag"]))
	}
	if len(decoded.FilterMeta["category"]) != 1 {
		t.Errorf("expected 1 category value, got %d", len(decoded.FilterMeta["category"]))
	}
}

// ---------- Integration: FTS search with metadata filtering ----------

func TestFTSFilterMeta_Integration(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	// Add documents with different metadata using the test helper.
	// addTestDoc calls s.addDocument which generates docID = genID(collection, key, lang).
	doc1 := addTestDoc(t, s, "blog", "go-intro", "en", "golang programming language introduction",
		map[string][]string{"tag": {"go"}, "level": {"beginner"}})
	doc2 := addTestDoc(t, s, "blog", "go-advanced", "en", "golang advanced concurrency patterns",
		map[string][]string{"tag": {"go"}, "level": {"advanced"}})
	doc3 := addTestDoc(t, s, "blog", "python-intro", "en", "python programming language introduction",
		map[string][]string{"tag": {"python"}, "level": {"beginner"}})
	doc4 := addTestDoc(t, s, "blog", "rust-intro", "en", "rust programming language systems",
		map[string][]string{"tag": {"rust"}, "level": {"beginner"}})

	// Index documents for FTS using the same docID that addDocument generated.
	if err := s.FTSIndex.Index("blog", doc1.ID, "golang programming language introduction"); err != nil {
		t.Fatalf("Index go-intro: %v", err)
	}
	if err := s.FTSIndex.Index("blog", doc2.ID, "golang advanced concurrency patterns"); err != nil {
		t.Fatalf("Index go-advanced: %v", err)
	}
	if err := s.FTSIndex.Index("blog", doc3.ID, "python programming language introduction"); err != nil {
		t.Fatalf("Index python-intro: %v", err)
	}
	if err := s.FTSIndex.Index("blog", doc4.ID, "rust programming language systems"); err != nil {
		t.Fatalf("Index rust-intro: %v", err)
	}

	// --- Test 1: Filter by tag=go should return only Go docs ---
	payload := FTSSearchRequest{
		Collection: "blog",
		Query:      "programming",
		Limit:      10,
		Algorithm:  "bm25",
		FilterMeta: map[string][]string{"tag": {"go"}},
	}
	rec := doRequest(t, s.handleFTS, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp1 FTSSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Only doc1 (go-intro) matches "programming" AND has tag=go
	// (doc2/go-advanced does not contain "programming")
	if resp1.Total != 1 {
		t.Errorf("tag=go filter: expected 1 result, got %d", resp1.Total)
	}
	if resp1.Total > 0 && resp1.Results[0].Document.ID != doc1.ID {
		t.Errorf("expected %s, got %s", doc1.ID, resp1.Results[0].Document.ID)
	}

	// --- Test 2: Filter by level=beginner should exclude go-advanced ---
	payload2 := FTSSearchRequest{
		Collection: "blog",
		Query:      "programming language",
		Limit:      10,
		Algorithm:  "bm25",
		FilterMeta: map[string][]string{"level": {"beginner"}},
	}
	rec2 := doRequest(t, s.handleFTS, payload2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec2.Code, rec2.Body.String())
	}

	var resp2 FTSSearchResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// go-intro, python-intro, rust-intro all have level=beginner and contain "programming language"
	if resp2.Total != 3 {
		t.Errorf("level=beginner filter: expected 3 results, got %d", resp2.Total)
	}
	for _, r := range resp2.Results {
		if r.Document.ID == doc2.ID {
			t.Error("go-advanced should be excluded by level=beginner filter")
		}
	}

	// --- Test 3: Filter with no matching meta should return empty ---
	payload3 := FTSSearchRequest{
		Collection: "blog",
		Query:      "programming",
		Limit:      10,
		Algorithm:  "bm25",
		FilterMeta: map[string][]string{"tag": {"java"}},
	}
	rec3 := doRequest(t, s.handleFTS, payload3)

	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec3.Code, rec3.Body.String())
	}

	var resp3 FTSSearchResponse
	if err := json.Unmarshal(rec3.Body.Bytes(), &resp3); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp3.Total != 0 {
		t.Errorf("tag=java filter: expected 0 results, got %d", resp3.Total)
	}

	// --- Test 4: Multiple filter keys (AND logic) ---
	payload4 := FTSSearchRequest{
		Collection: "blog",
		Query:      "programming",
		Limit:      10,
		Algorithm:  "bm25",
		FilterMeta: map[string][]string{
			"tag":   {"go"},
			"level": {"beginner"},
		},
	}
	rec4 := doRequest(t, s.handleFTS, payload4)

	if rec4.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec4.Code, rec4.Body.String())
	}

	var resp4 FTSSearchResponse
	if err := json.Unmarshal(rec4.Body.Bytes(), &resp4); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Only go-intro has both tag=go AND level=beginner
	if resp4.Total != 1 {
		t.Errorf("tag=go AND level=beginner: expected 1 result, got %d", resp4.Total)
	}

	// --- Test 5: No filterMeta returns all matching docs ---
	payload5 := FTSSearchRequest{
		Collection: "blog",
		Query:      "programming",
		Limit:      10,
		Algorithm:  "bm25",
	}
	rec5 := doRequest(t, s.handleFTS, payload5)

	if rec5.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec5.Code, rec5.Body.String())
	}

	var resp5 FTSSearchResponse
	if err := json.Unmarshal(rec5.Body.Bytes(), &resp5); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// go-intro, python-intro, rust-intro all contain "programming"
	if resp5.Total < 3 {
		t.Errorf("no filter: expected at least 3 results, got %d", resp5.Total)
	}
}

// ---------- FTS filter with empty collection ----------

func TestFTSFilterMeta_EmptyCollection(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	payload := FTSSearchRequest{
		Collection: "empty",
		Query:      "something",
		Limit:      10,
		Algorithm:  "bm25",
		FilterMeta: map[string][]string{"tag": {"go"}},
	}
	rec := doRequest(t, s.handleFTS, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp FTSSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Total != 0 {
		t.Errorf("expected 0 results for empty collection, got %d", resp.Total)
	}
}

// ---------- FTS filter with multiple values for same key (OR within key) ----------

func TestFTSFilterMeta_MultipleValuesOR(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()

	d1 := addTestDoc(t, s, "docs", "doc1", "en", "golang web framework",
		map[string][]string{"tag": {"go"}})
	_ = addTestDoc(t, s, "docs", "doc2", "en", "python web framework",
		map[string][]string{"tag": {"python"}})
	d3 := addTestDoc(t, s, "docs", "doc3", "en", "rust web framework",
		map[string][]string{"tag": {"rust"}})

	if err := s.FTSIndex.Index("docs", d1.ID, "golang web framework"); err != nil {
		t.Fatal(err)
	}
	// doc2 ID is generated by genID
	doc2ID := genID("docs", "doc2", "en")
	if err := s.FTSIndex.Index("docs", doc2ID, "python web framework"); err != nil {
		t.Fatal(err)
	}
	if err := s.FTSIndex.Index("docs", d3.ID, "rust web framework"); err != nil {
		t.Fatal(err)
	}

	// tag: [go, python] should match doc1 and doc2 (OR within same key)
	payload := FTSSearchRequest{
		Collection: "docs",
		Query:      "web framework",
		Limit:      10,
		Algorithm:  "bm25",
		FilterMeta: map[string][]string{"tag": {"go", "python"}},
	}
	rec := doRequest(t, s.handleFTS, payload)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp FTSSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Total != 2 {
		t.Errorf("tag=[go,python] OR filter: expected 2 results, got %d", resp.Total)
	}

	// Verify rust doc is excluded
	for _, r := range resp.Results {
		if r.Document.ID == d3.ID {
			t.Error("doc3 (rust) should be excluded by filter")
		}
	}
}
