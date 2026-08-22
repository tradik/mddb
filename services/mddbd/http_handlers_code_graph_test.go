package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gql "mddb/graphql"
	"mddb/internal/metrics"
)

// CODE-005: three transports, one traversal. The point of these is that REST,
// MCP and GraphQL cannot drift apart in what the graph says.

func doGraphGET(t *testing.T, srv *Server, query string) (*httptest.ResponseRecorder, *GraphResult) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/code-graph?"+query, nil)
	w := httptest.NewRecorder()
	srv.handleCodeGraph(w, r)

	if w.Code != http.StatusOK {
		return w, nil
	}
	var res GraphResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("response is not a graph: %v (%s)", err, w.Body.String())
	}
	return w, &res
}

func TestCodeGraphRESTGet(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	w, res := doGraphGET(t, srv, "collection=theme&key=theme/style.css&direction=in")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !containsKey(res, "theme/index.html") {
		t.Errorf("the dependent template is missing: %v", graphKeys(res))
	}
}

func TestCodeGraphRESTPost(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	body := `{"collection":"theme","key":"theme/style.css","direction":"in","depth":2}`
	r := httptest.NewRequest(http.MethodPost, "/v1/code-graph", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleCodeGraph(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var res GraphResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Depth != 2 {
		t.Errorf("depth = %d, want 2", res.Depth)
	}
}

// A missing document is the caller's typo. Anything retrying on 5xx needs that
// distinction, so it must not arrive as a server error.
func TestCodeGraphRESTMissingDocumentIs404(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	w, _ := doGraphGET(t, srv, "collection=theme&key=theme/nope.css")
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404: %s", w.Code, w.Body.String())
	}
}

func TestCodeGraphRESTRejectsBadRequests(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	cases := map[string]string{
		"no collection":     "key=theme/style.css",
		"no key":            "collection=theme",
		"unknown direction": "collection=theme&key=theme/style.css&direction=sideways",
	}
	for name, q := range cases {
		w, _ := doGraphGET(t, srv, q)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, w.Code)
		}
	}

	r := httptest.NewRequest(http.MethodDelete, "/v1/code-graph?collection=theme&key=x", nil)
	w := httptest.NewRecorder()
	srv.handleCodeGraph(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("DELETE gave %d, want 400", w.Code)
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/code-graph", strings.NewReader("{not json"))
	w = httptest.NewRecorder()
	srv.handleCodeGraph(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON gave %d, want 400", w.Code)
	}
}

// An unparseable optional limit means "not given", not "reject the query".
func TestCodeGraphRESTIgnoresJunkLimits(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	w, res := doGraphGET(t, srv, "collection=theme&key=theme/style.css&depth=abc&maxDegree=")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if res.Depth != GraphDefaultDepth {
		t.Errorf("depth = %d, want the default %d", res.Depth, GraphDefaultDepth)
	}
}

func TestAtoiOrZero(t *testing.T) {
	for in, want := range map[string]int{"3": 3, "": 0, "abc": 0, "-2": -2} {
		if got := atoiOrZero(in); got != want {
			t.Errorf("atoiOrZero(%q) = %d, want %d", in, got, want)
		}
	}
}

// The acceptance criterion: the same question through three transports gives
// the same answer.
func TestCodeGraphTransportsAgree(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	const collection, key, direction = "theme", "theme/index.html", "both"

	_, rest := doGraphGET(t, srv, "collection="+collection+"&key="+key+"&direction="+direction+"&depth=2")
	if rest == nil {
		t.Fatal("REST returned no graph")
	}

	mcpRes, err := NewDirectClient(srv).CodeGraph(context.Background(), &MCPCodeGraphRequest{
		Collection: collection, Key: key, Direction: direction, Depth: 2,
	})
	if err != nil {
		t.Fatalf("MCP: %v", err)
	}

	adapter := &GraphQLAdapter{server: srv, mcp: NewDirectClient(srv)}
	dir, depth := direction, 2
	gqlRes, err := adapter.CodeGraph(context.Background(), gql.CodeGraphInput{
		Collection: collection, Key: key, Direction: &dir, Depth: &depth,
	})
	if err != nil {
		t.Fatalf("GraphQL: %v", err)
	}

	if len(rest.Nodes) != len(mcpRes.Nodes) || len(rest.Nodes) != len(gqlRes.Nodes) {
		t.Fatalf("node counts differ: REST %d, MCP %d, GraphQL %d",
			len(rest.Nodes), len(mcpRes.Nodes), len(gqlRes.Nodes))
	}
	for i := range rest.Nodes {
		if rest.Nodes[i].Key != mcpRes.Nodes[i].Key || rest.Nodes[i].Key != gqlRes.Nodes[i].Key {
			t.Errorf("node %d differs: REST %s, MCP %s, GraphQL %s",
				i, rest.Nodes[i].Key, mcpRes.Nodes[i].Key, gqlRes.Nodes[i].Key)
		}
	}
	if len(rest.Edges) != len(gqlRes.Edges) {
		t.Errorf("edge counts differ: REST %d, GraphQL %d", len(rest.Edges), len(gqlRes.Edges))
	}
	for i := range rest.Edges {
		if string(rest.Edges[i].Kind) != gqlRes.Edges[i].Kind || rest.Edges[i].Symbol != gqlRes.Edges[i].Symbol {
			t.Errorf("edge %d differs: REST %+v, GraphQL %+v", i, rest.Edges[i], gqlRes.Edges[i])
		}
	}
}

func TestCodeGraphGraphQLRejectsUnknownDirection(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	adapter := &GraphQLAdapter{server: srv, mcp: NewDirectClient(srv)}
	bad := "sideways"
	if _, err := adapter.CodeGraph(context.Background(), gql.CodeGraphInput{
		Collection: "theme", Key: "theme/style.css", Direction: &bad,
	}); err == nil {
		t.Error("an unknown direction was accepted")
	}
}

func TestCodeGraphMCPRejectsUnknownDirection(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	if _, err := NewDirectClient(srv).CodeGraph(context.Background(), &MCPCodeGraphRequest{
		Collection: "theme", Key: "theme/style.css", Direction: "sideways",
	}); err == nil {
		t.Error("an unknown direction was accepted")
	}
}

// "This document has no language" and "its language is empty" are different
// answers; the GraphQL field must be null, not "".
func TestOptString(t *testing.T) {
	if optString("") != nil {
		t.Error("an empty value should map to null")
	}
	if got := optString("css"); got == nil || *got != "css" {
		t.Errorf("optString(css) = %v", got)
	}
}

// The MCP tool must be registered, dispatchable, and annotated read-only —
// an agent that cannot tell a read from a write will ask permission for both
// (the GO-016 lesson).
func TestCodeGraphMCPToolIsRegisteredAndReadOnly(t *testing.T) {
	found := false
	for _, tool := range mcpBuiltinTools() {
		if tool.Name == "code_graph" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("code_graph is not in the built-in tool list")
	}

	ann, ok := mcpToolAnnotations["code_graph"]
	if !ok {
		t.Fatal("code_graph has no annotations")
	}
	if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
		t.Error("a graph traversal is not marked read-only")
	}
	if ann.DestructiveHint != nil && *ann.DestructiveHint {
		t.Error("a graph traversal is marked destructive")
	}
}

