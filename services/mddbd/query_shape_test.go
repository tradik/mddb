package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	gql "mddb/graphql"
	"mddb/internal/metrics"
	pb "mddb/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

// OPS-025: each transport tags its requests, and each search and graph path
// counts what it ran under that tag.

func TestShapeSurfaceFollowsTheTransport(t *testing.T) {
	for path, want := range map[string]metrics.Surface{
		"/v1/fts": metrics.SurfaceREST, "/graphql": metrics.SurfaceGraphQL, "/es": metrics.SurfaceES,
		"/es/docs/_search": metrics.SurfaceES, "/esx": metrics.SurfaceREST,
	} {
		var got shapeScope
		h := withShapeSurface(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = shapeScopeOf(r.Context()) }))
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got.surface != want || got.client == 0 {
			t.Errorf("%s: %+v, want surface %v and a client", path, got, want)
		}
	}

	// gRPC: the interceptor tags the call, its peer is the client.
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 5}})
	var got shapeScope
	_, _ = shapeUnaryInterceptor(ctx, nil, &grpc.UnaryServerInfo{}, func(ctx context.Context, _ any) (any, error) {
		got = shapeScopeOf(ctx)
		return nil, nil
	})
	if got.surface != metrics.SurfaceGRPC || got.client != clientHash("ip", "192.0.2.1") {
		t.Errorf("grpc scope %+v", got)
	}

	// MCP: the session, else the key, else the address, else stdio.
	for _, c := range []struct {
		caller mcpCaller
		want   uint64
	}{
		{mcpCaller{sessionID: "s", keyName: "k", clientIP: "i"}, clientHash("session", "s")},
		{mcpCaller{keyName: "k", clientIP: "i"}, clientHash("key", "k")},
		{mcpCaller{clientIP: "i"}, clientHash("ip", "i")},
		{mcpCaller{}, clientHash("stdio", "stdio")},
	} {
		ctx := context.WithValue(context.Background(), mcpCallerContextKey{}, c.caller)
		if sc := shapeScopeOf(withMCPSurface(ctx)); sc.surface != metrics.SurfaceMCP || sc.client != c.want {
			t.Errorf("mcp %+v: %+v", c.caller, sc)
		}
	}

	// An authenticated user is the client, whatever the transport saw.
	user := context.WithValue(withShapeScope(context.Background(), shapeScope{client: 9}), authContextKey, &JWTClaims{Username: "u"})
	if shapeClient(user) != clientHash("user", "u") || shapeClient(withShapeScope(context.Background(), shapeScope{client: 9})) != 9 {
		t.Error("shapeClient does not prefer the user")
	}
	if clientHash("ip", "") != 0 {
		t.Error("an unknown client hashed to something")
	}
}

func TestFilterShapeNamesOperatorsNotValues(t *testing.T) {
	v := "x"
	yes := true
	ops, conds := filterShape(map[string][]string{"a": {"1"}, "b": {"2"}}, MetaFilter{
		"b": {Prefix: "p", Contains: "c", Gt: &v, Gte: &v, Lt: &v, Lte: &v, Not: []string{"n"}, Exists: &yes},
		"c": {In: []string{"i"}},
	}, 2)
	// a (filterMeta only), b (both), c, plus two ranges.
	if conds != 5 {
		t.Errorf("conditions %d", conds)
	}
	for op := metrics.FilterIn; op <= metrics.FilterRange; op++ {
		if ops&(1<<op) == 0 {
			t.Errorf("operator %d not counted", op)
		}
	}
	if ops, conds := filterShape(nil, nil, 0); ops != 0 || conds != 0 {
		t.Errorf("no filter: %v %d", ops, conds)
	}
}

func TestVectorAndGraphShapes(t *testing.T) {
	sh := vectorShape(metrics.ModeVector, "c", AlgorithmAuto, "hnsw", nil, nil,
		&SearchPlan{Decomposition: &QueryDecomposition{Parts: 3}}, 0, LimitReport{LimitCapped: true})
	if !sh.Auto || sh.Algorithm != "hnsw" || sh.DecomposedParts != 3 || !sh.LimitCapped {
		t.Errorf("vector shape %+v", sh)
	}
	if sh := vectorShape(metrics.ModeHybrid, "c", "flat", "flat", nil, nil, nil, 1, LimitReport{}); sh.Auto || sh.DecomposedParts != 0 {
		t.Errorf("plain shape %+v", sh)
	}
	g := codeGraphShape("c", "", GraphMaxDepth, GraphMaxDegree, nil)
	if !g.NoStart || !g.WholeGraph || g.Reached != nil {
		t.Errorf("map-shaped request %+v", g)
	}
	res := &GraphResult{Truncated: true, Nodes: []GraphNode{{Key: "a"}, {Key: "b"}}}
	g = codeGraphShape("c", "a", 1, 10, res)
	var seen []string
	for k := range g.Reached {
		seen = append(seen, k)
		break // the walk stops when the consumer does
	}
	if g.WholeGraph || !g.Truncated || len(seen) != 1 {
		t.Errorf("graph shape %+v, reached %v", g, seen)
	}
	// An unset fan-out is the default, which is the maximum.
	if g := codeGraphShape("c", "a", GraphMaxDepth, 0, nil); !g.WholeGraph {
		t.Errorf("depth 3 at the default fan-out: %+v", g)
	}
}

