package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Shared by the tests of /v1/web-search, /v1/rerank and /v1/extract/process
// (#449-#451): a server with the modules switched as asked, and one request
// through the registered routes — gate, module switch and handler.

// switchModules turns the named modules on, every other one as it defaults.
func switchModules(t *testing.T, s *Server, on ...string) {
	t.Helper()
	t.Setenv(featuresForceOffEnv, "")
	s.Features = NewFeatureManager(s.DB)
	if err := s.Features.LoadAll(); err != nil {
		t.Fatal(err)
	}
	for _, name := range on {
		if err := s.Features.Set(name, true, "test"); err != nil {
			t.Fatal(err)
		}
	}
}

// owuiCall sends one request through the routes as the principal c (none
// when nil), with the given headers.
func owuiCall(s *Server, c *JWTClaims, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	s.registerRoutes(mux, true)
	req := httptest.NewRequest(method, target, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c != nil {
		req = req.WithContext(context.WithValue(req.Context(), authContextKey, c))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAllowMethodAnswersJSON405(t *testing.T) {
	rec := httptest.NewRecorder()
	if allowMethod(rec, httptest.NewRequest(http.MethodGet, "/v1/rerank", nil), http.MethodPost) {
		t.Fatal("GET allowed on a POST endpoint")
	}
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost ||
		rec.Body.String() != `{"error":"method not allowed: use POST"}` {
		t.Errorf("405: %d %q %s", rec.Code, rec.Header().Get("Allow"), rec.Body.String())
	}
	if !allowMethod(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/", nil), http.MethodPut) {
		t.Error("the right method refused")
	}
}

// Every one of the three starts off and answers what any switched-off module
// answers; switched on, it reaches its handler.
func TestOpenWebUIModulesStartOff(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	switchModules(t, s)
	routes := map[string]string{"/v1/web-search": FeatureWebSearch, "/v1/rerank": FeatureRerankAPI, "/v1/extract/process": FeatureExtractAPI}
	for path, feature := range routes {
		if d, _ := featureByName(feature); d.Default {
			t.Errorf("%s is on by default", feature)
		}
		rec := owuiCall(s, nil, http.MethodGet, path, nil, nil)
		if rec.Code != http.StatusNotFound || rec.Body.String() != `{"error":"feature disabled","feature":"`+feature+`"}` {
			t.Errorf("%s off: %d %s", path, rec.Code, rec.Body.String())
		}
		if err := s.Features.Set(feature, true, "test"); err != nil {
			t.Fatal(err)
		}
		if rec := owuiCall(s, nil, http.MethodGet, path, nil, nil); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s on: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
