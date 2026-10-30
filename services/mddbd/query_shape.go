package main

import (
	"context"
	"hash/maphash"
	"net/http"
	"strings"
	"time"

	"mddb/internal/metrics"
	"mddb/internal/storage"

	"google.golang.org/grpc"
)

// Query-shape counting (OPS-025): which surface a request came in on and who
// sent it travel in the request context, set once by each transport — the
// HTTP chain (REST, GraphQL, /es), the gRPC chain and the MCP tool dispatch —
// so the search and graph code that counts a request needs only its context.
// The counters themselves live in internal/metrics.

// shapeScope is what a transport knows about a request for the counters.
type shapeScope struct {
	surface metrics.Surface
	// client identifies the caller for graph chains, hashed with a seed
	// made at start-up; 0 is unknown. It is never written anywhere.
	client uint64
}

type shapeScopeKey struct{}

// shapeSeed keys the client hashes; a new one each run, so a hash means
// nothing outside this process.
var shapeSeed = maphash.MakeSeed()

// clientHash hashes a caller identity of the given kind ("ip", "user",
// "session" …); an empty identity is unknown (0).
func clientHash(kind, id string) uint64 {
	if id == "" {
		return 0
	}
	var h maphash.Hash
	h.SetSeed(shapeSeed)
	_, _ = h.WriteString(kind)
	_ = h.WriteByte(0)
	_, _ = h.WriteString(id)
	return h.Sum64()
}

// withShapeScope returns ctx carrying sc.
func withShapeScope(ctx context.Context, sc shapeScope) context.Context {
	return context.WithValue(ctx, shapeScopeKey{}, sc)
}

// shapeScopeOf returns the scope a transport set; a context without one is
// a REST call with an unknown client (handlers called directly).
func shapeScopeOf(ctx context.Context) shapeScope {
	sc, _ := ctx.Value(shapeScopeKey{}).(shapeScope)
	return sc
}

// shapeClient identifies ctx's caller: the authenticated user when there is
// one, else what the transport saw.
func shapeClient(ctx context.Context) uint64 {
	if c, ok := GetClaimsFromContext(ctx); ok && c.Username != "" {
		return clientHash("user", c.Username)
	}
	return shapeScopeOf(ctx).client
}

// surfaceForPath is the surface an HTTP path belongs to.
func surfaceForPath(path string) metrics.Surface {
	switch {
	case path == "/graphql":
		return metrics.SurfaceGraphQL
	case path == esPathPrefix || strings.HasPrefix(path, esPathPrefix+"/"):
		return metrics.SurfaceES
	default:
		return metrics.SurfaceREST
	}
}

// withShapeSurface tags each HTTP request with its surface and client.
func withShapeSurface(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sc := shapeScope{surface: surfaceForPath(r.URL.Path), client: clientHash("ip", ClientIP(r))}
		next.ServeHTTP(w, r.WithContext(withShapeScope(r.Context(), sc)))
	})
}

// shapeUnaryInterceptor tags each gRPC call as the gRPC surface.
func shapeUnaryInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return handler(withShapeScope(ctx, shapeScope{surface: metrics.SurfaceGRPC, client: clientHash("ip", grpcPeerIP(ctx))}), req)
}

// withMCPSurface tags an MCP tool call. The MCP session is the client, or
// the key, or the address; a stdio server has one client.
func withMCPSurface(ctx context.Context) context.Context {
	c := mcpCallerFrom(ctx)
	client := clientHash("stdio", "stdio")
	switch {
	case c.sessionID != "":
		client = clientHash("session", c.sessionID)
	case c.keyName != "":
		client = clientHash("key", c.keyName)
	case c.clientIP != "":
		client = clientHash("ip", c.clientIP)
	}
	return withShapeScope(ctx, shapeScope{surface: metrics.SurfaceMCP, client: client})
}

// recordSearch counts one search on ctx's surface.
func (s *Server) recordSearch(ctx context.Context, sh metrics.SearchShape) {
	sh.Surface = shapeScopeOf(ctx).surface
	s.Metrics.QueryShape().Search(sh)
}

// recordGraph counts one graph request on ctx's surface, from ctx's client.
func (s *Server) recordGraph(ctx context.Context, g metrics.GraphShape) {
	g.Surface, g.Client = shapeScopeOf(ctx).surface, shapeClient(ctx)
	s.Metrics.QueryShape().Graph(g)
}

