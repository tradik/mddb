package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mddb/internal/fts"
	json "mddb/internal/jsonx"
	"mddb/internal/schema"
	proto "mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// SEC-019, end to end: a credential confined to one collection, or to one
// tenant, reaches nothing else on any surface — REST, gRPC, MCP or GraphQL.
// Each case goes through the real server stack, so a surface that forgets the
// gate fails here rather than in production.

const accessSecret = "sec019-test-secret-0123456789abcdef" // #nosec G101 -- a test-only JWT secret

// The principals every surface is tried with.
var (
	alice = &JWTClaims{Username: "alice"}               // read+write on "docs" only
	tina  = &JWTClaims{Username: "tina", Tenant: "t1"}  // tenant t1, "*" read+write
	root  = &JWTClaims{Username: "root", Admin: true}   // global administrator
	nobby = &JWTClaims{Username: "nobby", Tenant: "t2"} // tenant t2, "*" read+write
)

// accessServer is a server with auth on, the managers the tried routes use,
// and the four principals above.
func accessServer(t *testing.T) *Server {
	t.Helper()
	s, cleanup := newTestServerWithFTS(t)
	t.Cleanup(cleanup)
	s.RateLimiter = NewRateLimiter()
	s.SynonymManager = fts.NewSynonymManager(s.DB)
	s.StopWordManager = fts.NewStopWordManager(s.DB)
	s.SchemaManager = schema.NewSchemaManager(s.DB)
	for _, err := range []error{s.SynonymManager.EnsureBucket(), s.StopWordManager.EnsureBucket(), s.SchemaManager.EnsureBucket()} {
		if err != nil {
			t.Fatal(err)
		}
	}
	am := NewAuthManager(s.DB, AuthConfig{JWTSecret: accessSecret, JWTExpiry: time.Hour})
	if err := am.EnsureBuckets(); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ name, tenant string }{{"alice", ""}, {"tina", "t1"}, {"root", ""}, {"nobby", "t2"}} {
		if _, err := am.CreateTenantUser(u.name, "password-123", u.tenant); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []*Permission{
		{Username: "alice", Collection: "docs", Read: true, Write: true},
		{Username: "tina", Collection: "*", Read: true, Write: true},
		{Username: "nobby", Collection: "*", Read: true, Write: true},
		{Username: "root", Collection: "*", Read: true, Write: true, Admin: true},
	} {
		if err := am.SetPermission(p); err != nil {
			t.Fatal(err)
		}
	}
	s.AuthManager = am
	return s
}

// asUser serves a request as the given principal, the way the auth
// middleware leaves it for the routes.
func asUser(c *JWTClaims, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authContextKey, c)))
	})
}

