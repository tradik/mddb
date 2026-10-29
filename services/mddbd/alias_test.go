package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	gql "mddb/graphql"
	json "mddb/internal/jsonx"
	proto "mddb/proto"

	"github.com/99designs/gqlgen/graphql"
	"google.golang.org/grpc"
)

func aliasServer(t *testing.T) (*Server, func()) {
	s, cleanup := newHandlerTestServer(t)
	s.Aliases = NewAliasManager(s.DB)
	if err := s.Aliases.LoadAll(); err != nil {
		t.Fatal(err)
	}
	addTestDoc(t, s, "docs-v1", "a", "en", "release notes version one", map[string][]string{"v": {"1"}})
	addTestDoc(t, s, "docs-v2", "a", "en", "release notes version two", map[string][]string{"v": {"2"}})
	addTestDoc(t, s, "docs-v2", "b", "en", "more release notes", map[string][]string{"v": {"2"}})
	return s, cleanup
}

// REST through the middleware the server installs.
func aliasREST(s *Server, method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/search", s.handleSearch)
	mux.HandleFunc("/v1/fts", s.handleFTS)
	mux.HandleFunc("/v1/aliases", s.handleAliases)
	mux.HandleFunc("/v1/aliases/", s.handleAliases)
	mux.HandleFunc("/v1/eval-queries", s.handleEvalQueries)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	s.withAliases(mux).ServeHTTP(rec, req)
	return rec
}

