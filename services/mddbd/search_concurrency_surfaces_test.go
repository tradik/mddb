package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	json "mddb/internal/jsonx"
	"mddb/internal/metrics"
	proto "mddb/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GO-053: one gate, every surface. The search concurrency limit used to sit
// on some REST routes only, so /v1/search, /v1/cross-search, gRPC, GraphQL
// and MCP walked around it. These tests fill the slots and require the same
// refusal from every surface, then free them and require that every surface
// gets through holding one slot — a second one would never come.

// limitedGold is the golden server with capacity search slots and a queue
// timeout of waitMs.
func limitedGold(t *testing.T, capacity int, waitMs string) (*Server, func()) {
	t.Helper()
	t.Setenv(esCompatEnv, "true") // the Elasticsearch-compatible routes are queries too
	s, cleanup := goldenServer(t)
	s.SearchLimiter = limiterWith(t, capacity, waitMs)
	// A stored evaluation set, so the surfaces that take no queries run one.
	if _, err := s.AddEvalQuery("gold", EvalQuery{Query: "pump water", ExpectedKeys: []string{"manual"}}); err != nil {
		t.Fatal(err)
	}
	return s, cleanup
}

// searchSurfaceCall is one heavy query on one surface. It reports whether the
// surface refused it as busy; a refusal in any other shape fails the test.
type searchSurfaceCall struct {
	name string
	run  func(t *testing.T, s *Server) (busy bool)
}

var evalQueries = []map[string]any{{"query": "pump water", "expectedKeys": []string{"manual"}}}

// restBusy posts body to path through the registered routes and requires a
// refusal to be 503 with Retry-After and the SEARCH_BUSY body.
func restBusy(path, body string) func(*testing.T, *Server) bool {
	return func(t *testing.T, s *Server) bool {
		t.Helper()
		mux := http.NewServeMux()
		s.registerRoutes(mux, false)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		if rec.Code != http.StatusServiceUnavailable {
			return false
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: 503 body is not JSON: %s", path, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") != "1" || out["code"] != "SEARCH_BUSY" || out["retryAfterSeconds"] != float64(1) {
			t.Fatalf("%s: 503 without the refusal: Retry-After %q, body %v", path, rec.Header().Get("Retry-After"), out)
		}
		return true
	}
}

// grpcBusy requires a refused call to be RESOURCE_EXHAUSTED.
func grpcBusy(call func(g *GRPCServer) error) func(*testing.T, *Server) bool {
	return func(t *testing.T, s *Server) bool {
		t.Helper()
		err := call(&GRPCServer{server: s})
		if !strings.Contains(status.Convert(err).Message(), "search concurrency limit") {
			return false
		}
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatalf("code = %v (%v), want ResourceExhausted", status.Code(err), err)
		}
		return true
	}
}

// mcpBusy requires a refused tool call to be an error carrying the refusal as
// structured content.
func mcpBusy(tool string, args map[string]any) func(*testing.T, *Server) bool {
	return func(t *testing.T, s *Server) bool {
		t.Helper()
		res := mcpCall(s, tool, args)
		detail, _ := res["structuredContent"].(map[string]any)
		if detail["code"] != "SEARCH_BUSY" {
			return false
		}
		if res["isError"] != true || detail["status"] != http.StatusServiceUnavailable || detail["retryAfterSeconds"] != 1 {
			t.Fatalf("%s: %v", tool, res)
		}
		return true
	}
}

// graphQLBusy requires a refused field to be an error whose extensions carry
// the refusal.
func graphQLBusy(query string) func(*testing.T, *Server) bool {
	return func(t *testing.T, s *Server) bool {
		t.Helper()
		errs, _ := graphQLPost(t, s, query)["errors"].([]any)
		if len(errs) == 0 {
			return false
		}
		ext, _ := errs[0].(map[string]any)["extensions"].(map[string]any)
		if ext["code"] != "SEARCH_BUSY" {
			return false
		}
		if ext["status"] != float64(http.StatusServiceUnavailable) || ext["retryAfterSeconds"] != float64(1) {
			t.Fatalf("extensions = %v", ext)
		}
		return true
	}
}

