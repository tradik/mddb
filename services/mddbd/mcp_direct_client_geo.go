package main

import (
	"context"
	"errors"
	"mddb/internal/geo"
	"mddb/internal/storage"

	bolt "go.etcd.io/bbolt"
)

// GeoSearch performs a radius search via the direct client. Mirrors the
// HTTP handler at handleGeoSearch and the gRPC wrapper at GRPCServer.GeoSearch.
func (c *DirectClient) GeoSearch(ctx context.Context, req *MCPGeoSearchRequest) (*MCPGeoSearchResponse, error) {
	_ = ctx
	s := c.server
	if req.Collection == "" {
		return nil, errors.New("missing collection")
	}
	if req.RadiusMeters <= 0 {
		return nil, errors.New("radiusMeters must be > 0")
	}
	if !geo.ValidLatLng(req.Lat, req.Lng) {
		return nil, errors.New("invalid lat/lng")
	}
	algo := req.Algorithm
	if algo == "" {
		algo = "rtree"
	}
	if algo != "rtree" && algo != "geohash" {
		return nil, errors.New("unknown algorithm: " + algo)
	}

	var allowed map[string]struct{}
	if len(req.FilterMeta) > 0 {
		ids := s.getDocIDsByMeta(req.Collection, req.FilterMeta)
		if len(ids) == 0 {
			return &MCPGeoSearchResponse{
				Results:      []MCPGeoSearchResult{},
				RadiusMeters: req.RadiusMeters,
				Algorithm:    algo,
			}, nil
		}
		allowed = make(map[string]struct{}, len(ids))
		for id := range ids {
			allowed[id] = struct{}{}
		}
	}

	var hits []geo.GeoResult
	switch algo {
	case "geohash":
		if s.GeoHashIndex == nil || !s.GeoHashIndex.IsReady() {
			return nil, errors.New("geohash index is loading")
		}
		hits = s.GeoHashIndex.Search(req.Collection, req.Lat, req.Lng, req.RadiusMeters, req.TopK, allowed)
	default:
		if s.GeoIndex == nil || !s.GeoIndex.IsReady() {
			return nil, errors.New("geo index is loading")
		}
		hits = s.GeoIndex.Search(req.Collection, req.Lat, req.Lng, req.RadiusMeters, req.TopK, allowed)
	}
	items := c.loadGeoResults(req.Collection, hits, req.IncludeContent, true)
	return &MCPGeoSearchResponse{
		Results:      items,
		Total:        len(items),
		RadiusMeters: req.RadiusMeters,
		Algorithm:    algo,
	}, nil
}

// GeoWithin performs a bbox search via the direct client.
func (c *DirectClient) GeoWithin(ctx context.Context, req *MCPGeoWithinRequest) (*MCPGeoSearchResponse, error) {
	_ = ctx
	s := c.server
	if req.Collection == "" {
		return nil, errors.New("missing collection")
	}
	if req.MinLat > req.MaxLat || req.MinLng > req.MaxLng {
		return nil, errors.New("invalid bbox: min > max")
	}
	if s.GeoIndex == nil || !s.GeoIndex.IsReady() {
		return nil, errors.New("geo index is loading")
	}

	var allowed map[string]struct{}
	if len(req.FilterMeta) > 0 {
		ids := s.getDocIDsByMeta(req.Collection, req.FilterMeta)
		if len(ids) == 0 {
			return &MCPGeoSearchResponse{
				Results:   []MCPGeoSearchResult{},
				Algorithm: "rtree",
			}, nil
		}
		allowed = make(map[string]struct{}, len(ids))
		for id := range ids {
			allowed[id] = struct{}{}
		}
	}
	hits := s.GeoIndex.Within(req.Collection, req.MinLat, req.MaxLat, req.MinLng, req.MaxLng, allowed)
	items := c.loadGeoResults(req.Collection, hits, req.IncludeContent, false)
	return &MCPGeoSearchResponse{
		Results:   items,
		Total:     len(items),
		Algorithm: "rtree",
	}, nil
}

// GeoStats returns a compact per-collection point count summary. Keeps the
// MCP surface tiny compared to the HTTP version, which also returns
// lastRebuild timestamps — the MCP channel is optimized for LLMs, and
// rebuild times are not actionable for them.
func (c *DirectClient) GeoStats(ctx context.Context) (*MCPGeoStatsResponse, error) {
	_ = ctx
	s := c.server
	if s.GeoIndex == nil {
		return &MCPGeoStatsResponse{Collections: map[string]int{}}, nil
	}
	cols := map[string]int{}
	for _, name := range s.GeoIndex.Collections() {
		cols[name] = s.GeoIndex.Len(name)
	}
	var pcStats map[string]int
	if pc := s.GeoIndex.Postcodes(); pc != nil {
		pcStats = pc.Stats()
	}
	return &MCPGeoStatsResponse{
		Collections:      cols,
		PostcodeDatasets: pcStats,
		Ready:            s.GeoIndex.IsReady(),
	}, nil
}

// GeoEncode returns the geohash for a (lat, lng) at the requested precision.
// Precision is clamped to [1, 12] by geo.GeohashEncode.
func (c *DirectClient) GeoEncode(ctx context.Context, lat, lng float64, precision int) (string, error) {
	_ = ctx
	if !geo.ValidLatLng(lat, lng) {
		return "", errors.New("invalid lat/lng")
	}
	if precision == 0 {
		precision = geo.GeohashMaxPrecision
	}
	h := geo.GeohashEncode(lat, lng, precision)
	if h == "" {
		return "", errors.New("encoding failed")
	}
	return h, nil
}

// GeoDecode returns the centroid (lat, lng) of a geohash cell.
func (c *DirectClient) GeoDecode(ctx context.Context, hash string) (float64, float64, error) {
	_ = ctx
	if hash == "" {
		return 0, 0, errors.New("missing geohash")
	}
	return geo.GeohashDecode(hash)
}

// loadGeoResults hydrates R-tree / geohash candidate hits into MCP result
// items by reading the underlying storage.Doc from BoltDB. Shared between
// GeoSearch and GeoWithin to avoid duplicate loops.
func (c *DirectClient) loadGeoResults(collection string, hits []geo.GeoResult, includeContent, includeDistance bool) []MCPGeoSearchResult {
	if len(hits) == 0 {
		return []MCPGeoSearchResult{}
	}
	items := make([]MCPGeoSearchResult, 0, len(hits))
	_ = c.server.DBViewC(collection, func(tx *bolt.Tx) error {
		b := tx.Bucket(c.server.BucketNames.Docs)
		if b == nil {
			return nil
		}
		for i, h := range hits {
			v := b.Get(storage.DocKey(collection, h.DocID))
			if v == nil {
				continue
			}
			d, err := unmarshalDoc(v)
			if err != nil || d == nil {
				continue
			}
			doc := docToMCPDocument(*d)
			if !includeContent {
				doc.ContentMD = ""
			}
			item := MCPGeoSearchResult{Document: doc, Rank: i + 1}
			if includeDistance {
				item.DistanceMeters = h.DistanceMeters
			}
			items = append(items, item)
		}
		return nil
	})
	return items
}