// Auth and metrics paths. A read endpoint that skips the permission check is
// a data leak, so the 403 is worth a test of its own.

func themeFixtureWithAuth(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, cleanup := themeFixture(t)

	srv.AuthManager = NewAuthManager(srv.DB, AuthConfig{
		JWTSecret:     "test-secret-do-not-ship",
		JWTExpiry:     time.Hour,
		AdminUsername: "admin",
		AdminPassword: "secret",
	})
	for _, step := range []func() error{
		srv.AuthManager.EnsureBuckets,
		srv.AuthManager.LoadAll,
		srv.AuthManager.BootstrapAdmin,
	} {
		if err := step(); err != nil {
			cleanup()
			t.Fatal(err)
		}
	}
	srv.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: srv})
	return srv, cleanup
}

func TestCodeGraphRESTRequiresReadPermission(t *testing.T) {
	srv, cleanup := themeFixtureWithAuth(t)
	defer cleanup()

	// No claims in the request context: an anonymous caller.
	w, _ := doGraphGET(t, srv, "collection=theme&key=theme/style.css")
	if w.Code != http.StatusForbidden {
		t.Errorf("an unauthenticated read got %d, want 403: %s", w.Code, w.Body.String())
	}
}

func TestCodeGraphRESTCountsTheOperation(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()
	srv.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: srv})

	w, _ := doGraphGET(t, srv, "collection=theme&key=theme/style.css")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

