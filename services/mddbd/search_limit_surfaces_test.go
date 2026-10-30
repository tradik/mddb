package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gql "mddb/graphql"
	proto "mddb/proto"
)

// The same over-the-cap request over MCP, gRPC and GraphQL (#368): each
// surface returns at most the cap and says it was capped.

// wantMCPCapped requires a tool result that is capped to want, in the text
// and in structuredContent.
func wantMCPCapped(t *testing.T, tool string, res map[string]any, want int) {
	t.Helper()
	if res["isError"] == true {
		t.Fatalf("%s: %v", tool, res)
	}
	detail, _ := res["structuredContent"].(map[string]any)
	if detail["limitCapped"] != true || detail["limit"] != want {
		t.Fatalf("%s: structuredContent %v, want capped to %d", tool, detail, want)
	}
	text := res["content"].([]map[string]any)[0]["text"].(string)
	if rep := limitOf(t, []byte(text)); !rep.LimitCapped || rep.Limit != want {
		t.Fatalf("%s: text %s", tool, text)
	}
}

func TestSearchLimit_MCPTools(t *testing.T) {
	s, cleanup := cappedGold(t)
	defer cleanup()
	gold := map[string]any{"collection": "gold", "query": "pump water"}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range gold {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
	}{
		"fts":              {"full_text_search", with(map[string]any{"limit": 3})},
		"fts-projected":    {"full_text_search", with(map[string]any{"limit": 3, "fields": []any{"title"}})},
		"fts-passages":     {"full_text_search", with(map[string]any{"limit": 3, "format": "passages"})},
		"vector":           {"semantic_search", with(map[string]any{"top_k": 3})},
		"vector-projected": {"semantic_search", with(map[string]any{"top_k": 3, "fields": []any{"title"}})},
		"vector-passages":  {"semantic_search", with(map[string]any{"top_k": 3, "format": "passages"})},
		"hybrid":           {"hybrid_search", with(map[string]any{"top_k": 3})},
		"hybrid-default":   {"hybrid_search", gold},
		"hybrid-passages":  {"hybrid_search", with(map[string]any{"top_k": 3, "format": "passages"})},
		"documents":        {"search_documents", map[string]any{"collection": "gold", "limit": 3}},
		"docs-projected":   {"search_documents", map[string]any{"collection": "gold", "limit": 3, "fields": []any{"title"}}},
		"docs-passages":    {"search_documents", map[string]any{"collection": "gold", "limit": 3, "format": "passages"}},
		"autocomplete":     {"autocomplete", map[string]any{"collection": "gold", "q": "p", "top_n": 3}},
		"cross":            {"cross_search", map[string]any{"target_collections": []any{"gold"}, "query": "pump water", "top_k": 3}},
		"cross-passages":   {"cross_search", map[string]any{"target_collections": []any{"gold"}, "query": "pump water", "top_k": 3, "format": "passages"}},
		"cross-fts":        {"cross_search", map[string]any{"target_collections": []any{"gold"}, "query": "pump water", "top_k": 3, "search_type": "fts"}},
		"cross-hybrid-p":   {"cross_search", map[string]any{"target_collections": []any{"gold"}, "query": "pump water", "top_k": 3, "search_type": "hybrid", "format": "passages"}},
		"symbol":           {"code_symbol", map[string]any{"collection": "gold", "name": "pump", "limit": 3}},
	} {
		wantMCPCapped(t, name, mcpCall(s, tc.tool, tc.args), 2)
	}
	// Under the cap: no structured content, as before.
	if res := mcpCall(s, "full_text_search", with(map[string]any{"limit": 2})); res["structuredContent"] != nil {
		t.Fatalf("under the cap: %v", res)
	}
}

