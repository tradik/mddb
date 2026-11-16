package main

import (
	"context"
	"mddb/internal/embedding"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The cross-search paths the GO-052 split touched.

func crossSearchREST(s *Server, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.handleCrossSearch(w, httptest.NewRequest(http.MethodPost, "/v1/cross-search", strings.NewReader(body)))
	return w
}

func TestCrossTextGroupsDocumentsWithoutTheKeyAlone(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	addTestDoc(t, s, "a", "one", "en", "alpha words", nil)
	addTestDoc(t, s, "b", "two", "en", "alpha more words", nil)
	w := crossSearchREST(s, `{"targetCollections":["a","b"],"query":"alpha","searchType":"fts","groupBy":{"key":"author","perGroup":1}}`)
	if w.Code != http.StatusOK || strings.Count(w.Body.String(), `"collection":`) != 2 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// Hybrid whose query cannot be embedded: each target adds nothing.
	s.Embedding = failingPartEmbedding{Provider: embedding.NewLexicalProvider(256), fail: "alpha"}
	s.VectorIndex.SetReady()
	w = crossSearchREST(s, `{"targetCollections":["a","b"],"query":"alpha","searchType":"hybrid"}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"collection":"a"`) {
		t.Fatalf("hybrid: %d %s", w.Code, w.Body)
	}
}

func TestCrossPermittedChecksTheSourceToo(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	am, _, amCleanup := setupTestAuthManager(t)
	defer amCleanup()
	s.AuthManager = am
	admin := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "root", Admin: true})
	if !s.crossPermitted(admin, CrossSearchRequest{TargetCollections: []string{"a"}, SourceCollection: "b"}) {
		t.Fatal("an administrator may read every collection")
	}
	tenant := context.WithValue(context.Background(), authContextKey, &JWTClaims{Username: "u", Tenant: "acme"})
	if s.crossPermitted(tenant, CrossSearchRequest{TargetCollections: []string{"acme/a"}, SourceCollection: "other"}) {
		t.Fatal("a tenant read a source outside its namespace")
	}
}