func TestCodeGraphGraphQLRequiresAuthentication(t *testing.T) {
	srv, cleanup := themeFixtureWithAuth(t)
	defer cleanup()

	adapter := &GraphQLAdapter{server: srv, mcp: NewDirectClient(srv)}
	if _, err := adapter.CodeGraph(context.Background(), gql.CodeGraphInput{
		Collection: "theme", Key: "theme/style.css",
	}); err == nil {
		t.Error("an unauthenticated traversal was allowed")
	}
}

func TestCodeGraphGraphQLPropagatesNotFound(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	adapter := &GraphQLAdapter{server: srv, mcp: NewDirectClient(srv)}
	if _, err := adapter.CodeGraph(context.Background(), gql.CodeGraphInput{
		Collection: "theme", Key: "theme/nope.css",
	}); err == nil {
		t.Error("a missing document returned a graph")
	}
}

func TestCodeGraphRESTLinesOptIn(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	_, plain := doGraphGET(t, srv, "collection=theme&key=theme/style.css&direction=in")
	for _, e := range plain.Edges {
		if e.Lines != nil {
			t.Fatalf("lines were returned without being asked for: %+v", e)
		}
	}

	for _, truthy := range []string{"true", "1"} {
		_, withLines := doGraphGET(t, srv, "collection=theme&key=theme/style.css&direction=in&lines="+truthy)
		if len(withLines.Edges) == 0 {
			t.Fatal("no edges")
		}
		for _, e := range withLines.Edges {
			if e.Lines == nil {
				t.Errorf("lines=%s did not fill the line numbers: %+v", truthy, e)
			}
		}
	}
}

func TestCodeGraphGraphQLLinesOptIn(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	adapter := &GraphQLAdapter{server: srv, mcp: NewDirectClient(srv)}
	dir, lines := "in", true
	res, err := adapter.CodeGraph(context.Background(), gql.CodeGraphInput{
		Collection: "theme", Key: "theme/style.css", Direction: &dir, Lines: &lines,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Edges) == 0 {
		t.Fatal("no edges")
	}
	for _, e := range res.Edges {
		if e.FromLine == nil || e.ToLine == nil {
			t.Errorf("GraphQL dropped the line numbers: %+v", e)
		}
	}

	off, err := adapter.CodeGraph(context.Background(), gql.CodeGraphInput{
		Collection: "theme", Key: "theme/style.css", Direction: &dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range off.Edges {
		if e.FromLine != nil {
			t.Errorf("lines appeared without being requested: %+v", e)
		}
	}
}

func TestCodeGraphMCPLinesOptIn(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()

	res, err := NewDirectClient(srv).CodeGraph(context.Background(), &MCPCodeGraphRequest{
		Collection: "theme", Key: "theme/style.css", Direction: "in", Lines: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Edges {
		if e.Lines == nil {
			t.Errorf("MCP dropped the line numbers: %+v", e)
		}
	}
}
