package main

import (
	"context"

	"mddb/internal/geo"
	"mddb/internal/storage"
	"mddb/proto"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GeoSearch implements the GeoSearch RPC. Logic matches handleGeoSearch.
func (g *GRPCServer) GeoSearch(ctx context.Context, req *proto.GeoSearchRequest) (*proto.GeoSearchResponse, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if req.RadiusMeters <= 0 {
		return nil, status.Error(codes.InvalidArgument, "radius_meters must be > 0")
	}
	if !geo.ValidLatLng(req.Lat, req.Lng) {
		return nil, status.Error(codes.InvalidArgument, "invalid lat/lng")
	}
	if g.server.GeoIndex == nil || !g.server.GeoIndex.IsReady() {
		return nil, status.Error(codes.Unavailable, "geo index is loading")
	}

	var allowed map[string]struct{}
	if len(req.FilterMeta) > 0 {
		filterMeta := make(map[string][]string, len(req.FilterMeta))
		for k, v := range req.FilterMeta {
			filterMeta[k] = v.Values
		}
		ids := g.server.getDocIDsByMeta(req.Collection, filterMeta)
		if len(ids) == 0 {
			return &proto.GeoSearchResponse{
				RadiusMeters: req.RadiusMeters,
				Algorithm:    "rtree",
			}, nil
		}
		allowed = make(map[string]struct{}, len(ids))
		for id := range ids {
			allowed[id] = struct{}{}
		}
	}

	hits := g.server.GeoIndex.Search(req.Collection, req.Lat, req.Lng, req.RadiusMeters, int(req.TopK), allowed)
	results := make([]*proto.GeoSearchResultItem, 0, len(hits))
	_ = g.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(g.server.BucketNames.Docs)
		if bDocs == nil {
			return nil
		}
		for i, h := range hits {
			v := bDocs.Get(storage.DocKey(req.Collection, h.DocID))
			if v == nil {
				continue
			}
			d, err := unmarshalDoc(v)
			if err != nil {
				continue
			}
			if !req.IncludeContent {
				d.ContentMD = ""
			}
			results = append(results, &proto.GeoSearchResultItem{
				Document:       docToProto(d),
				DistanceMeters: h.DistanceMeters,
				Rank:           safeInt32(i + 1),
			})
		}
		return nil
	})

	return &proto.GeoSearchResponse{
		Results:      results,
		Total:        safeInt32(len(results)),
		RadiusMeters: req.RadiusMeters,
		Algorithm:    "rtree",
	}, nil
}

// GeoWithin implements the GeoWithin RPC. Logic matches handleGeoWithin.
func (g *GRPCServer) GeoWithin(ctx context.Context, req *proto.GeoWithinRequest) (*proto.GeoWithinResponse, error) {
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermRead); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if req.Collection == "" {
		return nil, status.Error(codes.InvalidArgument, "missing collection")
	}
	if req.MinLat > req.MaxLat || req.MinLng > req.MaxLng {
		return nil, status.Error(codes.InvalidArgument, "invalid bbox: min > max")
	}
	if g.server.GeoIndex == nil || !g.server.GeoIndex.IsReady() {
		return nil, status.Error(codes.Unavailable, "geo index is loading")
	}

	var allowed map[string]struct{}
	if len(req.FilterMeta) > 0 {
		filterMeta := make(map[string][]string, len(req.FilterMeta))
		for k, v := range req.FilterMeta {
			filterMeta[k] = v.Values
		}
		ids := g.server.getDocIDsByMeta(req.Collection, filterMeta)
		if len(ids) == 0 {
			return &proto.GeoWithinResponse{Algorithm: "rtree"}, nil
		}
		allowed = make(map[string]struct{}, len(ids))
		for id := range ids {
			allowed[id] = struct{}{}
		}
	}

	hits := g.server.GeoIndex.Within(req.Collection, req.MinLat, req.MaxLat, req.MinLng, req.MaxLng, allowed)
	results := make([]*proto.GeoSearchResultItem, 0, len(hits))
	_ = g.server.DBView(func(tx *bolt.Tx) error {
		bDocs := tx.Bucket(g.server.BucketNames.Docs)
		if bDocs == nil {
			return nil
		}
		for i, h := range hits {
			v := bDocs.Get(storage.DocKey(req.Collection, h.DocID))
			if v == nil {
				continue
			}
			d, err := unmarshalDoc(v)
			if err != nil {
				continue
			}
			if !req.IncludeContent {
				d.ContentMD = ""
			}
			results = append(results, &proto.GeoSearchResultItem{
				Document: docToProto(d),
				Rank:     safeInt32(i + 1),
			})
		}
		return nil
	})

	return &proto.GeoWithinResponse{
		Results:   results,
		Total:     safeInt32(len(results)),
		Algorithm: "rtree",
	}, nil
}