func TestSearchLimit_GRPC(t *testing.T) {
	s, cleanup := cappedGold(t)
	defer cleanup()
	ctx := context.Background()
	g := &GRPCServer{server: s}
	check := func(name string, capped bool, limit int32, n int, err error) {
		t.Helper()
		if err != nil || !capped || limit != 2 || n > 2 {
			t.Fatalf("%s: err %v, capped %v, limit %d, %d results", name, err, capped, limit, n)
		}
	}
	f, err := g.FTS(ctx, &proto.FTSRequest{Collection: "gold", Query: "pump water", Limit: 3})
	check("fts", f.GetLimitCapped(), f.GetLimit(), len(f.GetResults()), err)
	fp, err := g.FTS(ctx, &proto.FTSRequest{Collection: "gold", Query: "pump water", Limit: 3, Format: "passages"})
	check("fts passages", fp.GetLimitCapped(), fp.GetLimit(), 0, err)
	v, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "gold", Query: "pump water", TopK: 3})
	check("vector", v.GetLimitCapped(), v.GetLimit(), len(v.GetResults()), err)
	vp, err := g.VectorSearch(ctx, &proto.VectorSearchRequest{Collection: "gold", Query: "pump water", TopK: 3, Format: "passages"})
	check("vector passages", vp.GetLimitCapped(), vp.GetLimit(), 0, err)
	h, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "gold", Query: "pump water", TopK: 3})
	check("hybrid", h.GetLimitCapped(), h.GetLimit(), len(h.GetResults()), err)
	hp, err := g.HybridSearch(ctx, &proto.HybridSearchRequest{Collection: "gold", Query: "pump water", TopK: 3, Format: "passages"})
	check("hybrid passages", hp.GetLimitCapped(), hp.GetLimit(), 0, err)
	c, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{TargetCollections: []string{"gold"}, Query: "pump water", TopK: 3})
	check("cross", c.GetLimitCapped(), c.GetLimit(), len(c.GetResults()), err)
	cp, err := g.CrossSearch(ctx, &proto.CrossSearchRequest{TargetCollections: []string{"gold"}, Query: "pump water", TopK: 3, Format: "passages"})
	check("cross passages", cp.GetLimitCapped(), cp.GetLimit(), 0, err)
	d, err := g.Search(ctx, &proto.SearchRequest{Collection: "gold", Limit: 3})
	check("search", d.GetLimitCapped(), d.GetLimit(), len(d.GetDocuments()), err)
	sym, err := g.CodeSymbol(ctx, &proto.CodeSymbolRequest{Collection: "gold", Name: "pump", Limit: 3})
	check("code symbol", sym.GetLimitCapped(), sym.GetLimit(), 0, err)

	// Under the cap nothing is reported, as before.
	if u, err := g.Search(ctx, &proto.SearchRequest{Collection: "gold", Limit: 2}); err != nil || u.GetLimitCapped() || u.GetLimit() != 0 {
		t.Fatalf("search under the cap: %v %+v", err, u)
	}
}

func TestSearchLimit_GraphQL(t *testing.T) {
	s, cleanup := cappedGold(t)
	defer cleanup()
	ctx := context.Background()
	a := &GraphQLAdapter{server: s, mcp: NewDirectClient(s)}
	three, query, passages := 3, "pump water", "passages"
	check := func(name string, capped *bool, limit *int, err error) {
		t.Helper()
		if err != nil || capped == nil || !*capped || limit == nil || *limit != 2 {
			t.Fatalf("%s: %v capped=%v limit=%v", name, err, capped, limit)
		}
	}
	docs, err := a.SearchDocuments(ctx, gql.SearchInput{Collection: "gold", Limit: &three})
	check("search", docs.LimitCapped, docs.Limit, err)
	if len(docs.Edges) != 2 {
		t.Fatalf("search edges: %d", len(docs.Edges))
	}
	v, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "gold", Query: &query, TopK: &three})
	check("vector", v.LimitCapped, v.Limit, err)
	vp, err := a.VectorSearch(ctx, gql.VectorSearchInput{Collection: "gold", Query: &query, TopK: &three, Format: &passages})
	check("vector passages", vp.LimitCapped, vp.Limit, err)
	f, err := a.FullTextSearch(ctx, gql.FTSInput{Collection: "gold", Query: query, Limit: &three})
	check("fts", f.LimitCapped, f.Limit, err)
	fp, err := a.FullTextSearch(ctx, gql.FTSInput{Collection: "gold", Query: query, Limit: &three, Format: &passages})
	check("fts passages", fp.LimitCapped, fp.Limit, err)
	sym, err := a.CodeSymbol(ctx, gql.CodeSymbolInput{Collection: "gold", Name: "pump", Limit: &three})
	check("code symbol", sym.LimitCapped, sym.Limit, err)

	// Under the cap both fields are null.
	two := 2
	u, err := a.FullTextSearch(ctx, gql.FTSInput{Collection: "gold", Query: query, Limit: &two})
	if err != nil || u.LimitCapped != nil || u.Limit != nil {
		t.Fatalf("fts under the cap: %v %+v", err, u)
	}

	// Through the production handler, as a client asks for the fields.
	out := graphQLPost(t, s, `{
		fts(input: {collection: "gold", query: "pump water", limit: 3}) { total limitCapped limit }
		vectorSearch(input: {collection: "gold", query: "pump water", topK: 3}) { total limitCapped limit }
		search(input: {collection: "gold", limit: 3}) { totalCount limitCapped limit }
		codeSymbol(input: {collection: "gold", name: "pump", limit: 3}) { symbol limitCapped limit }
	}`)
	data, _ := out["data"].(map[string]any)
	for _, field := range []string{"fts", "vectorSearch", "search", "codeSymbol"} {
		got, _ := data[field].(map[string]any)
		if got["limitCapped"] != true || got["limit"] != float64(2) {
			t.Fatalf("graphql %s: %v", field, out)
		}
	}
}