func TestAlias_APIAndValidation(t *testing.T) {
	s, cleanup := aliasServer(t)
	defer cleanup()
	if rec := aliasREST(s, http.MethodPut, "/v1/aliases/docs", `{"collection":"docs-v1"}`); rec.Code != 200 {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	for body, why := range map[string]string{
		`{"collection":""}`:     "no collection",
		`{"collection":"docs"}`: "alias to an alias",
		`{`:                     "bad json",
	} {
		if rec := aliasREST(s, http.MethodPut, "/v1/aliases/other", body); rec.Code != 400 {
			t.Fatalf("%s: %d", why, rec.Code)
		}
	}
	if rec := aliasREST(s, http.MethodPut, "/v1/aliases/docs-v2", `{"collection":"docs-v1"}`); rec.Code != 400 ||
		!strings.Contains(rec.Body.String(), "is a collection") {
		t.Fatalf("alias over a collection: %d %s", rec.Code, rec.Body.String())
	}
	if s.SetAlias("a|b", "docs-v1") == nil {
		t.Fatal("bad name through set")
	}
	for _, bad := range []string{"", "a|b", "a b", "*", "role:x", strings.Repeat("x", 129)} {
		if validAliasName(bad) == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	rec := aliasREST(s, http.MethodGet, "/v1/aliases", "")
	if !strings.Contains(rec.Body.String(), `"alias":"docs","collection":"docs-v1"`) {
		t.Fatalf("list: %s", rec.Body.String())
	}
	// Survives a reload (a restart, a restore, a replicated write).
	fresh := NewAliasManager(s.DB)
	if err := fresh.LoadAll(); err != nil || fresh.Resolve("docs") != "docs-v1" {
		t.Fatalf("reload: %v", err)
	}
	if rec := aliasREST(s, http.MethodPost, "/v1/aliases", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatal("method")
	}
	if rec := aliasREST(s, http.MethodDelete, "/v1/aliases/docs", ""); rec.Code != 200 {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := aliasREST(s, http.MethodDelete, "/v1/aliases/docs", ""); rec.Code != 404 {
		t.Fatalf("delete twice: %d", rec.Code)
	}
	if s.Aliases.remove("nope") == nil {
		t.Fatal("remove unknown")
	}
	var nilMgr *AliasManager
	if nilMgr.Resolve("x") != "x" || nilMgr.active() {
		t.Fatal("nil manager")
	}
	none := &Server{}
	if none.SetAlias("a", "b") == nil || none.DeleteAlias("a") == nil {
		t.Fatal("no manager")
	}
}

// An alias works on every surface: REST (query and body), MCP, gRPC,
// GraphQL, and the multipart upload form.
func TestAlias_EverySurface(t *testing.T) {
	s, cleanup := aliasServer(t)
	defer cleanup()
	if err := s.SetAlias("docs", "docs-v2"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	rec := aliasREST(s, http.MethodPost, "/v1/search", `{"collection":"docs","limit":10}`)
	if !strings.Contains(rec.Body.String(), "version two") || strings.Contains(rec.Body.String(), "version one") {
		t.Fatalf("REST body: %s", rec.Body.String())
	}
	rec = aliasREST(s, http.MethodGet, "/v1/eval-queries?collection=docs", "")
	if !strings.Contains(rec.Body.String(), `"collection":"docs-v2"`) {
		t.Fatalf("REST query: %s", rec.Body.String())
	}
	// A body that is not a JSON object reaches the handler unchanged.
	if rec = aliasREST(s, http.MethodPost, "/v1/search", `[1]`); rec.Code != 400 {
		t.Fatalf("non-object body: %d", rec.Code)
	}

	tools := &MCPToolServer{client: NewDirectClient(s)}
	out, err := tools.mcpCallTool(ctx, "search_documents", map[string]interface{}{"collection": "docs", "include_content": true})
	if err != nil || !strings.Contains(out, "version two") {
		t.Fatalf("MCP: %v %s", err, out)
	}
	args := map[string]interface{}{"target_collections": []interface{}{"docs", "x"}, "source_collection": "docs"}
	if !s.Aliases.resolveAliasArgs(args) || args["target_collections"].([]interface{})[0] != "docs-v2" || args["source_collection"] != "docs-v2" {
		t.Fatalf("MCP lists: %+v", args)
	}

	g := &GRPCServer{server: s}
	req := &proto.SearchRequest{Collection: "docs"}
	resp, err := s.aliasUnaryInterceptor()(ctx, req, &grpc.UnaryServerInfo{}, func(ctx context.Context, r any) (any, error) {
		return g.Search(ctx, r.(*proto.SearchRequest))
	})
	if err != nil || resp.(*proto.SearchResponse).Total != 2 {
		t.Fatalf("gRPC: %v %+v", err, resp)
	}
	cross := &proto.CrossSearchRequest{TargetCollections: []string{"docs", "other"}, SourceCollection: "docs"}
	s.Aliases.resolveAliasMessage(cross)
	if cross.TargetCollections[0] != "docs-v2" || cross.SourceCollection != "docs-v2" || cross.TargetCollections[1] != "other" {
		t.Fatalf("gRPC lists: %+v", cross)
	}
	s.Aliases.resolveAliasMessage("not a message")

	// GraphQL: a plain argument and an input object.
	fc := &graphql.FieldContext{Args: map[string]any{"collection": "docs", "input": gql.FTSInput{Collection: "docs", Query: "x"},
		"ptr": &gql.FTSInput{Collection: "docs"}, "targetCollections": []string{"docs"}, "other": 3}}
	gctx := graphql.WithFieldContext(ctx, fc)
	if _, err := s.aliasFieldMiddleware(gctx, func(context.Context) (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if fc.Args["collection"] != "docs-v2" || fc.Args["input"].(gql.FTSInput).Collection != "docs-v2" ||
		fc.Args["ptr"].(*gql.FTSInput).Collection != "docs-v2" || fc.Args["targetCollections"].([]string)[0] != "docs-v2" {
		t.Fatalf("GraphQL: %+v", fc.Args)
	}
	type withPtr struct {
		Collection        *string
		TargetCollections []string
	}
	name := "docs"
	if got := s.Aliases.resolveAliasValue("in", withPtr{Collection: &name, TargetCollections: []string{"docs"}}).(withPtr); *got.Collection != "docs-v2" || got.TargetCollections[0] != "docs-v2" {
		t.Fatalf("pointer fields: %+v", got)
	}
	if s.Aliases.resolveAliasValue("query", "docs") != "docs" || s.Aliases.resolveAliasValue("tags", []string{"docs"}).([]string)[0] != "docs" {
		t.Fatal("only collection arguments are resolved")
	}

	// gRPC streams resolve each received message.
	stream := aliasStream{ServerStream: &fakeRecvStream{}, aliases: s.Aliases}
	m := &proto.ExportRequest{}
	if err := stream.RecvMsg(m); err != nil || m.Collection != "docs-v2" {
		t.Fatalf("stream: %v %+v", err, m)
	}
	if err := s.aliasStreamInterceptor()(nil, &fakeRecvStream{}, &grpc.StreamServerInfo{}, func(_ any, ss grpc.ServerStream) error {
		return ss.RecvMsg(&proto.ExportRequest{})
	}); err != nil {
		t.Fatal(err)
	}
	if err := (aliasStream{ServerStream: &fakeRecvStream{fail: true}, aliases: s.Aliases}).RecvMsg(m); err == nil {
		t.Fatal("receive error passes through")
	}
}

type fakeRecvStream struct {
	grpc.ServerStream
	fail bool
}

func (f *fakeRecvStream) RecvMsg(m any) error {
	if f.fail {
		return context.Canceled
	}
	m.(*proto.ExportRequest).Collection = "docs"
	return nil
}

// A flip is atomic: a search through the alias during flips never fails and
// never returns documents of both collections.
func TestAlias_FlipIsAtomic(t *testing.T) {
	s, cleanup := aliasServer(t)
	defer cleanup()
	if err := s.SetAlias("docs", "docs-v1"); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			target := []string{"docs-v1", "docs-v2"}[i%2]
			if err := s.SetAlias("docs", target); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for i := 0; i < 300; i++ {
		rec := aliasREST(s, http.MethodPost, "/v1/search", `{"collection":"docs","limit":10}`)
		var docs []struct {
			Meta map[string][]string `json:"meta"`
		}
		if rec.Code != 200 || json.Unmarshal(bytes.TrimSpace(rec.Body.Bytes()), &docs) != nil || len(docs) == 0 {
			t.Fatalf("search %d: %d %s", i, rec.Code, rec.Body.String())
		}
		for _, d := range docs[1:] {
			if d.Meta["v"][0] != docs[0].Meta["v"][0] {
				t.Fatalf("search %d mixed two collections", i)
			}
		}
	}
	stop.Store(true)
	wg.Wait()
}
