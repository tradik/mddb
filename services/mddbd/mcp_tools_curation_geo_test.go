package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mddb/internal/geo"
)

// The curation tools and geo_polygon over MCP: arguments in, compact JSON out.

// curationDownClient fails every curation call.
type curationDownClient struct{ *covBFakeClient }

var errCurationDown = errors.New("curation store offline")

func (curationDownClient) ListCurationRules(context.Context, string) ([]*CurationRule, error) {
	return nil, errCurationDown
}
func (curationDownClient) CreateCurationRule(context.Context, *CurationRule) (*CurationRule, error) {
	return nil, errCurationDown
}
func (curationDownClient) UpdateCurationRule(context.Context, *CurationRule) (*CurationRule, error) {
	return nil, errCurationDown
}
func (curationDownClient) DeleteCurationRule(context.Context, string) error { return errCurationDown }

func TestMCPCurationTools(t *testing.T) {
	s, cleanup := newHandlerTestServer(t)
	defer cleanup()
	s.CurationManager = NewCurationManager(s.DB)
	if err := s.CurationManager.EnsureBucket(); err != nil {
		t.Fatal(err)
	}
	ts := &MCPToolServer{client: NewDirectClient(s), globalMode: ModeRW}
	ctx := context.Background()
	addTestDoc(t, s, "kb", "a.md", "en", "alpha", nil)
	addTestDoc(t, s, "kb", "b.md", "en", "beta", nil)

	out, err := ts.mcpCallTool(ctx, "create_curation_rule", map[string]interface{}{"collection": "kb", "query": "alpha",
		"pins":  []interface{}{"a.md", map[string]interface{}{"key": "b.md", "lang": "en", "position": 2.0}},
		"hides": []interface{}{"c.md", 7.0}, "enabled": true})
	if err != nil || !strings.Contains(out, `"query":"alpha"`) || strings.Contains(out, "\n") {
		t.Fatalf("create: %v %s", err, out)
	}
	id := strings.Split(strings.Split(out, `"id":"`)[1], `"`)[0]
	out, err = ts.mcpCallTool(ctx, "list_curation_rules", map[string]interface{}{"collection": "kb"})
	if err != nil || !strings.Contains(out, `"total":1`) || !strings.Contains(out, id) {
		t.Fatalf("list: %v %s", err, out)
	}
	out, err = ts.mcpCallTool(ctx, "update_curation_rule", map[string]interface{}{"id": id, "collection": "kb", "query": "beta", "enabled": "yes"})
	if err != nil || !strings.Contains(out, `"query":"beta"`) {
		t.Fatalf("update: %v %s", err, out)
	}
	if _, err = ts.mcpCallTool(ctx, "update_curation_rule", map[string]interface{}{"collection": "kb", "query": "x"}); err == nil {
		t.Fatal("update without id")
	}
	if _, err = ts.mcpCallTool(ctx, "delete_curation_rule", map[string]interface{}{}); err == nil {
		t.Fatal("delete without id")
	}
	out, err = ts.mcpCallTool(ctx, "delete_curation_rule", map[string]interface{}{"id": id})
	if err != nil || out != `{"status":"deleted","id":"`+id+`"}` {
		t.Fatalf("delete: %v %s", err, out)
	}

	down := &MCPToolServer{client: curationDownClient{covBNewFake()}, globalMode: ModeRW}
	for tool, args := range map[string]map[string]interface{}{
		"list_curation_rules":  {"collection": "kb"},
		"create_curation_rule": {"collection": "kb", "query": "q"},
		"update_curation_rule": {"id": "r1", "collection": "kb", "query": "q"},
		"delete_curation_rule": {"id": "r1"},
	} {
		if _, err := down.mcpCallTool(ctx, tool, args); !errors.Is(err, errCurationDown) {
			t.Errorf("%s: %v", tool, err)
		}
	}
}

// geoDownClient fails the geo statistics call.
type geoDownClient struct{ *covBFakeClient }

func (geoDownClient) GeoStats(context.Context) (*MCPGeoStatsResponse, error) {
	return nil, errors.New("geo index offline")
}

func (geoDownClient) GeoWithin(context.Context, *MCPGeoWithinRequest) (*MCPGeoSearchResponse, error) {
	return nil, errors.New("geo index offline")
}

// square is a closed GeoJSON ring around the origin.
func square(size float64) map[string]interface{} {
	return map[string]interface{}{"type": "Polygon", "coordinates": []interface{}{[]interface{}{
		[]interface{}{-size, -size}, []interface{}{size, -size}, []interface{}{size, size}, []interface{}{-size, size}, []interface{}{-size, -size}}}}
}

