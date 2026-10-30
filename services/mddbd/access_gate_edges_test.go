package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	proto "mddb/proto"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SEC-019: the edges of the gate — auth off, no credentials, unreadable
// bodies, streams, resources, role targets and argument shapes.

func TestGateChangesNothingWithAuthOff(t *testing.T) {
	for name, s := range map[string]*Server{
		"no manager":       {},
		"disabled manager": {AuthManager: &AuthManager{}},
	} {
		reached := false
		h := s.httpGate("/v1/never-classified", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/never-classified", nil))
		if !reached {
			t.Errorf("%s: the gate refused a request with auth off", name)
		}
		if err := s.authorize(context.Background(), accessRule{}, nil); err != nil {
			t.Errorf("%s: authorize = %v", name, err)
		}
	}
}

func TestGateRefusesUnclassifiedAndAnonymous(t *testing.T) {
	s := accessServer(t)
	s.AuthManager.publicEndpoints = map[string]bool{"/v1/health": true}
	serve := func(pattern string, who *JWTClaims) (int, bool) {
		reached := false
		h := s.httpGate(pattern, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
		if who != nil {
			h = asUser(who, h)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pattern, nil))
		return rec.Code, reached
	}
	if code, reached := serve("/v1/never-classified", root); reached || code != http.StatusForbidden {
		t.Errorf("an unclassified route answered %d, reached=%v; want 403", code, reached)
	}
	if code, _ := serve("/v1/system/info", nil); code != http.StatusUnauthorized {
		t.Errorf("an anonymous request answered %d, want 401", code)
	}
	if code, _ := serve("/v1/search", nil); code != http.StatusUnauthorized {
		t.Errorf("an anonymous collection request answered %d, want 401", code)
	}
	if _, reached := serve("/v1/health", nil); !reached {
		t.Error("a public endpoint did not reach its handler")
	}
}

// brokenBody fails every read.
type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("broken") }

func TestRequestCollectionsReadsQueryAndBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/cross-search?collection=a&collections=b",
		strings.NewReader(`{"collection":"c","sourceCollection":"d","collections":["e"],"targetCollections":["f","g"]}`))
	names, err := requestCollections(req)
	if err != nil || strings.Join(names, ",") != "a,b,c,d,e,f,g" {
		t.Fatalf("names = %v, %v", names, err)
	}
	if rest, _ := io.ReadAll(req.Body); !strings.Contains(string(rest), `"targetCollections"`) {
		t.Errorf("the body was not left for the handler: %q", rest)
	}

	form := httptest.NewRequest(http.MethodPost, "/v1/x", strings.NewReader(`{"collection":"c"}`))
	form.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if names, _ := requestCollections(form); len(names) != 0 {
		t.Errorf("a non-JSON body was read: %v", names)
	}

	broken := httptest.NewRequest(http.MethodPost, "/v1/x", brokenBody{})
	if _, err := requestCollections(broken); err == nil {
		t.Error("an unreadable body was accepted")
	}
}

func TestGateAnswersAnUnreadableBody(t *testing.T) {
	s := accessServer(t)
	for _, tc := range []struct {
		body io.Reader
		want int
	}{
		{brokenBody{}, http.StatusBadRequest},
		{strings.NewReader(`{"collection":"docs","pad":"` + strings.Repeat("x", 64) + `"}`), http.StatusRequestEntityTooLarge},
	} {
		h := withMaxBody(16, nil, asUser(alice, s.httpGate("/v1/search", http.NotFoundHandler())))
		req := httptest.NewRequest(http.MethodPost, "/v1/search", tc.body)
		req.ContentLength = -1
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("status %d, want %d", rec.Code, tc.want)
		}
	}
}

