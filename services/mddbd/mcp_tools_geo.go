package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mddb/internal/geo"
)

func (s *MCPToolServer) toolGeoSearch(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPGeoSearchRequest{
		Collection:   mcpGetString(args, "collection"),
		RadiusMeters: mcpGetFloat(args, "radius_meters"),
		TopK:         mcpGetInt(args, "top_k"),
		Algorithm:    mcpGetString(args, "algorithm"),
		FilterMeta:   mcpGetMetaMap(args, "filter_meta"),
	}
	req.Lat = mcpGetFloat(args, "lat")
	req.Lng = mcpGetFloat(args, "lng")
	if b, ok := args["include_content"].(bool); ok {
		req.IncludeContent = b
	}

	resp, err := s.client.GeoSearch(ctx, req)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(resp)
	return string(data), nil
}

func (s *MCPToolServer) toolGeoWithin(ctx context.Context, args map[string]interface{}) (string, error) {
	req := &MCPGeoWithinRequest{
		Collection: mcpGetString(args, "collection"),
		MinLat:     mcpGetFloat(args, "min_lat"),
		MaxLat:     mcpGetFloat(args, "max_lat"),
		MinLng:     mcpGetFloat(args, "min_lng"),
		MaxLng:     mcpGetFloat(args, "max_lng"),
		FilterMeta: mcpGetMetaMap(args, "filter_meta"),
	}
	if b, ok := args["include_content"].(bool); ok {
		req.IncludeContent = b
	}
	resp, err := s.client.GeoWithin(ctx, req)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(resp)
	return string(data), nil
}

// toolGeoPolygon runs a GeoJSON Polygon or MultiPolygon containment query
// against the in-memory R-tree. Unlike toolGeoSearch / toolGeoWithin it
// reaches into the server directly rather than going through the MCPClient
// interface — the polygon request shape doesn't justify four new RPCs on
// the interface, and polygon queries are always in-process anyway.
func (s *MCPToolServer) toolGeoPolygon(_ context.Context, args map[string]interface{}) (string, error) {
	dc, ok := s.client.(*DirectClient)
	if !ok || dc.server == nil || dc.server.GeoIndex == nil {
		return "", errors.New("geo_polygon requires direct (in-process) MCP mode with the geo index initialized")
	}
	if !dc.server.GeoIndex.IsReady() {
		return "", errors.New("geo index is loading, please retry")
	}
	collection := mcpGetString(args, "collection")
	if collection == "" {
		return "", errors.New("missing collection")
	}

	polygon, gotPolygon := extractPolygonArg(args, "polygon")
	multi, gotMulti := extractMultiPolygonArg(args, "multi_polygon")
	if gotPolygon == gotMulti {
		return "", errors.New("exactly one of polygon or multi_polygon must be set")
	}

	var allowed map[string]struct{}
	if fm := mcpGetMetaMap(args, "filter_meta"); len(fm) > 0 {
		ids := dc.server.getDocIDsByMeta(collection, fm)
		if len(ids) == 0 {
			return `{"results":[],"total":0,"shape":"` + polygonShapeFromArgs(gotPolygon) + `","algorithm":"rtree"}`, nil
		}
		allowed = make(map[string]struct{}, len(ids))
		for id := range ids {
			allowed[id] = struct{}{}
		}
	}

	var hits []geo.GeoResult
	var shape string
	if gotPolygon {
		if err := geo.ValidatePolygon(polygon); err != nil {
			return "", err
		}
		hits = dc.server.GeoIndex.SearchPolygon(collection, polygon.Coordinates, allowed)
		shape = "polygon"
	} else {
		if err := geo.ValidateMultiPolygon(multi); err != nil {
			return "", err
		}
		hits = dc.server.GeoIndex.SearchMultiPolygon(collection, multi.Coordinates, allowed)
		shape = "multiPolygon"
	}

	includeContent, _ := args["include_content"].(bool)
	items := dc.server.hydrateGeoResults(collection, hits, includeContent, false)
	resp := GeoPolygonResponse{
		Results:   items,
		Total:     len(items),
		Shape:     shape,
		Algorithm: "rtree",
	}
	data, _ := json.Marshal(resp)
	return string(data), nil
}

// extractPolygonArg coerces a JSON object coming off the MCP wire into the
// geo.GeoJSONPolygon shape the index layer expects. Returns (polygon, true) on
// success; (nil, false) when the key is missing entirely.
func extractPolygonArg(args map[string]interface{}, key string) (*geo.GeoJSONPolygon, bool) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, false
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	p := &geo.GeoJSONPolygon{}
	if err := json.Unmarshal(buf, p); err != nil {
		return nil, false
	}
	return p, true
}

// extractMultiPolygonArg is the MultiPolygon counterpart of the helper above.
func extractMultiPolygonArg(args map[string]interface{}, key string) (*geo.GeoJSONMultiPolygon, bool) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, false
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	mp := &geo.GeoJSONMultiPolygon{}
	if err := json.Unmarshal(buf, mp); err != nil {
		return nil, false
	}
	return mp, true
}

// polygonShapeFromArgs returns the response.shape label for the early-exit
// empty-result path so the JSON string matches whatever the caller asked for.
func polygonShapeFromArgs(gotPolygon bool) string {
	if gotPolygon {
		return "polygon"
	}
	return "multiPolygon"
}

func (s *MCPToolServer) toolGeoStats(ctx context.Context, args map[string]interface{}) (string, error) {
	_ = args
	resp, err := s.client.GeoStats(ctx)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(resp)
	return string(data), nil
}

func (s *MCPToolServer) toolGeoEncode(ctx context.Context, args map[string]interface{}) (string, error) {
	lat := mcpGetFloat(args, "lat")
	lng := mcpGetFloat(args, "lng")
	precision := mcpGetInt(args, "precision")
	h, err := s.client.GeoEncode(ctx, lat, lng, precision)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"geohash":%q,"precision":%d}`, h, len(h)), nil
}

func (s *MCPToolServer) toolGeoDecode(ctx context.Context, args map[string]interface{}) (string, error) {
	hash := mcpGetString(args, "geohash")
	lat, lng, err := s.client.GeoDecode(ctx, hash)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"lat":%v,"lng":%v}`, lat, lng), nil
}