// searchSurfaceCalls is every heavy query on every surface.
func searchSurfaceCalls() []searchSurfaceCall {
	ctx := context.Background()
	q := map[string]any{"collection": "gold", "query": "pump water"}
	cross := map[string]any{"target_collections": []any{"gold"}, "query": "pump water"}
	evalBody, _ := json.Marshal(map[string]any{"collection": "gold", "queries": evalQueries})
	return []searchSurfaceCall{
		{"REST search", restBusy("/v1/search", `{"collection":"gold"}`)},
		{"REST fts", restBusy("/v1/fts", `{"collection":"gold","query":"pump water"}`)},
		{"REST vector-search", restBusy("/v1/vector-search", `{"collection":"gold","query":"pump water"}`)},
		{"REST hybrid-search", restBusy("/v1/hybrid-search", `{"collection":"gold","query":"pump water"}`)},
		{"REST cross-search", restBusy("/v1/cross-search", `{"targetCollections":["gold"],"query":"pump water"}`)},
		{"REST cross-search fts", restBusy("/v1/cross-search", `{"targetCollections":["gold"],"query":"pump water","searchType":"fts"}`)},
		{"REST aggregate", restBusy("/v1/aggregate", `{"collection":"gold"}`)},
		{"REST code-graph", restBusy("/v1/code-graph", `{"collection":"gold","key":"manual"}`)},
		{"REST symbol", restBusy("/v1/symbol", `{"collection":"gold","name":"pump"}`)},
		{"REST eval", restBusy("/v1/eval", string(evalBody))},
		{"REST find-duplicates", restBusy("/v1/find-duplicates", `{"collection":"gold"}`)},
		{"REST memory recall", restBusy("/v1/memory/recall", `{"query":"pump water"}`)},
		{"ES _search listing", restBusy("/es/gold/_search", `{"query":{"match_all":{}}}`)},
		{"ES _search text", restBusy("/es/gold/_search", `{"query":{"match":{"content":"pump water"}}}`)},
		{"ES _count", restBusy("/es/gold/_count", `{"query":{"match_all":{}}}`)},

		{"gRPC Search", grpcBusy(func(g *GRPCServer) error {
			_, err := g.Search(ctx, &proto.SearchRequest{Collection: "gold"})
			return err
		})},
		{"gRPC FTS", grpcBusy(func(g *GRPCServer) error {
			_, err := g.FTS(ctx, &proto.FTSRequest{Collection: "gold", Query: "pump water"})
			return err
		})},
		{"gRPC VectorSearch", grpcBusy(func(g *GRPCServer) error {
			_, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "gold", Query: "pump water"})
			return err
		})},
		{"gRPC HybridSearch", grpcBusy(func(g *GRPCServer) error {
			_, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "gold", Query: "pump water"})
			return err
		})},
		{"gRPC CrossSearch", grpcBusy(func(g *GRPCServer) error {
			_, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{TargetCollections: []string{"gold"}, Query: "pump water"})
			return err
		})},
		{"gRPC CrossSearch hybrid", grpcBusy(func(g *GRPCServer) error {
			_, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{TargetCollections: []string{"gold"}, Query: "pump water", SearchType: "hybrid"})
			return err
		})},
		{"gRPC CodeSymbol", grpcBusy(func(g *GRPCServer) error {
			_, err := g.CodeSymbol(ctx, &proto.CodeSymbolRequest{Collection: "gold", Name: "pump"})
			return err
		})},
		{"gRPC RunEval", grpcBusy(func(g *GRPCServer) error {
			_, err := g.RunEval(ctx, &proto.RunEvalRequest{Collection: "gold",
				Queries: []*proto.EvalQueryProto{{Query: "pump water", ExpectedKeys: []string{"manual"}}}})
			return err
		})},
		{"gRPC FindDuplicates", grpcBusy(func(g *GRPCServer) error {
			_, err := g.FindDuplicates(ctx, &proto.FindDuplicatesRequest{Collection: "gold"})
			return err
		})},

		{"MCP search_documents", mcpBusy("search_documents", map[string]any{"collection": "gold"})},
		{"MCP full_text_search", mcpBusy("full_text_search", q)},
		{"MCP semantic_search", mcpBusy("semantic_search", q)},
		{"MCP hybrid_search", mcpBusy("hybrid_search", q)},
		{"MCP cross_search", mcpBusy("cross_search", cross)},
		{"MCP code_symbol", mcpBusy("code_symbol", map[string]any{"collection": "gold", "name": "pump"})},
		{"MCP code_graph", mcpBusy("code_graph", map[string]any{"collection": "gold", "key": "manual"})},
		{"MCP aggregate", mcpBusy("aggregate", map[string]any{"collection": "gold"})},
		{"MCP run_eval", mcpBusy("run_eval", map[string]any{"collection": "gold"})},
		{"MCP find_duplicates", mcpBusy("find_duplicates", map[string]any{"collection": "gold"})},
		{"MCP memory_recall", mcpBusy("memory_recall", map[string]any{"query": "pump water"})},
		{"MCP memory_recall keyword", mcpBusy("memory_recall", map[string]any{"query": "pump water", "strategy": "keyword"})},

		{"GraphQL search", graphQLBusy(`{ search(input: {collection: "gold"}) { totalCount } }`)},
		{"GraphQL fts", graphQLBusy(`{ fts(input: {collection: "gold", query: "pump water"}) { total } }`)},
		{"GraphQL vectorSearch", graphQLBusy(`{ vectorSearch(input: {collection: "gold", query: "pump water"}) { total } }`)},
		{"GraphQL codeSymbol", graphQLBusy(`{ codeSymbol(input: {collection: "gold", name: "pump"}) { symbol } }`)},
		{"GraphQL codeGraph", graphQLBusy(`{ codeGraph(input: {collection: "gold", key: "manual"}) { collection } }`)},
	}
}