// TestSearchLimit_Geo: a radius search's topK is capped on REST, MCP and
// gRPC, and the geo indexes' default of 10 still applies under the cap.
func TestSearchLimit_Geo(t *testing.T) {
	s, cleanup := newTestServerForGeo(t)
	defer cleanup()
	seedGeoDocs(t, s, "v", [][2]float64{{52.52, 13.405}, {52.521, 13.406}, {52.522, 13.407}})
	s.SearchMaxLimit = 2
	ctx := context.Background()

	for _, algo := range []string{"rtree", "geohash"} {
		body := `{"collection":"v","lat":52.52,"lng":13.405,"radiusMeters":5000,"topK":3,"algorithm":"` + algo + `"}`
		rec := httptest.NewRecorder()
		s.handleGeoSearch(rec, httptest.NewRequest(http.MethodPost, "/v1/geo-search", strings.NewReader(body)))
		wantCapped(t, "rest geo "+algo, rec, 2)
		var resp GeoSearchResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.Total != 2 {
			t.Fatalf("rest geo %s: %d results", algo, resp.Total)
		}
		m, err := NewDirectClient(s).GeoSearch(ctx, &MCPGeoSearchRequest{Collection: "v", Lat: 52.52, Lng: 13.405, RadiusMeters: 5000, TopK: 3, Algorithm: algo})
		if err != nil || !m.LimitCapped || m.Limit != 2 || m.Total != 2 {
			t.Fatalf("mcp geo %s: %v %+v", algo, err, m)
		}
	}
	// A filter nothing matches answers empty, still reporting the cap.
	empty := `{"collection":"v","lat":52.52,"lng":13.405,"radiusMeters":5000,"topK":3,"filterMeta":{"k":["none"]}}`
	rec := httptest.NewRecorder()
	s.handleGeoSearch(rec, httptest.NewRequest(http.MethodPost, "/v1/geo-search", strings.NewReader(empty)))
	wantCapped(t, "rest geo empty", rec, 2)
	m, err := NewDirectClient(s).GeoSearch(ctx, &MCPGeoSearchRequest{Collection: "v", Lat: 52.52, Lng: 13.405, RadiusMeters: 5000, TopK: 3,
		FilterMeta: map[string][]string{"k": {"none"}}})
	if err != nil || !m.LimitCapped {
		t.Fatalf("mcp geo empty: %v %+v", err, m)
	}
	wantMCPCapped(t, "geo_search", mcpCall(s, "geo_search", map[string]any{"collection": "v", "lat": 52.52, "lng": 13.405, "radius_meters": 5000.0, "top_k": 3}), 2)

	g := &GRPCServer{server: s}
	gr, err := g.GeoSearch(ctx, &proto.GeoSearchRequest{Collection: "v", Lat: 52.52, Lng: 13.405, RadiusMeters: 5000, TopK: 3})
	if err != nil || !gr.LimitCapped || gr.Limit != 2 || gr.Total != 2 {
		t.Fatalf("grpc geo: %v %+v", err, gr)
	}
	ge, err := g.GeoSearch(ctx, &proto.GeoSearchRequest{Collection: "v", Lat: 52.52, Lng: 13.405, RadiusMeters: 5000, TopK: 3,
		FilterMeta: map[string]*proto.MetaValues{"k": {Values: []string{"none"}}}})
	if err != nil || !ge.LimitCapped {
		t.Fatalf("grpc geo empty: %v %+v", err, ge)
	}

	// Under the cap: the default of 10 applies and nothing is reported.
	s.SearchMaxLimit = 0
	if n, rep := s.geoTopK("v", 0); n != geoDefaultTopK || rep.LimitCapped {
		t.Fatalf("geo default: %d %+v", n, rep)
	}
}
