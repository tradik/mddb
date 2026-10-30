package metrics

import (
	"fmt"
	"iter"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// keys yields the given document keys, as a graph answer's nodes do.
func keys(ks ...string) iter.Seq[string] { return slices.Values(ks) }

func TestQueryShapeCountsSearches(t *testing.T) {
	q := NewQueryShape()
	q.Search(SearchShape{Surface: SurfaceREST, Mode: ModeFTS, Collection: "docs", Algorithm: "bm25", Results: 3,
		FilterOps: FilterOps(0).With(FilterIn).With(FilterGt), Conditions: 2})
	q.Search(SearchShape{Surface: SurfaceMCP, Mode: ModeVector, Collection: "docs", Algorithm: "hnsw", Auto: true,
		LimitCapped: true, DecomposedParts: 3})
	q.Search(SearchShape{Surface: SurfaceES, Mode: ModeFTS, Collection: "other", Algorithm: "made-up", Conditions: 9})
	q.Search(SearchShape{Surface: surfaceCount}) // out of range: ignored
	q.Search(SearchShape{Mode: modeCount})
	var nilQ *QueryShape
	nilQ.Search(SearchShape{})
	nilQ.Graph(GraphShape{})

	r := q.Report(func(c string) string { return "h_" + c })
	if got := r.Searches["rest"]["fts"]; got != (SearchCounts{Total: 1}) {
		t.Errorf("rest fts %+v", got)
	}
	if got := r.Searches["mcp"]["vector"]; got != (SearchCounts{Total: 1, Empty: 1, LimitCapped: 1}) {
		t.Errorf("mcp vector %+v", got)
	}
	if r.EmptyRatio["fts"] != 0.5 || r.EmptyRatio["vector"] != 1 {
		t.Errorf("empty ratio %v", r.EmptyRatio)
	}
	if r.Algorithms["fts"]["bm25"] != 1 || r.Algorithms["fts"]["other"] != 1 || r.AutoChoices["hnsw"] != 1 {
		t.Errorf("algorithms %v auto %v", r.Algorithms, r.AutoChoices)
	}
	if r.Filters.Operators["in"] != 1 || r.Filters.Operators["gt"] != 1 || r.Filters.Conditions["2"] != 1 ||
		r.Filters.Conditions["5+"] != 1 || r.Filters.Conditions["0"] != 1 {
		t.Errorf("filters %+v", r.Filters)
	}
	if r.Decomposition.Searches["mcp"] != 1 || r.Decomposition.Parts != 3 {
		t.Errorf("decomposition %+v", r.Decomposition)
	}
	if c := r.Collections["h_docs"]; c.Searches["fts"] != 1 || c.Searches["vector"] != 1 || c.Empty["vector"] != 1 || c.LimitCapped != 1 {
		t.Errorf("collection row %+v", c)
	}
	if _, raw := r.Collections["docs"]; raw {
		t.Error("a collection kept its name in a hashed report")
	}
	if r.Since == "" {
		t.Error("no since")
	}
	if nilQ.Report(nil).Searches == nil {
		t.Error("a nil report has nil maps")
	}
}

func TestQueryShapeBoundsCollections(t *testing.T) {
	q := NewQueryShape()
	for i := range maxShapeCollections + 5 {
		q.Search(SearchShape{Mode: ModeMeta, Collection: fmt.Sprint("c", i), Results: 1})
	}
	r := q.Report(nil)
	if len(r.Collections) != maxShapeCollections+1 || r.Collections[otherCollection].Searches["meta"] != 5 {
		t.Fatalf("%d collections, other %+v", len(r.Collections), r.Collections[otherCollection])
	}
	// A collection already counted keeps its row; a new one past the bound
	// joins "_other" whether or not the row exists yet.
	q.Search(SearchShape{Mode: ModeMeta, Collection: "c0", Results: 1})
	if q.Report(nil).Collections["c0"].Searches["meta"] != 2 {
		t.Error("a known collection lost its row")
	}
	q2 := NewQueryShape()
	for i := range maxShapeCollections {
		q2.collection(fmt.Sprint("c", i))
	}
	if q2.collection("new") != q2.collection("newer") {
		t.Error("past the bound, two new collections got two rows")
	}
}

func TestQueryShapeCountsGraphAndGates(t *testing.T) {
	q := NewQueryShape()
	now := time.Unix(1000, 0)
	q.graph.chains.now = func() time.Time { return now }
	call := func(client uint64, start string, reached ...string) {
		q.Graph(GraphShape{Surface: SurfaceREST, Kind: GraphCodeGraph, Collection: "code", Depth: 2, MaxDepth: 3,
			Client: client, Start: start, Reached: keys(reached...)})
	}
	call(1, "a", "b", "c")
	call(1, "b", "d") // chain 2
	call(1, "d", "e") // chain 3
	call(2, "x")      // another client: its own chain
	call(1, "zzz")    // not reached by the last answer: chain of 3 ends
	now = now.Add(time.Minute)
	call(1, "zzz") // window passed: the chain of 1 ends, another starts

	q.Graph(GraphShape{Surface: SurfaceMCP, Kind: GraphCodeGraph, Collection: "code", Depth: 9, MaxDepth: 3,
		WholeGraph: true, Truncated: true})
	q.Graph(GraphShape{Surface: SurfaceGraphQL, Kind: GraphCodeGraph, Collection: "code", NoStart: true})
	q.Graph(GraphShape{Surface: SurfaceREST, Kind: GraphExpand, Collection: "code", Depth: 5, MaxDepth: 3})
	q.Graph(GraphShape{Kind: graphKindCount}) // out of range: ignored

	r := q.Report(nil)
	g := r.Graph
	if g.Requests["rest"]["code_graph"] != 6 || g.Requests["mcp"]["code_graph"] != 1 || g.Requests["rest"]["expand"] != 1 {
		t.Errorf("requests %v", g.Requests)
	}
	if g.DepthClamped["mcp"]["code_graph"] != 1 || g.DepthClamped["rest"]["expand"] != 1 || g.Depth["code_graph"]["2"] != 6 {
		t.Errorf("clamped %v depth %v", g.DepthClamped, g.Depth)
	}
	if g.NoStart["graphql"] != 1 || g.WholeGraph["mcp"] != 1 || g.Truncated["code_graph"] != 1 {
		t.Errorf("noStart %v whole %v truncated %v", g.NoStart, g.WholeGraph, g.Truncated)
	}
	// Ended: client 1's chain of 3, then its chain of 1, and client 2's chain
	// of 1 (expired at the report); client 1's last chain is still open.
	if g.Chains.Sessions != 3 || g.Chains.Length["3"] != 1 || g.Chains.Length["1"] != 2 || g.Chains.AtLeast3 != 1 || g.Chains.Open != 1 {
		t.Errorf("chains %+v", g.Chains)
	}
	if r.Collections["code"].Graph != 9 || r.Collections["code"].DepthClamped != 2 {
		t.Errorf("collection %+v", r.Collections["code"])
	}
	// Too few sessions for the gate, whatever the share.
	if r.Gates.Code006.Share < 0.33 || r.Gates.Code006.Met || r.Gates.Code007.Numerator != 2 || r.Gates.Code007.Denominator != 8 {
		t.Errorf("gates %+v", r.Gates)
	}
	if g := newGate("m", 2, 20, 0.05, 20); !g.Met || g.Share != 0.1 {
		t.Errorf("gate over threshold %+v", g)
	}
	if g := newGate("m", 0, 0, 0.05, 0); g.Met || g.Share != 0 {
		t.Errorf("empty gate %+v", g)
	}
}

func TestChainTrackerBoundsClientsAndKeys(t *testing.T) {
	now := time.Unix(0, 0)
	tr := newChainTracker(func() time.Time { return now })
	tr.observe(0, "c", "a", nil) // unknown client: not tracked
	if len(tr.clients) != 0 {
		t.Fatal("client 0 was tracked")
	}
	for i := range maxChainClients {
		tr.observe(uint64(i+1), "c", "a", nil)
	}
	tr.observe(999999, "c", "a", nil) // full, nothing expired
	if tr.untracked != 1 {
		t.Fatalf("untracked %d", tr.untracked)
	}
	now = now.Add(time.Hour)
	tr.observe(999999, "c", "a", nil) // full, everything expired: swept
	if tr.untracked != 1 || len(tr.clients) != 1 || tr.sessions != maxChainClients {
		t.Fatalf("after sweep: untracked %d clients %d sessions %d", tr.untracked, len(tr.clients), tr.sessions)
	}
	many := make([]string, maxChainReached+10)
	for i := range many {
		many[i] = fmt.Sprint("k", i)
	}
	tr.observe(999999, "c", "a", slices.Values(many))
	if n := len(tr.clients[999999].reached); n != maxChainReached {
		t.Fatalf("reached %d", n)
	}
}

func TestQueryShapePrometheus(t *testing.T) {
	m := NewMetrics(true, stubStats{})
	if (*Metrics)(nil).QueryShape() != nil {
		t.Fatal("nil collector has counters")
	}
	empty := scrape(t, m)
	for _, want := range []string{
		`mddb_query_shape_graph_depth_clamped_total{surface="rest",kind="code_graph"} 0`,
		`mddb_query_shape_graph_chain_length_bucket{le="+Inf"} 0`,
		"mddb_query_shape_graph_chains_open 0",
	} {
		if !strings.Contains(empty, want) {
			t.Errorf("idle scrape lacks %q", want)
		}
	}
	if strings.Contains(empty, "mddb_query_shape_searches_total") {
		t.Error("an idle server reports search series")
	}

	q := m.QueryShape()
	q.Search(SearchShape{Surface: SurfaceGRPC, Mode: ModeHybrid, Collection: "c", Algorithm: "flat", Auto: true,
		FilterOps: FilterOps(0).With(FilterExists), Conditions: 1, LimitCapped: true, DecomposedParts: 2})
	q.Graph(GraphShape{Surface: SurfaceES, Kind: GraphExpand, Collection: "c", Depth: 7, MaxDepth: 3, Truncated: true})
	q.Graph(GraphShape{Surface: SurfaceMCP, Kind: GraphCodeGraph, Collection: "c", WholeGraph: true})
	q.Graph(GraphShape{Surface: SurfaceMCP, Kind: GraphCodeGraph, Collection: "c", NoStart: true})
	body := scrape(t, m)
	for _, want := range []string{
		`mddb_query_shape_searches_total{surface="grpc",mode="hybrid"} 1`,
		`mddb_query_shape_empty_results_total{surface="grpc",mode="hybrid"} 1`,
		`mddb_query_shape_limit_capped_total{surface="grpc",mode="hybrid"} 1`,
		`mddb_query_shape_algorithm_total{mode="hybrid",algorithm="flat"} 1`,
		`mddb_query_shape_auto_choice_total{algorithm="flat"} 1`,
		`mddb_query_shape_filter_operator_total{operator="exists"} 1`,
		`mddb_query_shape_filter_conditions_total{conditions="1"} 1`,
		`mddb_query_shape_decomposed_total{surface="grpc"} 1`,
		"mddb_query_shape_decomposition_parts_total 2",
		`mddb_query_shape_graph_requests_total{surface="es",kind="expand"} 1`,
		`mddb_query_shape_graph_depth_total{kind="expand",depth="5+"} 1`,
		`mddb_query_shape_graph_depth_clamped_total{surface="es",kind="expand"} 1`,
		`mddb_query_shape_graph_truncated_total{kind="expand"} 1`,
		`mddb_query_shape_graph_whole_graph_total{surface="mcp"} 1`,
		`mddb_query_shape_graph_no_start_total{surface="mcp"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape lacks %q", want)
		}
	}
	if strings.Contains(body, `graph_depth_clamped_total{surface="rest",kind="code_graph"} 0`) {
		t.Error("a family with a real series still writes its zero placeholder")
	}
	var nilQ *QueryShape
	var sb strings.Builder
	nilQ.writeQueryShape(&sb)
	if sb.Len() != 0 {
		t.Error("nil counters wrote series")
	}
}

func TestQueryShapeChainHistogramInScrape(t *testing.T) {
	m := NewMetrics(true, stubStats{})
	q := m.QueryShape()
	now := time.Unix(0, 0)
	q.graph.chains.now = func() time.Time { return now }
	for _, start := range []string{"a", "b", "c", "d"} {
		q.Graph(GraphShape{Surface: SurfaceREST, Kind: GraphCodeGraph, Collection: "c", Client: 7, Start: start,
			Reached: keys("b", "c", "d")})
	}
	now = now.Add(time.Hour)
	body := scrape(t, m)
	for _, want := range []string{
		`mddb_query_shape_graph_chain_length_bucket{le="3"} 0`,
		`mddb_query_shape_graph_chain_length_bucket{le="4"} 1`,
		"mddb_query_shape_graph_chain_length_sum 4",
		"mddb_query_shape_graph_chain_length_count 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape lacks %q\n%s", want, body)
		}
	}
}

func TestSurfaceNames(t *testing.T) {
	if SurfaceGraphQL.String() != "graphql" || surfaceCount.String() != "other" {
		t.Fatal(SurfaceGraphQL.String(), surfaceCount.String())
	}
}

// scrape returns m's /metrics body.
func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.HandleMetrics(rec, httptest.NewRequest("GET", "/metrics", nil))
	return rec.Body.String()
}

// BenchmarkQueryShapeSearch is the hot path: one search counted on a
// collection already seen. It must not allocate.
func BenchmarkQueryShapeSearch(b *testing.B) {
	q := NewQueryShape()
	s := SearchShape{Surface: SurfaceREST, Mode: ModeFTS, Collection: "docs", Algorithm: "bm25",
		FilterOps: FilterOps(0).With(FilterIn), Conditions: 1, Results: 10}
	q.Search(s)
	b.ReportAllocs()
	for b.Loop() {
		q.Search(s)
	}
}

func TestQueryShapeSearchDoesNotAllocate(t *testing.T) {
	q := NewQueryShape()
	s := SearchShape{Surface: SurfaceREST, Mode: ModeVector, Collection: "docs", Algorithm: "hnsw", Auto: true,
		FilterOps: FilterOps(0).With(FilterIn).With(FilterRange), Conditions: 2, LimitCapped: true, DecomposedParts: 2}
	q.Search(s)
	g := GraphShape{Surface: SurfaceREST, Kind: GraphExpand, Collection: "docs", Depth: 5, MaxDepth: 3}
	q.Graph(g)
	if n := testing.AllocsPerRun(1000, func() { q.Search(s); q.Graph(g) }); n != 0 {
		t.Fatalf("%v allocations per counted search and expansion", n)
	}
}