func TestRoleTargetsAreExpanded(t *testing.T) {
	s := accessServer(t)
	s.CollectionManager = NewCollectionManager(s.DB)
	if err := s.CollectionManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	if err := s.CollectionManager.Set("refs", &CollectionConfig{Code: &CodeConfigDef{Role: CodeRoleReference}}); err != nil {
		t.Fatal(err)
	}
	got := s.expandCollections([]string{"", "docs", rolePrefix + CodeRoleReference})
	if strings.Join(got, ",") != "docs,refs" {
		t.Fatalf("expanded to %v", got)
	}
	ctx := context.WithValue(context.Background(), authContextKey, alice)
	if err := s.authorizeTool(ctx, "reference_search", map[string]any{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("alice searched every reference collection: %v", err)
	}
	if err := s.authorize(ctx, ruleRead, []string{"docs", "refs"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("one forbidden collection among several was let through: %v", err)
	}
}

// fakeStream is a server stream that receives one request message, on
// behalf of who (anonymous when nil).
type fakeStream struct {
	grpc.ServerStream
	who  *JWTClaims
	coll string
	err  error
}

// Context is the stream's context: who's claims, or none when anonymous.
func (f *fakeStream) Context() context.Context {
	if f.who == nil {
		return context.Background()
	}
	return context.WithValue(context.Background(), authContextKey, f.who)
}
func (f *fakeStream) RecvMsg(m any) error {
	if f.err != nil {
		return f.err
	}
	m.(*proto.ExportRequest).Collection = f.coll
	return nil
}

func TestStreamInterceptorGatesEveryMessage(t *testing.T) {
	s := accessServer(t)
	icpt := s.accessStreamInterceptor()
	recv := func(_ any, ss grpc.ServerStream) error { return ss.RecvMsg(&proto.ExportRequest{}) }
	export := &grpc.StreamServerInfo{FullMethod: "/mddb.MDDB/Export"}
	if err := icpt(nil, &fakeStream{who: alice, coll: "secret"}, export, recv); status.Code(err) != codes.PermissionDenied {
		t.Errorf("export of another collection: %v", err)
	}
	if err := icpt(nil, &fakeStream{who: alice, coll: "docs"}, export, recv); err != nil {
		t.Errorf("export of her own collection: %v", err)
	}
	broken := errors.New("gone")
	if err := icpt(nil, &fakeStream{who: alice, err: broken}, export, recv); !errors.Is(err, broken) {
		t.Errorf("a receive error was replaced: %v", err)
	}
	snapshot := &grpc.StreamServerInfo{FullMethod: "/mddb.MDDBReplication/RequestSnapshot"}
	called := func(any, grpc.ServerStream) error { return nil }
	if err := icpt(nil, &fakeStream{who: alice}, snapshot, called); err != nil {
		t.Errorf("replication authorizes itself, the gate refused: %v", err)
	}
	unknown := &grpc.StreamServerInfo{FullMethod: "/mddb.MDDB/Unknown"}
	if err := icpt(nil, &fakeStream{who: alice}, unknown, called); status.Code(err) != codes.PermissionDenied {
		t.Errorf("an unclassified stream ran: %v", err)
	}
	anonymous := &fakeStream{coll: "docs"}
	if err := icpt(nil, anonymous, export, recv); status.Code(err) != codes.Unauthenticated {
		t.Errorf("an anonymous stream: %v", err)
	}
}

func TestUnaryInterceptorAndMessageCollections(t *testing.T) {
	s := accessServer(t)
	ctx := context.WithValue(context.Background(), authContextKey, alice)
	ran := false
	handler := func(context.Context, any) (any, error) { ran = true; return nil, nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/mddb.MDDB/CrossSearch"}
	req := &proto.CrossSearchRequest{SourceCollection: "docs", TargetCollections: []string{"docs", "secret"}}
	if _, err := s.accessUnaryInterceptor()(ctx, req, info, handler); status.Code(err) != codes.PermissionDenied || ran {
		t.Errorf("a cross search reaching another collection: %v, ran=%v", err, ran)
	}
	req.TargetCollections = []string{"docs"}
	if _, err := s.accessUnaryInterceptor()(ctx, req, info, handler); err != nil || !ran {
		t.Errorf("a cross search of her own collection: %v, ran=%v", err, ran)
	}
	nested := &proto.CreateCurationRuleRequest{Rule: &proto.CurationRuleProto{Collection: "secret"}}
	if got := messageCollections(nested); len(got) != 1 || got[0] != "secret" {
		t.Errorf("nested collection: %v", got)
	}
	if got := messageCollections("not a message"); got != nil {
		t.Errorf("a non-message named %v", got)
	}
}

func TestMCPGateEdges(t *testing.T) {
	s := accessServer(t)
	ctx := context.WithValue(context.Background(), authContextKey, alice)
	if err := s.authorizeTool(ctx, "my_custom_search", map[string]any{"collection": "secret"}); err != nil {
		t.Errorf("a custom tool is gated as its action, not by name: %v", err)
	}
	if err := s.authorizeTool(context.Background(), "add_document", map[string]any{"collection": "secret"}); err != nil {
		t.Errorf("a call on an MCP API key carries no claims and keeps its toolset scope: %v", err)
	}
	if got := collectionsFromArgs(map[string]any{"collection": "a", "target_collections": []any{"b", 3}, "collections": []string{"c"}}); strings.Join(got, ",") != "a,c,b" {
		t.Errorf("collectionsFromArgs = %v", got)
	}
	for uri, allowed := range map[string]bool{
		"mddb://health":          true,
		"mddb://stats":           false,
		"mddb://docs/key":        true,
		"mddb://secret/key":      false,
		"mddb-search://docs":     true,
		"mddb-search://secret":   false,
		"elsewhere://secret/key": true, // refused by the reader, not the gate
	} {
		u, _ := url.Parse(uri)
		if err := s.authorizeResource(ctx, u); (err == nil) != allowed {
			t.Errorf("%s: %v, want allowed=%v", uri, err, allowed)
		}
	}
	ts := &MCPToolServer{client: NewDirectClient(s)}
	if _, err := ts.readResource(ctx, "mddb://secret/key"); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("a resource of another collection was read: %v", err)
	}
}

func TestGraphQLGateEdges(t *testing.T) {
	s := accessServer(t)
	next := func(context.Context) (any, error) { return "ok", nil }
	if v, err := s.accessFieldMiddleware(context.Background(), next); v != "ok" || err != nil {
		t.Errorf("no field context: %v %v", v, err)
	}
	ctx := context.WithValue(context.Background(), authContextKey, alice)
	for _, fc := range []*graphql.FieldContext{
		{Object: "Document", Field: graphql.CollectedField{Field: &ast.Field{Name: "content"}}},
		{Object: "Query", Field: graphql.CollectedField{Field: &ast.Field{Name: "__schema"}}},
	} {
		if _, err := s.accessFieldMiddleware(graphql.WithFieldContext(ctx, fc), next); err != nil {
			t.Errorf("%s.%s was gated: %v", fc.Object, fc.Field.Name, err)
		}
	}
	secret, empty := "secret", (*string)(nil)
	type input struct {
		Collection        string
		SourceCollection  *string
		TargetCollections []string
		Other             string
	}
	for name, tc := range map[string]struct {
		arg  string
		v    any
		want string
	}{
		"string":          {"collection", "a", "a"},
		"other string":    {"query", "a", ""},
		"pointer":         {"collection", &secret, "secret"},
		"nil pointer":     {"collection", empty, ""},
		"other pointer":   {"key", &secret, ""},
		"list":            {"targetCollections", []string{"a", "b"}, "a,b"},
		"other list":      {"members", []string{"a"}, ""},
		"input":           {"input", input{Collection: "a", SourceCollection: &secret, TargetCollections: []string{"b"}}, "a,secret,b"},
		"input pointer":   {"input", &input{Collection: "a"}, "a"},
		"not an input":    {"limit", 5, ""},
		"nil input value": {"input", nil, ""},
	} {
		if got := strings.Join(argCollections(tc.arg, tc.v), ","); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

func TestJobRoutesCheckTheJobsCollection(t *testing.T) {
	s := accessServer(t)
	s.BulkIngest = NewBulkIngestManager(s, 8)
	oneDoc := []*proto.BatchDocument{{Key: "k", Lang: "en", ContentMd: "x"}}
	mine, err := s.BulkIngest.Submit("docs", oneDoc, "")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := s.BulkIngest.Submit("secret", oneDoc, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		who          *JWTClaims
		forbidden    bool
	}{
		{http.MethodGet, "/v1/bulk-ingest-job/" + mine.ID, alice, false},
		{http.MethodGet, "/v1/bulk-ingest-job/" + theirs.ID, alice, true},
		{http.MethodDelete, "/v1/bulk-ingest-job/" + theirs.ID, alice, true},
		{http.MethodDelete, "/v1/bulk-ingest-job/" + mine.ID, alice, false},
		{http.MethodDelete, "/v1/bulk-ingest-job/" + theirs.ID, root, false},
	} {
		code := restCall(t, s, tc.who, tc.method, tc.path, "")
		if (code == http.StatusForbidden) != tc.forbidden {
			t.Errorf("%s %s as %s: %d", tc.method, tc.path, tc.who.Username, code)
		}
	}
	if code := restCall(t, s, alice, http.MethodPut, "/v1/bulk-ingest-job/"+mine.ID, ""); code != http.StatusMethodNotAllowed {
		t.Errorf("PUT a job: %d", code)
	}
	if code := restCall(t, s, alice, http.MethodGet, "/v1/bulk-ingest-job/missing", ""); code != http.StatusNotFound {
		t.Errorf("a missing job: %d", code)
	}

	job, err := s.reindexJobs.start("secret", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		who  *JWTClaims
		want int
	}{
		{"/v1/vector-reindex/jobs", alice, http.StatusForbidden},
		{"/v1/vector-reindex/jobs", root, http.StatusOK},
		{"/v1/vector-reindex/jobs/" + job.ID, alice, http.StatusForbidden},
		{"/v1/vector-reindex/jobs/" + job.ID, root, http.StatusOK},
	} {
		if code := restCall(t, s, tc.who, http.MethodGet, tc.path, ""); code != tc.want {
			t.Errorf("GET %s as %s: %d, want %d", tc.path, tc.who.Username, code, tc.want)
		}
	}
}

func TestTenantsAreConfinedOnEverySurface(t *testing.T) {
	s := accessServer(t)
	// nobby is tenant t2: his own collection is fine, t1's is not.
	if code := restCall(t, s, nobby, http.MethodPost, "/v1/search", `{"collection":"t2/notes"}`); code == http.StatusForbidden {
		t.Errorf("t2 on its own collection: %d", code)
	}
	if code := restCall(t, s, nobby, http.MethodPost, "/v1/search", `{"collection":"t1/notes"}`); code != http.StatusForbidden {
		t.Errorf("t2 on t1's collection: %d", code)
	}
	// A listing filtered by tenant is open to it; one that is not, is not.
	if code := restCall(t, s, nobby, http.MethodGet, "/v1/stats", ""); code == http.StatusForbidden {
		t.Errorf("t2 on the tenant-filtered stats: %d", code)
	}
	if code := restCall(t, s, nobby, http.MethodGet, "/v1/geo-stats", ""); code != http.StatusForbidden {
		t.Errorf("t2 on the unfiltered geo stats: %d", code)
	}
}
