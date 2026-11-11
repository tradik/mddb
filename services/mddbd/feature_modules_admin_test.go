package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"mddb/internal/webhooks"
	"mddb/proto"
)

// GO-049, 2.19.0 extension: the management surfaces of webhooks, schemas,
// aliases, cross search, search tuning and FTS tuning are modules too. A
// switch hides a module's management; what it already stored keeps working,
// so switching one off never breaks access to data.

func TestNewModulesOwnTheirEntryPoints(t *testing.T) {
	for _, c := range []struct{ feature, route, rpc, tool, field string }{
		{FeatureWebhooks, "/v1/webhooks/delete", "RegisterWebhook", "list_webhooks", "Mutation.registerWebhook"},
		{FeatureSchemas, "/v1/validate", "ValidateDocument", "validate_document", "Query.schemas"},
		{FeatureAliases, "/v1/aliases/", "", "", ""},
		{FeatureCrossSearch, "/v1/cross-search", "CrossSearch", "cross_search", ""},
		{FeatureSearchTuning, "/v1/vector-projection", "SearchAdvisor", "search_advisor", ""},
		{FeatureFTSTuning, "/v1/stopwords", "DeleteSynonym", "add_stopwords", ""},
	} {
		def, ok := featureByName(c.feature)
		if !ok || !def.Default || def.Description == "" {
			t.Errorf("%s: registered %v, %+v", c.feature, ok, def)
		}
		if got := routeFeature(c.route); got != c.feature {
			t.Errorf("route %s: %q", c.route, got)
		}
		if got := rpcFeature("/mddb.MDDB/" + c.rpc); c.rpc != "" && got != c.feature {
			t.Errorf("rpc %s: %q", c.rpc, got)
		}
		if got := toolFeature(c.tool); c.tool != "" && got != c.feature {
			t.Errorf("tool %s: %q", c.tool, got)
		}
		if got := graphqlFeatureFields[c.field]; c.field != "" && got != c.feature {
			t.Errorf("field %s: %q", c.field, got)
		}
	}
	// The paths that resolve an alias or apply a schema are core.
	for _, p := range []string{"/v1/add", "/v1/get", "/v1/vector-search", "/v1/hybrid-search", "/v1/fts-languages"} {
		if routeFeature(p) != "" {
			t.Errorf("route %s is core", p)
		}
	}
}

// The gate tables name RPCs that exist and GraphQL fields of the schema.
func TestGateTablesNameRealEntryPoints(t *testing.T) {
	rpcs := map[string]bool{}
	for _, m := range proto.MDDB_ServiceDesc.Methods {
		rpcs[m.MethodName] = true
	}
	for _, m := range proto.MDDB_ServiceDesc.Streams {
		rpcs[m.StreamName] = true
	}
	for m := range grpcFeatureMethods {
		if !rpcs[m] {
			t.Errorf("rpc %s does not exist", m)
		}
	}
	sdl, err := os.ReadFile("graphql/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	for f := range graphqlFeatureFields {
		name := f[strings.Index(f, ".")+1:]
		if !strings.Contains(string(sdl), "  "+name+"(") && !strings.Contains(string(sdl), "  "+name+":") {
			t.Errorf("GraphQL field %s does not exist", f)
		}
	}
}

// switchOff turns modules off as root through the REST API.
func switchOff(t *testing.T, s *Server, names ...string) {
	t.Helper()
	for _, n := range names {
		if rec := featureREST(t, s, root, http.MethodPut, "/v1/features/"+n, `{"enabled":false}`); rec.Code != http.StatusOK {
			t.Fatalf("switch %s off: %d %s", n, rec.Code, rec.Body.String())
		}
	}
}

// wantFeatureOff checks a request answers the module's 404.
func wantFeatureOff(t *testing.T, s *Server, method, path, body, feature string) {
	t.Helper()
	rec := featureREST(t, s, root, method, path, body)
	if rec.Code != http.StatusNotFound || rec.Body.String() != `{"error":"feature disabled","feature":"`+feature+`"}` {
		t.Errorf("%s %s while %s is off: %d %s", method, path, feature, rec.Code, rec.Body.String())
	}
}