// filterShape names the operators of a search's metadata filters — the
// filterMeta equality lists, the operator filter and range filters — and
// counts the keys they constrain. Values are never read.
func filterShape(fm map[string][]string, f MetaFilter, ranges int) (metrics.FilterOps, int) {
	var ops metrics.FilterOps
	conds := len(f) + ranges
	if len(fm) > 0 {
		ops = ops.With(metrics.FilterIn)
	}
	for k := range fm {
		if _, both := f[k]; !both {
			conds++
		}
	}
	for _, c := range f {
		for _, op := range [...]struct {
			on bool
			op metrics.FilterOp
		}{
			{c.hasIn || len(c.In) > 0, metrics.FilterIn}, {c.Prefix != "", metrics.FilterPrefix},
			{c.Contains != "", metrics.FilterContains}, {c.Gt != nil, metrics.FilterGt}, {c.Gte != nil, metrics.FilterGte},
			{c.Lt != nil, metrics.FilterLt}, {c.Lte != nil, metrics.FilterLte}, {len(c.Not) > 0, metrics.FilterNot},
			{c.Exists != nil, metrics.FilterExists},
		} {
			if op.on {
				ops = ops.With(op.op)
			}
		}
	}
	if ranges > 0 {
		ops = ops.With(metrics.FilterRange)
	}
	return ops, conds
}

// shapedFTS is searchFTS, counted. Callers that serve a request use it;
// measurement (eval) and cross-collection fan-out call searchFTS directly.
func (s *Server) shapedFTS(ctx context.Context, req FTSSearchRequest, start time.Time) (*FTSSearchResponse, error) {
	resp, err := s.searchFTS(req, start)
	if err != nil {
		return nil, err
	}
	ops, conds := filterShape(req.FilterMeta, req.Filter, len(req.RangeMeta))
	s.recordSearch(ctx, metrics.SearchShape{Mode: metrics.ModeFTS, Collection: req.Collection, Algorithm: resp.Algorithm,
		FilterOps: ops, Conditions: conds, Results: len(resp.Results), LimitCapped: resp.LimitCapped})
	return resp, nil
}

// shapedListing is searchDocuments for a metadata listing, counted.
func (s *Server) shapedListing(ctx context.Context, collection string, filter MetaFilter, sortBy string, asc bool, offset, limit int,
	rep LimitReport) ([]storage.Doc, int, error) {
	page, total, err := s.searchDocuments(collection, filter, sortBy, asc, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	ops, conds := filterShape(nil, filter, 0)
	s.recordSearch(ctx, metrics.SearchShape{Mode: metrics.ModeMeta, Collection: collection, FilterOps: ops,
		Conditions: conds, Results: len(page), LimitCapped: rep.LimitCapped})
	return page, total, nil
}

// vectorShape describes a vector or hybrid search for the counters: the
// algorithm the planner ran (and whether the caller left the choice to it),
// the filters, the sub-queries of a decomposed query and the result count.
func vectorShape(mode metrics.SearchMode, collection, requested, ran string, fm map[string][]string, f MetaFilter,
	plan *SearchPlan, results int, rep LimitReport) metrics.SearchShape {
	ops, conds := filterShape(fm, f, 0)
	sh := metrics.SearchShape{Mode: mode, Collection: collection, Algorithm: ran, Auto: requested == AlgorithmAuto,
		FilterOps: ops, Conditions: conds, Results: results, LimitCapped: rep.LimitCapped}
	if plan != nil && plan.Decomposition != nil {
		sh.DecomposedParts = plan.Decomposition.Parts
	}
	return sh
}

// shapedCodeGraph is CodeGraph, counted: REST, MCP and GraphQL all walk the
// graph through it. A request without a start document is counted too — it
// is the "show me the map" question CODE-007 asks about — and then refused
// by CodeGraph.
func (s *Server) shapedCodeGraph(ctx context.Context, req GraphRequest) (*GraphResult, error) {
	res, err := s.CodeGraph(req)
	s.recordGraph(ctx, codeGraphShape(req.Collection, req.Key, req.Depth, req.MaxDegree, res))
	return res, err
}

// codeGraphShape describes a code-graph request: the depth asked for against
// GraphMaxDepth, and whether it asked for the whole map — no start document,
// or depth and fan-out at their maximum (CODE-007); an unset fan-out is the
// default, which is the maximum. res is nil when the walk did not run.
func codeGraphShape(collection, key string, depth, maxDegree int, res *GraphResult) metrics.GraphShape {
	if maxDegree <= 0 {
		maxDegree = GraphDefaultDegree
	}
	g := metrics.GraphShape{Kind: metrics.GraphCodeGraph, Collection: collection, Depth: depth, MaxDepth: GraphMaxDepth,
		NoStart: key == "", WholeGraph: depth >= GraphMaxDepth && maxDegree >= GraphMaxDegree, Start: key}
	if res != nil {
		g.Truncated = res.Truncated
		g.Reached = func(yield func(string) bool) {
			for _, n := range res.Nodes {
				if !yield(n.Key) {
					return
				}
			}
		}
	}
	return g
}