func TestCodeGraphChainsAcrossSurfaces(t *testing.T) {
	srv, cleanup := themeFixture(t)
	defer cleanup()
	srv.Metrics = metrics.NewMetrics(false, nil)
	client := withShapeScope(context.Background(), shapeScope{client: 42})

	// REST from style.css, then MCP and GraphQL each from a node the previous
	// answer reached: one chain of three, whichever surface each call took.
	rec := httptest.NewRecorder()
	srv.handleCodeGraph(rec, httptest.NewRequest(http.MethodGet, "/v1/code-graph?collection=theme&key=theme/style.css&direction=in", nil).WithContext(client))
	if rec.Code != http.StatusOK {
		t.Fatalf("REST %d %s", rec.Code, rec.Body.String())
	}
	mcp := NewDirectClient(srv)
	res, err := mcp.CodeGraph(withShapeScope(context.Background(), shapeScope{surface: metrics.SurfaceMCP, client: 42}),
		&MCPCodeGraphRequest{Collection: "theme", Key: "theme/index.html"})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &GraphQLAdapter{server: srv, mcp: mcp}
	depth := 9
	if _, err := adapter.CodeGraph(withShapeScope(context.Background(), shapeScope{surface: metrics.SurfaceGraphQL, client: 42}),
		gql.CodeGraphInput{Collection: "theme", Key: res.Nodes[len(res.Nodes)-1].Key, Depth: &depth}); err != nil {
		t.Fatal(err)
	}
	// No start document: refused, and counted as a request for the map.
	rec = httptest.NewRecorder()
	srv.handleCodeGraph(rec, httptest.NewRequest(http.MethodGet, "/v1/code-graph?collection=theme", nil).WithContext(client))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no key: %d", rec.Code)
	}

	r := srv.Metrics.QueryShape().Report(nil)
	g := r.Graph
	if g.Requests["rest"]["code_graph"] != 2 || g.Requests["mcp"]["code_graph"] != 1 || g.Requests["graphql"]["code_graph"] != 1 ||
		g.DepthClamped["graphql"]["code_graph"] != 1 || g.NoStart["rest"] != 1 {
		t.Fatalf("graph %+v", g)
	}
	// The refused request ended the chain of three, and opened its own.
	if g.Chains.Sessions != 1 || g.Chains.Length["3"] != 1 || g.Chains.AtLeast3 != 1 || g.Chains.Open != 1 {
		t.Errorf("chains %+v", g.Chains)
	}
}

func TestSearchesAreCountedOnEverySurface(t *testing.T) {
	s := corpusServer(t, 256)
	s.Metrics = metrics.NewMetrics(false, nil)
	seedCorpus(t, s)
	ctx := context.Background()
	grpcCtx := withShapeScope(ctx, shapeScope{surface: metrics.SurfaceGRPC})
	g := &GRPCServer{server: s}
	if _, err := g.FTS(grpcCtx, &pb.FTSRequest{Collection: "docs", Query: "follower"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Search(grpcCtx, &pb.SearchRequest{Collection: "docs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.VectorSearch(grpcCtx, &pb.VectorSearchRequest{Collection: "docs", Query: "follower replica copies and backups written to tarballs", Algorithm: "auto",
		QueryDecomposition: QueryDecompositionAuto}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.HybridSearch(grpcCtx, &pb.HybridSearchRequest{Collection: "docs", Query: "certificate"}); err != nil {
		t.Fatal(err)
	}
	mcp := NewDirectClient(s)
	mctx := withShapeScope(ctx, shapeScope{surface: metrics.SurfaceMCP})
	if _, err := mcp.Search(mctx, &MCPSearchRequest{Collection: "docs"}); err != nil {
		t.Fatal(err)
	}
	// A filter that matches nothing is an empty search.
	if _, err := mcp.VectorSearch(mctx, &MCPVectorSearchRequest{Collection: "docs", Query: "x",
		FilterMeta: map[string][]string{"geo_lat": {"0"}}}); err != nil {
		t.Fatal(err)
	}
	rec := doRequest(t, s.handleVectorSearch, map[string]any{"collection": "docs", "query": "x", "filterMeta": map[string][]string{"geo_lat": {"0"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("rest vector %d", rec.Code)
	}
	// retrievalMode graph counts the expansion and its depth.
	rec = doRequest(t, s.handleVectorSearch, map[string]any{"collection": "docs", "query": "backup", "retrievalMode": "graph",
		"graphExpand": map[string]any{"graphDepth": 7}})
	if rec.Code != http.StatusOK {
		t.Fatalf("rest graph expansion %d %s", rec.Code, rec.Body.String())
	}

	r := s.Metrics.QueryShape().Report(nil)
	for _, c := range []struct {
		surface, mode string
		total         int64
	}{{"grpc", "fts", 1}, {"grpc", "meta", 1}, {"grpc", "vector", 1}, {"grpc", "hybrid", 1}, {"mcp", "meta", 1}, {"mcp", "vector", 1}, {"rest", "vector", 2}} {
		if got := r.Searches[c.surface][c.mode].Total; got != c.total {
			t.Errorf("%s %s = %d, want %d (%+v)", c.surface, c.mode, got, c.total, r.Searches)
		}
	}
	if r.Searches["mcp"]["vector"].Empty != 1 || r.Searches["rest"]["vector"].Empty != 1 {
		t.Errorf("empty searches %+v", r.Searches)
	}
	if r.Decomposition.Searches["grpc"] != 1 || r.AutoChoices["flat"] == 0 {
		t.Errorf("decomposition %+v auto %v", r.Decomposition, r.AutoChoices)
	}
	if r.Graph.Requests["rest"]["expand"] != 1 || r.Graph.DepthClamped["rest"]["expand"] != 1 {
		t.Errorf("expansion %+v", r.Graph)
	}
}
