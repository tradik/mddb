package main

import (
	"errors"
	"mddb/internal/geo"
	"mddb/internal/storage"
	"net/http"
	"time"

	bolt "go.etcd.io/bbolt"
	json "mddb/internal/jsonx"
)

// GeoSearchRequest is the JSON payload for POST /v1/geo-search.
type GeoSearchRequest struct {
	Collection     string              `json:"collection"`
	Lat            float64             `json:"lat"`
	Lng            float64             `json:"lng"`
	RadiusMeters   float64             `json:"radiusMeters"`
	TopK           int                 `json:"topK,omitempty"`
	Algorithm      string              `json:"algorithm,omitempty"` // "rtree" (default) or "geohash"
	FilterMeta     map[string][]string `json:"filterMeta,omitempty"`
	IncludeContent bool                `json:"includeContent,omitempty"`
}

// GeoEncodeRequest is the JSON payload for POST /v1/geo-encode.
type GeoEncodeRequest struct {
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Precision int     `json:"precision,omitempty"` // default 12
}

// GeoEncodeResponse returns the computed geohash.
type GeoEncodeResponse struct {
	Geohash   string `json:"geohash"`
	Precision int    `json:"precision"`
}

// GeoDecodeRequest is the JSON payload for POST /v1/geo-decode.
type GeoDecodeRequest struct {
	Geohash string `json:"geohash"`
}

// GeoDecodeResponse returns the decoded centroid and optional bbox.
type GeoDecodeResponse struct {
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
	MinLat float64 `json:"minLat"`
	MaxLat float64 `json:"maxLat"`
	MinLng float64 `json:"minLng"`
	MaxLng float64 `json:"maxLng"`
}

// GeoWithinRequest is the JSON payload for POST /v1/geo-within.
type GeoWithinRequest struct {
	Collection     string              `json:"collection"`
	MinLat         float64             `json:"minLat"`
	MaxLat         float64             `json:"maxLat"`
	MinLng         float64             `json:"minLng"`
	MaxLng         float64             `json:"maxLng"`
	FilterMeta     map[string][]string `json:"filterMeta,omitempty"`
	IncludeContent bool                `json:"includeContent,omitempty"`
}

// GeoSearchResultItem is a single item in a geo search response.
type GeoSearchResultItem struct {
	Document       storage.Doc `json:"document"`
	DistanceMeters float64     `json:"distanceMeters,omitempty"`
	Rank           int         `json:"rank"`
}

// GeoSearchResponse is returned from /v1/geo-search.
type GeoSearchResponse struct {
	Results      []GeoSearchResultItem `json:"results"`
	Total        int                   `json:"total"`
	RadiusMeters float64               `json:"radiusMeters"`
	Algorithm    string                `json:"algorithm"`
}

// GeoWithinResponse is returned from /v1/geo-within.
type GeoWithinResponse struct {
	Results   []GeoSearchResultItem `json:"results"`
	Total     int                   `json:"total"`
	Algorithm string                `json:"algorithm"`
}

// GeoPolygonRequest is the JSON payload for POST /v1/geo-polygon.
// Exactly one of Polygon or MultiPolygon must be set. When both are
// present the handler returns 400 so callers don't accidentally receive
// an intersection-of-whatever-we-felt-like-picking result.
type GeoPolygonRequest struct {
	Collection     string                   `json:"collection"`
	Polygon        *geo.GeoJSONPolygon      `json:"polygon,omitempty"`
	MultiPolygon   *geo.GeoJSONMultiPolygon `json:"multiPolygon,omitempty"`
	FilterMeta     map[string][]string      `json:"filterMeta,omitempty"`
	IncludeContent bool                     `json:"includeContent,omitempty"`
}

// GeoPolygonResponse is returned from /v1/geo-polygon.
type GeoPolygonResponse struct {
	Results   []GeoSearchResultItem `json:"results"`
	Total     int                   `json:"total"`
	Shape     string                `json:"shape"` // "polygon" or "multiPolygon"
	Algorithm string                `json:"algorithm"`
}

// GeoReindexRequest is the payload for POST /v1/geo-reindex.
type GeoReindexRequest struct {
	Collection    string            `json:"collection,omitempty"`    // empty = all collections
	LoadPostcodes []GeoPostcodeLoad `json:"loadPostcodes,omitempty"` // optional per-country CSVs to load
}

