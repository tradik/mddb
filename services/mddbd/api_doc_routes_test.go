package main

import (
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"mddb/internal/automationlog"
	"mddb/internal/spell"
	"mddb/internal/temporal"
)

// apiDocPath is the REST reference every registered route must appear in.
const apiDocPath = "../../docs/API.md"

// everyRESTRoute registers the HTTP API's routes with every optional
// subsystem on (auth, automation, temporal, spell, the Elasticsearch subset,
// pprof) and returns the patterns, sorted.
func everyRESTRoute(t *testing.T) []string {
	t.Helper()
	t.Setenv("MDDB_PPROF_ENABLED", "true")
	t.Setenv(esCompatEnv, "true")
	srv, cleanup := routeServer(t)
	t.Cleanup(cleanup)
	srv.AutomationManager = NewAutomationManager(srv.DB)
	srv.AutomationLogStore = automationlog.NewStore(srv.DB, time.Hour)
	srv.TemporalManager = temporal.NewTemporalManager(srv.DB)
	srv.SpellManager = spell.NewSpellManager(srv.DB)
	rec := &recordingMux{patterns: map[string]http.Handler{}}
	srv.registerRoutes(rec, true)
	routes := make([]string, 0, len(rec.patterns))
	for p := range rec.patterns {
		routes = append(routes, p)
	}
	sort.Strings(routes)
	return routes
}

// routeDocumented reports whether doc names the route pattern: as a heading,
// a catalogue row or a link, anywhere in the text. A pattern ending in "/"
// (a subtree such as /v1/automation/) must appear with its slash, as in
// "/v1/automation/:id"; an exact pattern must not run on into a longer path,
// so /v1/add is not satisfied by /v1/add-batch.
func routeDocumented(doc, route string) bool {
	if strings.HasSuffix(route, "/") {
		return strings.Contains(doc, route)
	}
	return regexp.MustCompile(regexp.QuoteMeta(route) + `(?:[^A-Za-z0-9_\-]|$)`).MatchString(doc)
}

// TestEveryRouteIsInAPIDoc keeps docs/API.md complete: every route
// registerRoutes can register must be named in it. Twenty-one routes had
// drifted out of it while staying in openapi.yaml and the code. Document a
// new route in API.md, or add a row to "Endpoints documented on topic pages"
// that links to the page that does.
func TestEveryRouteIsInAPIDoc(t *testing.T) {
	data, err := os.ReadFile(apiDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", apiDocPath, err)
	}
	doc := string(data)
	routes := everyRESTRoute(t)
	if len(routes) < 120 {
		t.Fatalf("registered %d routes: the recorder is broken", len(routes))
	}
	var missing []string
	for _, r := range routes {
		if !routeDocumented(doc, r) {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%s does not mention %d registered routes:\n  %s", apiDocPath, len(missing), strings.Join(missing, "\n  "))
	}
}

func TestRouteDocumentedMatchesWholePaths(t *testing.T) {
	for _, c := range []struct {
		doc, route string
		want       bool
	}{
		{"### POST /v1/add\n", "/v1/add", true},
		{"### POST /v1/add-batch\n", "/v1/add", false},
		{"see `/v1/add`.", "/v1/add", true},
		{"/v1/automation/:id", "/v1/automation/", true},
		{"/v1/automation", "/v1/automation/", false},
		{"POST /v1/documents:updateWhere", "/v1/documents:updateWhere", true},
	} {
		if got := routeDocumented(c.doc, c.route); got != c.want {
			t.Errorf("routeDocumented(%q, %q) = %v, want %v", c.doc, c.route, got, c.want)
		}
	}
}