func TestSwitchedOffAliasesStillResolve(t *testing.T) {
	s := featureServer(t)
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "books-v2", "a", "en", "alias target", nil)
	if err := s.SetAlias("books", "books-v2"); err != nil {
		t.Fatal(err)
	}
	switchOff(t, s, FeatureAliases)
	wantFeatureOff(t, s, http.MethodGet, "/v1/aliases", "", FeatureAliases)
	// Resolution sits in front of every route (main.go), not behind a switch.
	mux := http.NewServeMux()
	s.registerRoutes(mux, true)
	rec := httptest.NewRecorder()
	asUser(root, s.withAliases(mux)).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/get", strings.NewReader(`{"collection":"books","key":"a","lang":"en"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alias target") {
		t.Errorf("an existing alias resolves while the module is off: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSwitchedOffSchemasStillValidateWrites(t *testing.T) {
	s := featureServer(t)
	if err := s.SchemaManager.Set("docs", `{"type":"object","required":["author"]}`); err != nil {
		t.Fatal(err)
	}
	switchOff(t, s, FeatureSchemas)
	wantFeatureOff(t, s, http.MethodPost, "/v1/schema/get", `{"collection":"docs"}`, FeatureSchemas)
	wantFeatureOff(t, s, http.MethodPost, "/v1/validate", `{"collection":"docs"}`, FeatureSchemas)
	rec := featureREST(t, s, root, http.MethodPost, "/v1/add", `{"collection":"docs","key":"k","lang":"en","contentMd":"x"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "author") {
		t.Errorf("a stored schema is enforced while the module is off: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSwitchedOffWebhooksStillDeliver(t *testing.T) {
	s := featureServer(t)
	s.WebhookManager = webhooks.NewWebhookManager(s.DB)
	if err := s.WebhookManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct{ Event string }
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p.Event
	}))
	t.Cleanup(hook.Close)
	if _, err := s.WebhookManager.Register(hook.URL, []string{"doc.deleted"}, "docs"); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "docs", "gone", "en", "x", nil)
	switchOff(t, s, FeatureWebhooks)
	wantFeatureOff(t, s, http.MethodGet, "/v1/webhooks", "", FeatureWebhooks)
	if rec := featureREST(t, s, root, http.MethodPost, "/v1/delete", `{"collection":"docs","key":"gone","lang":"en"}`); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case e := <-got:
		if e != "doc.deleted" {
			t.Errorf("delivered %q", e)
		}
	case <-time.After(5 * time.Second):
		t.Error("a registered webhook is not delivered while the module is off")
	}
}

func TestSwitchedOffFTSTuningStillExpandsSynonyms(t *testing.T) {
	s := featureServer(t)
	if err := s.SynonymManager.Set("docs", "car", []string{"automobile"}); err != nil {
		t.Fatal(err)
	}
	switchOff(t, s, FeatureFTSTuning, FeatureCrossSearch, FeatureSearchTuning)
	wantFeatureOff(t, s, http.MethodGet, "/v1/synonyms?collection=docs", "", FeatureFTSTuning)
	wantFeatureOff(t, s, http.MethodPost, "/v1/cross-search", `{}`, FeatureCrossSearch)
	wantFeatureOff(t, s, http.MethodPost, "/v1/search-advisor", `{}`, FeatureSearchTuning)
	s.FTSIndex.SetSynonymManager(s.SynonymManager)
	rec := featureREST(t, s, root, http.MethodPost, "/v1/fts", `{"collection":"docs","query":"car"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"synonymsActive":true`) || !strings.Contains(rec.Body.String(), "automobil") {
		t.Errorf("stored synonyms still expand a query while the module is off: %d %s", rec.Code, rec.Body.String())
	}
}