// GeoPostcodeLoad instructs the reindex handler to load a postcode CSV.
type GeoPostcodeLoad struct {
	Country string `json:"country"`
	CSVPath string `json:"csvPath"`
}

// GeoReindexResponse reports how many points were loaded.
type GeoReindexResponse struct {
	Points          int            `json:"points"`
	Collection      string         `json:"collection,omitempty"`
	PostcodesLoaded map[string]int `json:"postcodesLoaded,omitempty"`
	DurationMs      int64          `json:"durationMs"`
}

// GeoStatsResponse is returned from GET /v1/geo-stats.
type GeoStatsResponse struct {
	Collections      map[string]GeoCollectionStat `json:"collections"`
	PostcodeDatasets map[string]int               `json:"postcodeDatasets,omitempty"`
	Ready            bool                         `json:"ready"`
}

// GeoCollectionStat is per-collection info surfaced by /v1/geo-stats.
type GeoCollectionStat struct {
	Points      int       `json:"points"`
	LastRebuild time.Time `json:"lastRebuild,omitempty"`
}

// handleGeoSearch handles POST /v1/geo-search.
func (s *Server) handleGeoSearch(w http.ResponseWriter, r *http.Request) {
	var req GeoSearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if req.RadiusMeters <= 0 {
		bad(w, errors.New("radiusMeters must be > 0"))
		return
	}
	if !geo.ValidLatLng(req.Lat, req.Lng) {
		bad(w, errors.New("invalid lat/lng"))
		return
	}
	algo := req.Algorithm
	if algo == "" {
		algo = "rtree"
	}
	if algo != "rtree" && algo != "geohash" {
		bad(w, errors.New("unknown algorithm: "+algo+" (expected: rtree, geohash)"))
		return
	}
	if s.GeoIndex == nil || !s.GeoIndex.IsReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"geo index is loading, please retry"}`))
		return
	}

	var allowed map[string]struct{}
	if len(req.FilterMeta) > 0 {
		ids := s.getDocIDsByMeta(req.Collection, req.FilterMeta)
		if len(ids) == 0 {
			ok(w, GeoSearchResponse{Results: []GeoSearchResultItem{}, Total: 0, RadiusMeters: req.RadiusMeters, Algorithm: algo})
			return
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
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"geohash index is loading, please retry"}`))
			return
		}
		hits = s.GeoHashIndex.Search(req.Collection, req.Lat, req.Lng, req.RadiusMeters, req.TopK, allowed)
	default: // "rtree"
		hits = s.GeoIndex.Search(req.Collection, req.Lat, req.Lng, req.RadiusMeters, req.TopK, allowed)
	}
	items := s.hydrateGeoResults(req.Collection, hits, req.IncludeContent, true)
	ok(w, GeoSearchResponse{
		Results:      items,
		Total:        len(items),
		RadiusMeters: req.RadiusMeters,
		Algorithm:    algo,
	})
}

// handleGeoEncode handles POST /v1/geo-encode — convert (lat, lng) → geohash.
func (s *Server) handleGeoEncode(w http.ResponseWriter, r *http.Request) {
	var req GeoEncodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if !geo.ValidLatLng(req.Lat, req.Lng) {
		bad(w, errors.New("invalid lat/lng"))
		return
	}
	prec := req.Precision
	if prec == 0 {
		prec = geo.GeohashMaxPrecision
	}
	h := geo.GeohashEncode(req.Lat, req.Lng, prec)
	if h == "" {
		bad(w, errors.New("encoding failed"))
		return
	}
	ok(w, GeoEncodeResponse{Geohash: h, Precision: len(h)})
}

// handleGeoDecode handles POST /v1/geo-decode — convert geohash → centroid + bbox.
func (s *Server) handleGeoDecode(w http.ResponseWriter, r *http.Request) {
	var req GeoDecodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Geohash == "" {
		bad(w, errors.New("missing geohash"))
		return
	}
	lat, lng, err := geo.GeohashDecode(req.Geohash)
	if err != nil {
		bad(w, err)
		return
	}
	minLat, maxLat, minLng, maxLng, err := geo.GeohashBBox(req.Geohash)
	if err != nil {
		bad(w, err)
		return
	}
	ok(w, GeoDecodeResponse{
		Lat:    lat,
		Lng:    lng,
		MinLat: minLat,
		MaxLat: maxLat,
		MinLng: minLng,
		MaxLng: maxLng,
	})
}