func TestMCPToolGeoPolygon(t *testing.T) {
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	ctx := context.Background()
	call := func(args map[string]interface{}) (string, error) { return ts.mcpCallTool(ctx, "geo_polygon", args) }

	out, err := call(map[string]interface{}{"collection": "places", "polygon": square(1), "include_content": true})
	if err != nil || out != `{"results":[],"total":0,"shape":"polygon","algorithm":"rtree"}` {
		t.Fatalf("polygon: %v %s", err, out)
	}
	multi := map[string]interface{}{"type": "MultiPolygon", "coordinates": []interface{}{square(1)["coordinates"]}}
	out, err = call(map[string]interface{}{"collection": "places", "multi_polygon": multi})
	if err != nil || !strings.Contains(out, `"shape":"multiPolygon"`) {
		t.Fatalf("multi polygon: %v %s", err, out)
	}
	// A metadata filter nothing matches answers without searching.
	for shape, args := range map[string]map[string]interface{}{
		"polygon":      {"collection": "places", "polygon": square(1)},
		"multiPolygon": {"collection": "places", "multi_polygon": multi},
	} {
		args["filter_meta"] = map[string]interface{}{"city": []interface{}{"nowhere"}}
		if out, err = call(args); err != nil || !strings.Contains(out, `"shape":"`+shape+`"`) {
			t.Fatalf("empty filter %s: %v %s", shape, err, out)
		}
	}
	for label, args := range map[string]map[string]interface{}{
		"no collection":     {"polygon": square(1)},
		"neither shape":     {"collection": "places"},
		"both shapes":       {"collection": "places", "polygon": square(1), "multi_polygon": multi},
		"not a polygon":     {"collection": "places", "polygon": "square"},
		"not marshallable":  {"collection": "places", "polygon": make(chan int)},
		"multi not a shape": {"collection": "places", "multi_polygon": "squares"},
		"multi unmarshal":   {"collection": "places", "multi_polygon": make(chan int)},
		"open ring":         {"collection": "places", "polygon": map[string]interface{}{"type": "Polygon", "coordinates": []interface{}{[]interface{}{[]interface{}{0.0, 0.0}, []interface{}{1.0, 1.0}}}}},
		"empty multi":       {"collection": "places", "multi_polygon": map[string]interface{}{"type": "MultiPolygon", "coordinates": []interface{}{}}},
	} {
		if _, err := call(args); err == nil {
			t.Errorf("%s: accepted", label)
		}
	}
	// A filter that matches documents narrows the search to them.
	dc := ts.client.(*DirectClient)
	addTestDoc(t, dc.server, "places", "cafe.md", "en", "cafe", map[string][]string{"city": {"lodz"}})
	out, err = call(map[string]interface{}{"collection": "places", "polygon": square(1), "filter_meta": map[string]interface{}{"city": []interface{}{"lodz"}}})
	if err != nil || !strings.Contains(out, `"total":0`) {
		t.Fatalf("matching filter: %v %s", err, out)
	}
	// include_content reaches the radius and box searches too.
	if out, err = ts.mcpCallTool(ctx, "geo_search", map[string]interface{}{"collection": "places", "lat": 1.0, "lng": 1.0, "radius_meters": 10.0, "include_content": true}); err != nil || strings.Contains(out, "\n  ") {
		t.Fatalf("geo_search: %v %s", err, out)
	}
	if _, err = ts.mcpCallTool(ctx, "geo_within", map[string]interface{}{"collection": "places", "min_lat": 0.0, "min_lng": 0.0, "max_lat": 1.0, "max_lng": 1.0, "include_content": true, "limit": 5.0}); err != nil {
		t.Fatalf("geo_within: %v", err)
	}
	if _, err = (&MCPToolServer{client: geoDownClient{covBNewFake()}}).mcpCallTool(ctx, "geo_stats", nil); err == nil {
		t.Fatal("geo_stats error not returned")
	}
	if _, err = (&MCPToolServer{client: geoDownClient{covBNewFake()}}).mcpCallTool(ctx, "geo_within", map[string]interface{}{"collection": "places"}); err == nil {
		t.Fatal("geo_within error not returned")
	}
	// An index still loading says so rather than answering with nothing.
	loading, cleanupLoading := newHandlerTestServer(t)
	defer cleanupLoading()
	loading.GeoIndex = geo.NewGeoIndex()
	notReady := &MCPToolServer{client: NewDirectClient(loading), globalMode: ModeRW}
	if _, err := notReady.mcpCallTool(ctx, "geo_polygon", map[string]interface{}{"collection": "places", "polygon": square(1)}); err == nil || !strings.Contains(err.Error(), "loading") {
		t.Fatalf("loading index: %v", err)
	}
	// Outside the server process there is no index to ask.
	remote := &MCPToolServer{client: covBNewFake(), globalMode: ModeRW}
	if _, err := remote.mcpCallTool(ctx, "geo_polygon", map[string]interface{}{"collection": "places", "polygon": square(1)}); err == nil {
		t.Fatal("geo_polygon without a direct client")
	}
}
