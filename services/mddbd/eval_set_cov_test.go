package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCovHEvalQueriesForbiddenAndStoreFailure(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	if _, err := s.AddEvalQuery("c", EvalQuery{ID: "q1", Query: "x", ExpectedKeys: []string{"a"}}); err != nil {
		t.Fatal(err)
	}

	// With auth on and no caller, every method is refused before it acts.
	am, amCleanup := authMwSetup(t)
	defer amCleanup()
	s.AuthManager = am
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		rec := httptest.NewRecorder()
		body := `{"collection":"c","query":"y","expectedKeys":["b"]}`
		s.handleEvalQueries(rec, httptest.NewRequest(method, "/v1/eval-queries?collection=c&id=q1", strings.NewReader(body)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d, want 403", method, rec.Code)
		}
	}
	if set := s.evalSet("c"); len(set) != 1 || set[0].ID != "q1" {
		t.Fatalf("a refused request changed the set: %+v", set)
	}

	// A delete the database cannot store is a 400 and the cached set stays.
	s.AuthManager = nil
	_ = s.DB.Close()
	rec := httptest.NewRecorder()
	s.handleEvalQueries(rec, httptest.NewRequest(http.MethodDelete, "/v1/eval-queries?collection=c&id=q1", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete on a closed database: %d %s", rec.Code, rec.Body)
	}
	if set := s.evalSet("c"); len(set) != 1 {
		t.Fatalf("a failed delete changed the set: %+v", set)
	}
}