// GeoReindex implements the GeoReindex RPC. Matches handleGeoReindex.
func (g *GRPCServer) GeoReindex(ctx context.Context, req *proto.GeoReindexRequest) (*proto.GeoReindexResponse, error) {
	if g.isReadOnly() {
		return nil, status.Error(codes.PermissionDenied, "read-only mode")
	}
	if g.server.AuthManager != nil {
		if err := g.server.AuthManager.CheckPermission(ctx, req.Collection, PermWrite); err != nil {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
	}
	if g.server.GeoStore == nil || g.server.GeoIndex == nil {
		return nil, status.Error(codes.FailedPrecondition, "geo subsystem not initialized")
	}

	loaded := map[string]int32{}
	if len(req.LoadPostcodes) > 0 {
		pc := g.server.GeoIndex.Postcodes()
		if pc == nil {
			pc = geo.NewPostcodeLookup()
			g.server.GeoIndex.SetPostcodes(pc)
		}
		for _, p := range req.LoadPostcodes {
			// Same confinement as the HTTP path: write permission on a
			// collection is not authority to read an arbitrary file.
			csvPath, err := safeGeoCSVPath(p.CsvPath)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			n, err := pc.LoadCountry(p.Country, csvPath)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			loaded[p.Country] = safeInt32(n)
		}
	}

	count, err := g.server.GeoStore.Rebuild(g.server.GeoIndex, req.Collection)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	g.server.GeoIndex.SetReady()

	return &proto.GeoReindexResponse{
		Points:          safeInt32(count),
		Collection:      req.Collection,
		PostcodesLoaded: loaded,
	}, nil
}

// GeoEncode implements the GeoEncode RPC.
func (g *GRPCServer) GeoEncode(ctx context.Context, req *proto.GeoEncodeRequest) (*proto.GeoEncodeResponse, error) {
	_ = ctx
	if !geo.ValidLatLng(req.Lat, req.Lng) {
		return nil, status.Error(codes.InvalidArgument, "invalid lat/lng")
	}
	prec := int(req.Precision)
	if prec == 0 {
		prec = geo.GeohashMaxPrecision
	}
	h := geo.GeohashEncode(req.Lat, req.Lng, prec)
	if h == "" {
		return nil, status.Error(codes.Internal, "encoding failed")
	}
	return &proto.GeoEncodeResponse{Geohash: h, Precision: safeInt32(len(h))}, nil
}

// GeoDecode implements the GeoDecode RPC.
func (g *GRPCServer) GeoDecode(ctx context.Context, req *proto.GeoDecodeRequest) (*proto.GeoDecodeResponse, error) {
	_ = ctx
	if req.Geohash == "" {
		return nil, status.Error(codes.InvalidArgument, "missing geohash")
	}
	lat, lng, err := geo.GeohashDecode(req.Geohash)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	minLat, maxLat, minLng, maxLng, err := geo.GeohashBBox(req.Geohash)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &proto.GeoDecodeResponse{
		Lat:    lat,
		Lng:    lng,
		MinLat: minLat,
		MaxLat: maxLat,
		MinLng: minLng,
		MaxLng: maxLng,
	}, nil
}

// GeoStats implements the GeoStats RPC.
func (g *GRPCServer) GeoStats(ctx context.Context, req *proto.GeoStatsRequest) (*proto.GeoStatsResponse, error) {
	_ = req
	if g.server.GeoIndex == nil {
		return &proto.GeoStatsResponse{Collections: map[string]*proto.GeoCollectionStatProto{}}, nil
	}
	stats := map[string]*proto.GeoCollectionStatProto{}
	for _, c := range g.server.GeoIndex.Collections() {
		s := &proto.GeoCollectionStatProto{
			Points: safeInt32(g.server.GeoIndex.Len(c)),
		}
		if t := g.server.GeoIndex.LastRebuild(c); !t.IsZero() {
			s.LastRebuildUnix = t.Unix()
		}
		stats[c] = s
	}
	var pcStats map[string]int32
	if pc := g.server.GeoIndex.Postcodes(); pc != nil {
		raw := pc.Stats()
		pcStats = make(map[string]int32, len(raw))
		for k, v := range raw {
			pcStats[k] = safeInt32(v)
		}
	}
	return &proto.GeoStatsResponse{
		Collections:      stats,
		PostcodeDatasets: pcStats,
		Ready:            g.server.GeoIndex.IsReady(),
	}, nil
}
