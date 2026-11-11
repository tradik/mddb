package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
)

// Every API error is the server's JSON shape. With MDDB_TEMPORAL or
// MDDB_SPELL off their routes were not registered, so the mux answered its
// plain-text "404 page not found" — which a client parsing the body as JSON
// could not read, and which looked like a typo in the path rather than a
// module switched off.

func TestDisabledModulesAnswerFeatureDisabledInJSON(t *testing.T) {
	srv, cleanup := routeServer(t)
	defer cleanup()
	mux := http.NewServeMux()
	srv.registerRoutes(mux, false)

	for path, feature := range map[string]string{
		"/v1/temporal/query":     "temporal",
		"/v1/temporal/hot":       "temporal",
		"/v1/temporal/histogram": "temporal",
		"/v1/spell-suggest":      "spell",
		"/v1/spell-cleanup":      "spell",
		"/v1/spell-dictionary":   "spell",
	} {
		rec := httptest.NewRecorder()
		withJSON(mux).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s: not JSON: %q", path, rec.Body.String())
			continue
		}
		if body["error"] != "feature disabled" || body["feature"] != feature || len(body) != 2 {
			t.Errorf("%s: %v", path, body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: content type %q", path, ct)
		}
	}
}