// handleGeoWithin handles POST /v1/geo-within.
func (s *Server) handleGeoWithin(w http.ResponseWriter, r *http.Request) {
	var req GeoWithinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if req.MinLat > req.MaxLat || req.MinLng > req.MaxLng {
		bad(w, errors.New("invalid bbox: min > max"))
		return
	}
	if s.GeoIndex == nil || !s.GeoIndex.IsReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"geo index is loading, please retry"}`))
		return
	}

	var allowed map[string]struct{}
	if len(req.FilterMeta) > 0 {
		ids := s.getDocIDsByMeta(req.Collection, req.FilterMeta)
		if len(ids) == 0 {
			ok(w, GeoWithinResponse{Results: []GeoSearchResultItem{}, Total: 0, Algorithm: "rtree"})
			return
		}
		allowed = make(map[string]struct{}, len(ids))
		for id := range ids {
			allowed[id] = struct{}{}
		}
	}

	hits := s.GeoIndex.Within(req.Collection, req.MinLat, req.MaxLat, req.MinLng, req.MaxLng, allowed)
	items := s.hydrateGeoResults(req.Collection, hits, req.IncludeContent, false)
	ok(w, GeoWithinResponse{
		Results:   items,
		Total:     len(items),
		Algorithm: "rtree",
	})
}

// handleGeoPolygon handles POST /v1/geo-polygon. Accepts a GeoJSON Polygon
// (outer ring + optional holes) or a MultiPolygon (union of polygons) and
// returns every indexed point whose coordinates fall inside the shape.
// The R-tree's bounding-box query narrows the candidate set before the
// ray-cast test, so response time tracks the shape's bbox size rather
// than the collection size.
func (s *Server) handleGeoPolygon(w http.ResponseWriter, r *http.Request) {
	var req GeoPolygonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	if req.Collection == "" {
		bad(w, errors.New("missing collection"))
		return
	}
	if (req.Polygon == nil) == (req.MultiPolygon == nil) {
		bad(w, errors.New("exactly one of polygon or multiPolygon must be set"))
		return
	}
	if req.Polygon != nil {
		if err := geo.ValidatePolygon(req.Polygon); err != nil {
			bad(w, err)
			return
		}
	} else {
		if err := geo.ValidateMultiPolygon(req.MultiPolygon); err != nil {
			bad(w, err)
			return
		}
	}
	if s.GeoIndex == nil || !s.GeoIndex.IsReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"geo index is loading, please retry"}`))
		return
	}

	var allowed map[string]struct{}
	if len(req.FilterMeta) > 0 {
		ids := s.getDocIDsByMeta(req.Collection, req.FilterMeta)
		if len(ids) == 0 {
			ok(w, GeoPolygonResponse{
				Results:   []GeoSearchResultItem{},
				Total:     0,
				Shape:     polygonShapeLabel(req),
				Algorithm: "rtree",
			})
			return
		}
		allowed = make(map[string]struct{}, len(ids))
		for id := range ids {
			allowed[id] = struct{}{}
		}
	}

	var hits []geo.GeoResult
	var shape string
	if req.Polygon != nil {
		hits = s.GeoIndex.SearchPolygon(req.Collection, req.Polygon.Coordinates, allowed)
		shape = "polygon"
	} else {
		hits = s.GeoIndex.SearchMultiPolygon(req.Collection, req.MultiPolygon.Coordinates, allowed)
		shape = "multiPolygon"
	}
	items := s.hydrateGeoResults(req.Collection, hits, req.IncludeContent, false)
	if s.Metrics != nil {
		s.Metrics.IncOp("geo_polygon", shape)
	}
	ok(w, GeoPolygonResponse{
		Results:   items,
		Total:     len(items),
		Shape:     shape,
		Algorithm: "rtree",
	})
}

// polygonShapeLabel picks the response.shape value for an early-exit
// empty-result path so both branches report which shape type was served.
func polygonShapeLabel(req GeoPolygonRequest) string {
	if req.Polygon != nil {
		return "polygon"
	}
	return "multiPolygon"
}