func TestSearchBusy_EverySurfaceRefusesTheSame(t *testing.T) {
	s, cleanup := limitedGold(t, 1, "5")
	defer cleanup()
	release, err := s.SearchLimiter.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	calls := searchSurfaceCalls()
	for _, c := range calls {
		if !c.run(t, s) {
			t.Errorf("%s: answered with every slot taken — it is outside the limit", c.name)
		}
	}
	// One refusal per query: a surface that asked the gate twice would count two.
	if _, _, rejected, _ := s.SearchLimiter.Stats(); rejected != uint64(len(calls)) {
		t.Errorf("rejected = %d for %d refused queries", rejected, len(calls))
	}
}

// With one slot, a request that took a second one — a REST route reaching the
// core, a hybrid search running its halves, an evaluation running its queries
// — would wait for itself and be refused.
func TestSearchBusy_OneSlotPerRequest(t *testing.T) {
	s, cleanup := limitedGold(t, 1, "2000")
	defer cleanup()
	for _, c := range searchSurfaceCalls() {
		if c.run(t, s) {
			t.Errorf("%s: refused with the slot free — it holds more than one", c.name)
		}
		if inUse, _, _, _ := s.SearchLimiter.Stats(); inUse != 0 {
			t.Fatalf("%s: left %d slots taken", c.name, inUse)
		}
	}
	if _, peak, rejected, _ := s.SearchLimiter.Stats(); peak != 1 || rejected != 0 {
		t.Errorf("peak %d, rejected %d; want 1 and 0", peak, rejected)
	}
}

// The gate itself: a context that holds a slot passes again for free, a
// server without a limiter admits everything, and a caller that gave up is
// answered with its own context's error, not a refusal.
func TestSearchSlot(t *testing.T) {
	s := &Server{SearchLimiter: limiterWith(t, 1, "5")}
	ctx, release, err := s.searchSlot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	inner, releaseInner, err := s.searchSlot(ctx)
	if err != nil || inner != ctx {
		t.Fatalf("a context holding a slot must pass unchanged: %v", err)
	}
	releaseInner()
	if inUse, _, _, _ := s.SearchLimiter.Stats(); inUse != 1 {
		t.Fatalf("inUse = %d, want the outer slot still held", inUse)
	}
	if _, err := limitedSearch(context.Background(), s, func(context.Context) (int, error) { return 1, nil }); !errors.Is(err, ErrSearchBusy) {
		t.Fatalf("err = %v, want ErrSearchBusy", err)
	}
	if got, err := limitedSearch(ctx, s, func(context.Context) (int, error) { return 7, nil }); err != nil || got != 7 {
		t.Fatalf("under the held slot: %d, %v", got, err)
	}
	release()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	held, _ := s.SearchLimiter.Acquire(context.Background())
	if _, _, err := s.searchSlot(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the caller's cancellation", err)
	}
	held()
	if _, _, rejected, _ := s.SearchLimiter.Stats(); rejected != 1 {
		t.Fatalf("rejected = %d; a cancelled caller is not a refusal", rejected)
	}

	// A negative queue timeout is no wait at all, not a wait that never ends.
	if l := limiterWith(t, 1, "-5"); l.wait != 0 {
		t.Fatalf("wait = %v for a negative timeout", l.wait)
	}

	open := &Server{}
	if got, err := limitedSearch(context.Background(), open, func(context.Context) (string, error) { return "ran", nil }); err != nil || got != "ran" {
		t.Fatalf("no limiter: %q, %v", got, err)
	}
}

// A refusal is counted in the metrics once, whatever surface it came from,
// and only a refusal is rendered as one.
func TestSearchBusyReporting(t *testing.T) {
	s, cleanup := limitedGold(t, 1, "5")
	defer cleanup()
	s.Metrics = metrics.NewMetrics(true, &serverMetricsStats{s: s})
	release, _ := s.SearchLimiter.Acquire(context.Background())
	defer release()
	if _, err := NewDirectClient(s).Aggregate(context.Background(), &AggregateRequest{Collection: "gold"}); !errors.Is(err, ErrSearchBusy) {
		t.Fatalf("err = %v, want ErrSearchBusy", err)
	}
	out := httptest.NewRecorder()
	s.Metrics.HandleMetrics(out, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if want := `mddb_operations_total{operation="search_rejected",label="concurrency"} 1`; !strings.Contains(out.Body.String(), want) {
		t.Fatalf("no %s in /metrics", want)
	}

	rec := httptest.NewRecorder()
	if writeSearchBusy(rec, errors.New("something else")) || rec.Body.Len() != 0 {
		t.Fatal("only a search refusal is written as one")
	}
	if searchBusyDetail(nil) != nil || mcpQuotaContent(errors.New("x")) != nil {
		t.Fatal("no detail for another error")
	}
	if got := status.Code(grpcError(codes.Internal, ErrSearchBusy)); got != codes.ResourceExhausted {
		t.Fatalf("grpcError = %v", got)
	}
}