// restCall sends one JSON request through the registered routes.
func restCall(t *testing.T, s *Server, c *JWTClaims, method, target, body string) int {
	t.Helper()
	mux := http.NewServeMux()
	s.registerRoutes(mux, true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	asUser(c, mux).ServeHTTP(rec, req)
	return rec.Code
}

// The routes the SEC-019 survey found without any check, tried with a
// collection the caller has no right to and with one it has.
func TestRESTGateDeniesOtherCollections(t *testing.T) {
	s := accessServer(t)
	cases := []struct {
		name, method, path, body string
	}{
		{"fts", http.MethodPost, "/v1/fts", `{"collection":"%s","query":"x"}`},
		{"add-batch", http.MethodPost, "/v1/add-batch", `{"collection":"%s","documents":[{"key":"k","lang":"en","contentMd":"x"}]}`},
		{"schema/get", http.MethodPost, "/v1/schema/get", `{"collection":"%s"}`},
		{"schema/set", http.MethodPost, "/v1/schema/set", `{"collection":"%s","schema":"{}"}`},
		{"synonyms", http.MethodGet, "/v1/synonyms?collection=%s", ``},
		{"stopwords add", http.MethodPost, "/v1/stopwords", `{"collection":"%s","words":["zz"]}`},
		{"autocomplete", http.MethodGet, "/v1/autocomplete?collection=%s&q=x", ``},
		{"validate", http.MethodPost, "/v1/validate", `{"collection":"%s","meta":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fill := func(c string) (string, string) {
				return strings.ReplaceAll(tc.path, "%s", c), strings.ReplaceAll(tc.body, "%s", c)
			}
			p, b := fill("secret")
			if code := restCall(t, s, alice, tc.method, p, b); code != http.StatusForbidden {
				t.Errorf("alice on another collection: %d, want 403", code)
			}
			p, b = fill("t2/notes")
			if code := restCall(t, s, tina, tc.method, p, b); code != http.StatusForbidden {
				t.Errorf("tenant t1 on t2's collection: %d, want 403", code)
			}
			p, b = fill("docs")
			if code := restCall(t, s, alice, tc.method, p, b); code == http.StatusForbidden || code == http.StatusUnauthorized {
				t.Errorf("alice on her own collection: %d", code)
			}
			p, b = fill("secret")
			if code := restCall(t, s, root, tc.method, p, b); code == http.StatusForbidden || code == http.StatusUnauthorized {
				t.Errorf("admin: %d", code)
			}
		})
	}
}

// grpcClient serves the MDDB service with the production interceptor chain.
func grpcClient(t *testing.T, s *Server) proto.MDDBClient {
	t.Helper()
	opts, _ := s.grpcServerOptions(true)
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(opts...)
	proto.RegisterMDDBServer(srv, NewGRPCServer(s))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return proto.NewMDDBClient(conn)
}

// grpcAs is an outgoing context carrying the principal's token.
func grpcAs(t *testing.T, c *JWTClaims) context.Context {
	t.Helper()
	tok, err := GenerateTenantJWT(c.Username, c.Tenant, c.Admin, accessSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+tok)
}

func TestGRPCGateDeniesOtherCollections(t *testing.T) {
	s := accessServer(t)
	client := grpcClient(t, s)
	calls := map[string]func(ctx context.Context, coll string) error{
		"ListSynonyms": func(ctx context.Context, coll string) error {
			_, err := client.ListSynonyms(ctx, &proto.ListSynonymsRequest{Collection: coll})
			return err
		},
		"AddSynonym": func(ctx context.Context, coll string) error {
			_, err := client.AddSynonym(ctx, &proto.AddSynonymRequest{Collection: coll, Term: "a", Synonyms: []string{"b"}})
			return err
		},
		"ListStopwords": func(ctx context.Context, coll string) error {
			_, err := client.ListStopwords(ctx, &proto.ListStopwordsRequest{Collection: coll})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(grpcAs(t, alice), "secret"); status.Code(err) != codes.PermissionDenied {
				t.Errorf("alice on another collection: %v, want PermissionDenied", err)
			}
			if err := call(grpcAs(t, tina), "t2/notes"); status.Code(err) != codes.PermissionDenied {
				t.Errorf("tenant t1 on t2's collection: %v, want PermissionDenied", err)
			}
			if err := call(grpcAs(t, alice), "docs"); err != nil {
				t.Errorf("alice on her own collection: %v", err)
			}
			if err := call(grpcAs(t, root), "secret"); err != nil {
				t.Errorf("admin: %v", err)
			}
		})
	}
}

// mcpCallAs runs one tool through the MCP handler as the principal.
func mcpCallAs(s *Server, c *JWTClaims, tool string, args map[string]any) map[string]any {
	h := NewMCPHandlerWithConfig(NewDirectClient(s), nil, MCPServerInfo{}, "", ModeRW, "")
	ctx := context.WithValue(context.Background(), authContextKey, c)
	return h.HandleContext(ctx, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
}

// mcpDenied reports whether a tools/call answer is the gate's refusal.
func mcpDenied(res map[string]any) bool {
	r, _ := res["result"].(map[string]any)
	if isErr, _ := r["isError"].(bool); !isErr {
		return false
	}
	b, _ := json.Marshal(r["content"])
	return strings.Contains(string(b), "forbidden")
}

func TestMCPGateDeniesOtherCollections(t *testing.T) {
	s := accessServer(t)
	tools := map[string]map[string]any{
		"add_document":  {"key": "k", "lang": "en", "content_md": "x"},
		"list_synonyms": {},
		"get_schema":    {},
	}
	for tool, args := range tools {
		t.Run(tool, func(t *testing.T) {
			with := func(coll string) map[string]any { return mcpArgsFor(coll, args) }
			if res := mcpCallAs(s, alice, tool, with("secret")); !mcpDenied(res) {
				t.Errorf("alice on another collection: %v", res)
			}
			if res := mcpCallAs(s, tina, tool, with("t2/notes")); !mcpDenied(res) {
				t.Errorf("tenant t1 on t2's collection: %v", res)
			}
			if res := mcpCallAs(s, alice, tool, with("docs")); mcpDenied(res) {
				t.Errorf("alice on her own collection: %v", res)
			}
			if res := mcpCallAs(s, root, tool, with("secret")); mcpDenied(res) {
				t.Errorf("admin: %v", res)
			}
		})
	}
}

// mcpArgsFor is args with the collection set to coll.
func mcpArgsFor(coll string, args map[string]any) map[string]any {
	out := map[string]any{"collection": coll}
	for k, v := range args {
		out[k] = v
	}
	return out
}

// gqlCall runs one GraphQL document as the principal.
func gqlCall(t *testing.T, s *Server, c *JWTClaims, query string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	asUser(c, s.newGraphQLHandler()).ServeHTTP(rec, req)
	return rec.Body.String()
}

// assertGQLCollectionGate checks a query naming a collection is refused on
// another collection and served on alice's own.
func assertGQLCollectionGate(t *testing.T, s *Server, q string) {
	t.Helper()
	if out := gqlCall(t, s, alice, strings.ReplaceAll(q, "%s", "secret")); !strings.Contains(out, "forbidden") {
		t.Errorf("alice on another collection: %s", out)
	}
	if out := gqlCall(t, s, alice, strings.ReplaceAll(q, "%s", "docs")); strings.Contains(out, "forbidden") {
		t.Errorf("alice on her own collection: %s", out)
	}
}

func TestGraphQLGateDeniesOtherCollections(t *testing.T) {
	s := accessServer(t)
	queries := map[string]string{
		"schema":      `{ schema(collection: "%s") { collection } }`,
		"vectorStats": `{ vectorStats { enabled } }`,
		"schemas":     `{ schemas { collection } }`,
	}
	for name, q := range queries {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(q, "%s") {
				assertGQLCollectionGate(t, s, q)
			}
			if out := gqlCall(t, s, tina, strings.ReplaceAll(q, "%s", "t2/notes")); !strings.Contains(out, "forbidden") {
				t.Errorf("tenant t1 across tenants: %s", out)
			}
			if out := gqlCall(t, s, root, strings.ReplaceAll(q, "%s", "secret")); strings.Contains(out, "forbidden") {
				t.Errorf("admin: %s", out)
			}
		})
	}
}
