package main

import (
	"context"
	"strings"
	"testing"
)

// newGeoMCPToolServer builds an MCPToolServer with a DirectClient wired
// to a ready geo subsystem. The dispatch table in mcp_tools.go uses the
// tool name to call the per-tool handler; these tests exercise that
// end-to-end: args map → tool function → DirectClient → JSON string.
func newGeoMCPToolServer(t *testing.T) (*MCPToolServer, func()) {
	t.Helper()
	s, cleanup := newTestServerForGeo(t)
	return &MCPToolServer{
		client:     NewDirectClient(s),
		globalMode: ModeRW,
	}, cleanup
}

func TestMCPToolGeoSearch_Empty(t *testing.T) {
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	args := map[string]interface{}{
		"collection":    "v",
		"lat":           52.52,
		"lng":           13.405,
		"radius_meters": 1000.0,
	}
	out, err := ts.mcpCallTool(context.Background(), "geo_search", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"total":0`) {
		t.Errorf("expected total=0 in output, got: %s", out)
	}
	if !strings.Contains(out, `"algorithm":"rtree"`) {
		t.Errorf("expected algorithm=rtree, got: %s", out)
	}
}

func TestMCPToolGeoSearch_MissingCollection(t *testing.T) {
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	args := map[string]interface{}{
		"lat":           52.52,
		"lng":           13.405,
		"radius_meters": 1000.0,
	}
	_, err := ts.mcpCallTool(context.Background(), "geo_search", args)
	if err == nil {
		t.Error("expected error for missing collection")
	}
}

func TestMCPToolGeoWithin_Empty(t *testing.T) {
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	args := map[string]interface{}{
		"collection": "v",
		"min_lat":    0.0,
		"max_lat":    90.0,
		"min_lng":    0.0,
		"max_lng":    180.0,
	}
	out, err := ts.mcpCallTool(context.Background(), "geo_within", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"total":0`) {
		t.Errorf("expected total=0, got: %s", out)
	}
}

func TestMCPToolGeoStats(t *testing.T) {
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	out, err := ts.mcpCallTool(context.Background(), "geo_stats", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"ready":true`) {
		t.Errorf("expected ready=true, got: %s", out)
	}
}

func TestMCPToolGeoEncode(t *testing.T) {
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	// JSON numbers decode to float64 in interface{} maps — match that.
	args := map[string]interface{}{
		"lat":       52.52,
		"lng":       13.405,
		"precision": 8.0,
	}
	out, err := ts.mcpCallTool(context.Background(), "geo_encode", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"geohash":`) || !strings.Contains(out, `"precision":8`) {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestMCPToolGeoEncode_Invalid(t *testing.T) {
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	args := map[string]interface{}{
		"lat": 999.0,
		"lng": 0.0,
	}
	_, err := ts.mcpCallTool(context.Background(), "geo_encode", args)
	if err == nil {
		t.Error("expected error for invalid lat")
	}
}

func TestMCPToolGeoDecode(t *testing.T) {
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	args := map[string]interface{}{
		"geohash": "u33d8",
	}
	out, err := ts.mcpCallTool(context.Background(), "geo_decode", args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"lat"`) || !strings.Contains(out, `"lng"`) {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestMCPToolGeoDecode_Invalid(t *testing.T) {
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	_, err := ts.mcpCallTool(context.Background(), "geo_decode", map[string]interface{}{
		"geohash": "",
	})
	if err == nil {
		t.Error("expected error for empty geohash")
	}
}

func TestMCPToolReadOnlyModeAllowsGeo(t *testing.T) {
	// In read-only mode the dispatcher checks isToolReadOnly; all geo tools
	// are annotated readOnlyHint=true so they must succeed.
	ts, cleanup := newGeoMCPToolServer(t)
	defer cleanup()
	ts.globalMode = ModeRead
	args := map[string]interface{}{
		"collection":    "v",
		"lat":           52.52,
		"lng":           13.405,
		"radius_meters": 1000.0,
	}
	if _, err := ts.mcpCallTool(context.Background(), "geo_search", args); err != nil {
		t.Errorf("geo_search in read-only mode errored: %v", err)
	}
	if _, err := ts.mcpCallTool(context.Background(), "geo_stats", map[string]interface{}{}); err != nil {
		t.Errorf("geo_stats in read-only mode errored: %v", err)
	}
}
