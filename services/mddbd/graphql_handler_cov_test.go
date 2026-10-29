package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A resolver that panics must answer with a GraphQL error, not take the
// server down or leak the panic value. A Server with no database is the
// cheapest way to make the stats resolver dereference nil.
func TestCovKGraphQLHandler_RecoversFromAResolverPanic(t *testing.T) {
	s := &Server{}
	h := s.newGraphQLHandler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql",
		bytes.NewBufferString(`{"query":"{ stats { databasePath } }"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("panic not turned into an error: %d %s", rec.Code, body)
	}
	if strings.Contains(body, "nil pointer") {
		t.Errorf("panic detail leaked to the client: %s", body)
	}
}

// With auth enabled, claims the HTTP middleware put on the request reach the
// GraphQL handler; without them the request still passes through, and the
// resolvers refuse it themselves.
func TestCovKGraphQLAuthMiddleware_Enabled(t *testing.T) {
	am, cleanup := authMwSetup(t)
	defer cleanup()
	s := &Server{AuthManager: am}

	var seen *JWTClaims
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		seen, _ = GetClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	mw := s.GraphQLAuthMiddleware(next)

	claims := &JWTClaims{Username: "admin", Admin: true}
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req = req.WithContext(context.WithValue(req.Context(), authContextKey, claims))
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if !reached || seen != claims || rec.Code != http.StatusNoContent {
		t.Fatalf("with claims: reached=%v claims=%v code=%d", reached, seen, rec.Code)
	}

	reached, seen = false, nil
	rec = httptest.NewRecorder()
	mw.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/graphql", nil))
	if !reached || seen != nil {
		t.Fatalf("without claims: reached=%v claims=%v", reached, seen)
	}
}