// handleGeoReindex handles POST /v1/geo-reindex.
func (s *Server) handleGeoReindex(w http.ResponseWriter, r *http.Request) {
	var req GeoReindexRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		bad(w, err)
		return
	}
	// The gRPC twin of this call checks a permission and this one checked
	// none, so the same operation was gated two different ways depending on
	// which port it arrived at. Admin rather than write: loading a CSV reads
	// the filesystem, which is not the authority "may write to a collection"
	// grants.
	if s.AuthManager != nil && len(req.LoadPostcodes) > 0 {
		if err := s.AuthManager.CheckPermission(r.Context(), "*", PermAdmin); err != nil {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
	}
	if s.GeoStore == nil || s.GeoIndex == nil {
		bad(w, errors.New("geo subsystem not initialized"))
		return
	}

	start := time.Now()
	loaded := map[string]int{}
	if len(req.LoadPostcodes) > 0 {
		pc := s.GeoIndex.Postcodes()
		if pc == nil {
			pc = geo.NewPostcodeLookup()
			s.GeoIndex.SetPostcodes(pc)
		}
		for _, p := range req.LoadPostcodes {
			// CodeQL go/path-injection. csvPath came from the request body and
			// went straight to os.Open, so this endpoint would read any file
			// the process could — and unlike its gRPC twin it checked no
			// permission at all. Confined to the geo data directory now, and
			// gated below.
			csvPath, err := safeGeoCSVPath(p.CSVPath)
			if err != nil {
				bad(w, err)
				return
			}
			n, err := pc.LoadCountry(p.Country, csvPath)
			if err != nil {
				bad(w, err)
				return
			}
			loaded[p.Country] = n
		}
	}

	count, err := s.GeoStore.Rebuild(s.GeoIndex, req.Collection)
	if err != nil {
		bad(w, err)
		return
	}
	s.GeoIndex.SetReady()

	// Rebuild the geohash index as well so both algorithms stay in sync
	// with the on-disk state after a reindex.
	if s.GeoHashIndex != nil {
		if _, err := s.GeoStore.RebuildHash(s.GeoHashIndex, req.Collection); err != nil {
			bad(w, err)
			return
		}
		s.GeoHashIndex.SetReady()
	}

	ok(w, GeoReindexResponse{
		Points:          count,
		Collection:      req.Collection,
		PostcodesLoaded: loaded,
		DurationMs:      time.Since(start).Milliseconds(),
	})
}

// handleGeoStats handles GET /v1/geo-stats.
func (s *Server) handleGeoStats(w http.ResponseWriter, r *http.Request) {
	if s.GeoIndex == nil {
		ok(w, GeoStatsResponse{Collections: map[string]GeoCollectionStat{}, Ready: false})
		return
	}
	stats := map[string]GeoCollectionStat{}
	for _, c := range s.GeoIndex.Collections() {
		stats[c] = GeoCollectionStat{
			Points:      s.GeoIndex.Len(c),
			LastRebuild: s.GeoIndex.LastRebuild(c),
		}
	}
	var pcStats map[string]int
	if pc := s.GeoIndex.Postcodes(); pc != nil {
		pcStats = pc.Stats()
	}
	ok(w, GeoStatsResponse{
		Collections:      stats,
		PostcodeDatasets: pcStats,
		Ready:            s.GeoIndex.IsReady(),
	})
}

// hydrateGeoResults turns raw geo.GeoResult entries into GeoSearchResultItems by
// loading the underlying storage.Doc from BoltDB. If includeContent is false, the
// ContentMd field is stripped to keep responses small (matches the pattern
// used by vector/FTS handlers).
func (s *Server) hydrateGeoResults(collection string, hits []geo.GeoResult, includeContent, includeDistance bool) []GeoSearchResultItem {
	if len(hits) == 0 {
		return []GeoSearchResultItem{}
	}
	items := make([]GeoSearchResultItem, 0, len(hits))
	_ = s.DBView(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("docs"))
		if b == nil {
			return nil
		}
		for i, h := range hits {
			v := b.Get(storage.DocKey(collection, h.DocID))
			if v == nil {
				continue
			}
			docPtr, err := loadDoc(v)
			if err != nil || docPtr == nil {
				continue
			}
			d := *docPtr
			if !includeContent {
				d.ContentMD = ""
			}
			item := GeoSearchResultItem{Document: d, Rank: i + 1}
			if includeDistance {
				item.DistanceMeters = h.DistanceMeters
			}
			items = append(items, item)
		}
		return nil
	})
	return items
}
