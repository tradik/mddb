package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// covGCross posts a cross-search request, optionally as a caller with claims.
func covGCross(t *testing.T, s *Server, body any, claims *JWTClaims) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(b))
	if claims != nil {
		req = covGWithClaims(req, claims)
	}
	rec := httptest.NewRecorder()
	s.handleCrossSearch(rec, req)
	return rec
}

func TestCovGCrossSearchPermissions(t *testing.T) {
	s := covGServer(t)
	covGEnableAuth(t, s)
	mine := "acme" + TenantSeparator + "docs"

	// Anonymous: refused on the first target.
	covGWantCode(t, covGCross(t, s, map[string]any{"targetCollections": []string{mine}, "query": "x"}, nil),
		http.StatusForbidden)

	// A tenant may read its own target but not a source outside its namespace.
	tenant := &JWTClaims{Username: "u", Tenant: "acme", Admin: true}
	covGWantCode(t, covGCross(t, s, map[string]any{"targetCollections": []string{mine},
		"sourceCollection": "other", "sourceDocID": "d"}, tenant), http.StatusForbidden)
}

func TestCovGCrossSearchQueryVectorSources(t *testing.T) {
	s := covGVectorServer(t)
	src := covGEmbedded(t, s, "src", "seed", "seed text", nil)
	target := covGEmbedded(t, s, "dst", "match", "target text", nil)

	// From a source document's stored embedding.
	rec := covGCross(t, s, map[string]any{"targetCollections": []string{"dst"},
		"sourceCollection": "src", "sourceDocID": src.ID + "#0"}, nil)
	covGWantCode(t, rec, http.StatusOK)
	var resp CrossSearchResponse
	covGDecode(t, rec, &resp)
	if resp.Total != 1 || resp.Results[0].Document.ID != target.ID || resp.SourceDocID != src.ID+"#0" {
		t.Errorf("source-doc search: %+v", resp)
	}

	// sourceDocID needs its collection.
	rec = covGCross(t, s, map[string]any{"targetCollections": []string{"dst"}, "sourceDocID": src.ID}, nil)
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "sourceCollection required") {
		t.Errorf("body: %s", rec.Body.String())
	}

	// A source document without an embedding cannot seed a search.
	rec = covGCross(t, s, map[string]any{"targetCollections": []string{"dst"},
		"sourceCollection": "src", "sourceDocID": "nope"}, nil)
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "has no embedding") {
		t.Errorf("body: %s", rec.Body.String())
	}

	// A failing provider is reported.
	s.Embedding = covGFailEmbedding{&mockEmbedding{dims: 3, model: "m"}}
	rec = covGCross(t, s, map[string]any{"targetCollections": []string{"dst"}, "query": "x"}, nil)
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "failed to embed query") {
		t.Errorf("body: %s", rec.Body.String())
	}

	// No provider and nothing else to search with.
	s.Embedding = nil
	rec = covGCross(t, s, map[string]any{"targetCollections": []string{"dst"}, "query": "x"}, nil)
	covGWantCode(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "no embedding provider") {
		t.Errorf("body: %s", rec.Body.String())
	}
}
